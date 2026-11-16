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
	UpdateProviderStatus(resource.Event) error
}

type Reloader struct {
	dir           string
	legacyFile    string
	supervisor    *Supervisor
	runtime       ProviderRuntime
	registry      *Registry
	registrations map[string]models.ProviderRegistration
	failures      map[string]struct{}
}

func NewReloader(dir, legacyFile string, supervisor *Supervisor, runtime ProviderRuntime, registry *Registry, registrations []models.ProviderRegistration) *Reloader {
	failures := make(map[string]struct{})
	for name := range runtime.Manifest().ProviderErrors {
		failures[name] = struct{}{}
	}
	return &Reloader{dir: dir, legacyFile: legacyFile, supervisor: supervisor, runtime: runtime, registry: registry, registrations: registrationMap(registrations), failures: failures}
}

func (r *Reloader) Reload() error {
	registrations, failures := RegistrationsDir(r.dir, r.legacyFile)
	for name := range r.failures {
		if _, exists := failures[name]; !exists {
			r.runtime.SetProviderError(name, nil)
		}
	}
	for name, err := range failures {
		r.runtime.SetProviderError(name, err)
	}
	r.failures = make(map[string]struct{}, len(failures))
	for name := range failures {
		r.failures[name] = struct{}{}
	}
	desired := registrationMap(registrations)
	if !reflect.DeepEqual(r.registrations, desired) {
		if err := r.supervisor.Reload(registrations); err != nil {
			r.runtime.SetProviderError(RegistryErrorName, err)
			return err
		}
		if err := r.registry.Reload(registrations); err != nil {
			r.runtime.SetProviderError(RegistryErrorName, err)
			return err
		}
		r.registrations = desired
	}
	r.runtime.SetProviderError(RegistryErrorName, nil)
	for name := range desired {
		r.runtime.SetProviderError(name, r.supervisor.ProviderError(name))
	}
	return nil
}

func registrationMap(registrations []models.ProviderRegistration) map[string]models.ProviderRegistration {
	result := make(map[string]models.ProviderRegistration, len(registrations))
	for _, registration := range registrations {
		result[registration.Provider.Name] = registration
	}
	return result
}
