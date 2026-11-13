/*
  Copyright © 2026 Alexey Shulutkov <github@shulutkov.ru>

  Licensed under the Apache License, Version 2.0 (the "License");
  you may not use this file except in compliance with the License.
  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific language governing permissions and
  limitations under the License.
*/

package remote

import (
	"fmt"
	"reflect"

	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"
)

type ProviderRuntime interface {
	Manifest() models.Manifest
	Register(resource.Provider) error
	Unregister(string)
	SetProviderError(string, error)
	Resync()
}

type Reloader struct {
	dir           string
	legacyFile    string
	supervisor    *Supervisor
	runtime       ProviderRuntime
	registrations map[string]models.ProviderRegistration
}

func NewReloader(dir, legacyFile string, supervisor *Supervisor, runtime ProviderRuntime, registrations []models.ProviderRegistration) *Reloader {
	return &Reloader{dir: dir, legacyFile: legacyFile, supervisor: supervisor, runtime: runtime, registrations: registrationMap(registrations)}
}

func (r *Reloader) Reload() error {
	registrations, err := RegistrationsDir(r.dir, r.legacyFile)
	if err != nil {
		return err
	}
	desired := registrationMap(registrations)
	if reflect.DeepEqual(r.registrations, desired) {
		return nil
	}
	if err := r.supervisor.Reload(registrations); err != nil {
		return err
	}
	current := make(map[string]struct{})
	for _, provider := range r.runtime.Manifest().Providers {
		current[provider.Name] = struct{}{}
		if _, exists := desired[provider.Name]; !exists {
			r.runtime.Unregister(provider.Name)
			delete(current, provider.Name)
		}
	}
	for name, registration := range desired {
		previous, existed := r.registrations[name]
		_, registered := current[name]
		if registered && existed && reflect.DeepEqual(previous, registration) {
			continue
		}
		if registered {
			r.runtime.Unregister(name)
		}
		providers, err := loadRegistrations([]models.ProviderRegistration{registration}, nil)
		if err != nil {
			if processErr := r.supervisor.ProviderError(name); processErr != nil {
				err = fmt.Errorf("%v; %w", processErr, err)
			}
			err = fmt.Errorf("reload provider %s: %w", name, err)
			r.runtime.SetProviderError(name, err)
			return err
		}
		if len(providers) != 1 {
			err := fmt.Errorf("reload provider %s: invalid registration", name)
			r.runtime.SetProviderError(name, err)
			return err
		}
		if err := r.runtime.Register(providers[0]); err != nil {
			err = fmt.Errorf("reload provider %s: %w", name, err)
			r.runtime.SetProviderError(name, err)
			return err
		}
	}
	r.registrations = desired
	r.runtime.Resync()
	return nil
}

func registrationMap(registrations []models.ProviderRegistration) map[string]models.ProviderRegistration {
	result := make(map[string]models.ProviderRegistration, len(registrations))
	for _, registration := range registrations {
		result[registration.Provider.Name] = registration
	}
	return result
}
