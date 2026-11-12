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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ks-tool/vpn-agent/internal/resource"
	"github.com/ks-tool/vpn-agent/models"
)

type reloaderRuntime struct {
	providers map[string]resource.Provider
	resyncs   int
}

func (r *reloaderRuntime) Manifest() models.Manifest {
	manifest := models.Manifest{ProtocolVersion: models.ProtocolVersion}
	for _, provider := range r.providers {
		manifest.Providers = append(manifest.Providers, models.ProviderDefinition{Name: provider.Name(), Version: provider.Version(), Resources: provider.Definitions()})
	}
	return manifest
}
func (r *reloaderRuntime) Register(provider resource.Provider) error {
	r.providers[provider.Name()] = provider
	return nil
}
func (r *reloaderRuntime) Unregister(name string) { delete(r.providers, name) }
func (r *reloaderRuntime) Resync()                { r.resyncs++ }

func TestReloaderAddsUpdatesAndRemovesProviders(t *testing.T) {
	dir := t.TempDir()
	providerDir := filepath.Join(dir, "providers.d")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(dir, "providers.conf")
	first := models.ProviderRegistration{Address: "http://127.0.0.1:9091", Provider: models.ProviderDefinition{Name: "awg", Version: "1"}}
	second := models.ProviderRegistration{Address: "http://127.0.0.1:9092", Provider: models.ProviderDefinition{Name: "xray", Version: "1"}}
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
	reloader := NewReloader(providerDir, legacyFile, supervisor, runtime, []models.ProviderRegistration{first})
	providers, err := loadRegistrations([]models.ProviderRegistration{first}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register(providers[0]); err != nil {
		t.Fatal(err)
	}

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
	if runtime.providers["xray"].Version() != "2" {
		t.Fatalf("provider was not updated: %+v", runtime.Manifest().Providers)
	}
	writeRegistrations(updated)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.providers) != 1 || runtime.providers["awg"] != nil {
		t.Fatalf("provider was not removed: %+v", runtime.Manifest().Providers)
	}
}

func TestReloaderDiscoversAddedProvider(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/manifest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(models.Manifest{
			ProtocolVersion: models.ProtocolVersion,
			Providers: []models.ProviderDefinition{{
				Name: "awg", Version: "1",
				Resources: []models.ResourceDefinition{{APIVersion: "awg.ks-tool.dev/v1", Kind: "Interface"}},
			}},
		})
	}))
	defer providerServer.Close()

	dir := t.TempDir()
	registration := models.ProviderRegistration{
		Address: providerServer.URL, Discover: true,
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
	reloader := NewReloader(dir, filepath.Join(t.TempDir(), "providers.conf"), supervisor, runtime, nil)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	manifest := runtime.Manifest()
	if len(manifest.Providers) != 1 || len(manifest.Providers[0].Resources) != 1 {
		t.Fatalf("discovered provider is missing from manifest: %+v", manifest.Providers)
	}
}
