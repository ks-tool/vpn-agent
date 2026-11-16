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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"
)

type reloaderRuntime struct {
	providers map[string]resource.Provider
	errors    map[string]string
	resyncs   int
}

func (r *reloaderRuntime) Manifest() models.Manifest {
	manifest := models.Manifest{ProtocolVersion: models.ProtocolVersion, ProviderErrors: r.errors}
	for _, provider := range r.providers {
		manifest.Providers = append(manifest.Providers, models.ProviderDefinition{Name: provider.Name(), Version: provider.Version(), Resources: provider.Definitions()})
	}
	return manifest
}
func (r *reloaderRuntime) Register(provider resource.Provider) error {
	r.providers[provider.Name()] = provider
	delete(r.errors, provider.Name())
	return nil
}
func (r *reloaderRuntime) Unregister(name string) { delete(r.providers, name) }
func (r *reloaderRuntime) SetProviderError(name string, err error) {
	if r.errors == nil {
		r.errors = map[string]string{}
	}
	if err == nil {
		delete(r.errors, name)
		return
	}
	r.errors[name] = err.Error()
}
func (r *reloaderRuntime) Resync()                                   { r.resyncs++ }
func (r *reloaderRuntime) UpdateProviderStatus(resource.Event) error { return nil }

func TestReloaderAddsUpdatesAndRemovesProviders(t *testing.T) {
	dir := t.TempDir()
	providerDir := filepath.Join(dir, "providers.d")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(dir, "providers.conf")
	first := models.ProviderRegistration{Provider: models.ProviderDefinition{Name: "awg", Version: "1"}}
	second := models.ProviderRegistration{Provider: models.ProviderDefinition{Name: "xray", Version: "1"}}
	writeRegistrations := func(registrations ...models.ProviderRegistration) {
		t.Helper()
		data, err := json.Marshal(Config{ProtocolVersion: models.ProtocolVersion, Providers: registrations})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(providerDir, "providers.conf"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeRegistrations(first)
	supervisor := NewSupervisor(nil)
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	runtime := &reloaderRuntime{providers: make(map[string]resource.Provider)}
	registry := NewRegistry(runtime)
	if err := registry.Reload([]models.ProviderRegistration{first}); err != nil {
		t.Fatal(err)
	}
	reloader := NewReloader(providerDir, legacyFile, supervisor, runtime, registry, []models.ProviderRegistration{first})

	writeRegistrations(first, second)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.providers) != 2 || runtime.providers["xray"].Version() != "1" {
		t.Fatalf("provider was not added: %+v", runtime.Manifest().Providers)
	}
	updated := second
	updated.Provider.Version = "2"
	writeRegistrations(first, updated)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	if reloader.registrations["xray"].Provider.Version != "2" {
		t.Fatalf("registration was not updated: %+v", reloader.registrations["xray"])
	}
	writeRegistrations(updated)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.providers) != 1 || runtime.providers["awg"] != nil {
		t.Fatalf("provider was not removed: %+v", runtime.Manifest().Providers)
	}
}

func TestReloaderRegistersProviderBeforeProcessConnects(t *testing.T) {
	dir := t.TempDir()
	registration := models.ProviderRegistration{
		Command:  filepath.Join(dir, "provider"),
		Provider: models.ProviderDefinition{Name: "awg"},
	}
	data, err := json.Marshal(Config{ProtocolVersion: models.ProtocolVersion, Providers: []models.ProviderRegistration{registration}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "awg.conf"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	supervisor := NewSupervisor(nil)
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	runtime := &reloaderRuntime{providers: make(map[string]resource.Provider)}
	registry := NewRegistry(runtime)
	reloader := NewReloader(dir, filepath.Join(t.TempDir(), "providers.conf"), supervisor, runtime, registry, nil)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	manifest := runtime.Manifest()
	if len(manifest.Providers) != 1 || manifest.Providers[0].Name != "awg" {
		t.Fatalf("pending provider is missing from manifest: %+v", manifest.Providers)
	}
}

func TestReloaderClearsInitialRegistrationErrorWhenFileIsRemoved(t *testing.T) {
	dir := t.TempDir()
	supervisor := NewSupervisor(nil)
	if err := supervisor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Close()
	runtime := &reloaderRuntime{providers: make(map[string]resource.Provider), errors: map[string]string{"awg": "invalid registration"}}
	reloader := NewReloader(dir, filepath.Join(dir, "providers.conf"), supervisor, runtime, NewRegistry(runtime), nil)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, exists := runtime.errors["awg"]; exists {
		t.Fatalf("stale registration error was not cleared: %+v", runtime.errors)
	}
}
