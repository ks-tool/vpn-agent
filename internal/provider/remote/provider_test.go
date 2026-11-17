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
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ks-tool/vpn-agent/v2/internal/api"
	"github.com/ks-tool/vpn-agent/v2/internal/provider/transport"
	"github.com/ks-tool/vpn-agent/v2/internal/provider/transport/providerv1"
	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/ks-tool/vpn-agent/v2/providers/example"
	"github.com/ks-tool/vpn-agent/v2/storage"
)

type providerClient struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startProviderClient(t *testing.T, socket string, definition models.ProviderDefinition, runtime *resource.Runtime) *providerClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	client := &providerClient{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(client.done)
		handler := http.NewServeMux()
		handler.Handle("/v2/", api.NewResourceHandler(runtime))
		handler.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"bridged":true}`))
		})
		if err := transport.Serve(ctx, "unix://"+socket, definition, handler, runtime); err != nil {
			t.Errorf("provider client: %v", err)
		}
	}()
	return client
}

func (c *providerClient) stop(t *testing.T) {
	t.Helper()
	c.cancel()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("provider client did not stop")
	}
}

func startProviderPair(t *testing.T, externalProvider resource.Provider, options ...resource.RuntimeOption) (*resource.Runtime, *resource.Runtime, *Registry, string, models.ProviderDefinition) {
	t.Helper()
	definition := models.ProviderDefinition{Name: "example", Version: "0.1.0", Resources: externalProvider.Definitions()}
	externalStore, err := resource.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	externalRuntime := resource.NewRuntime(externalStore, time.Millisecond, time.Hour)
	if err := externalRuntime.Register(externalProvider); err != nil {
		t.Fatal(err)
	}
	externalRuntime.Start(context.Background())

	coreStore, err := resource.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	core := resource.NewRuntime(coreStore, time.Millisecond, time.Hour, options...)
	core.Start(context.Background())
	registry := NewRegistry(core)
	registration := models.ProviderRegistration{Provider: models.ProviderDefinition{Name: definition.Name}}
	if err := registry.Reload([]models.ProviderRegistration{registration}); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("/tmp", "vpn-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "p.sock")
	if err := registry.Start(socket); err != nil {
		t.Fatal(err)
	}
	return core, externalRuntime, registry, socket, definition
}

func closeProviderPair(core, external *resource.Runtime, registry *Registry) {
	registry.Close()
	external.Close()
	core.Close()
}

func TestExternalProviderLifecycleDisconnectAndReconnect(t *testing.T) {
	exampleProvider, err := example.New()
	if err != nil {
		t.Fatal(err)
	}
	core, externalRuntime, registry, socket, definition := startProviderPair(t, exampleProvider)
	defer closeProviderPair(core, externalRuntime, registry)
	client := startProviderClient(t, socket, definition, externalRuntime)
	waitManifest(t, core)
	if _, err := core.Put("example", "Endpoint", "public", models.Resource{APIVersion: "example.ks-tool.dev/v1", Spec: json.RawMessage(`{"title":"Public","address":"192.0.2.1"}`)}); err != nil {
		t.Fatal(err)
	}
	waitRemoteStatus(t, core, "public", models.ResourceReady)
	result, err := core.Action(context.Background(), "example", "Endpoint", "public", "inspect", json.RawMessage(`{}`))
	if err != nil || string(result.Data) != `{"title":"Public","address":"192.0.2.1"}` {
		t.Fatalf("external action: %+v, %v", result, err)
	}
	bridged, err := core.Bridge(context.Background(), "example", http.MethodGet, "/metrics", nil, nil)
	if err != nil || string(bridged.Body) != `{"bridged":true}` {
		t.Fatalf("external bridge: %+v, %v", bridged, err)
	}
	link := models.Link{ID: "link", Kind: "example", Generation: 1, Source: models.LinkEndpoint{ResourceID: "a"}, Target: models.LinkEndpoint{ResourceID: "b"}}
	offer, err := core.PrepareLink(context.Background(), "example", link, link.Source)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.CommitLink(context.Background(), "example", link, link.Source, offer); err != nil {
		t.Fatal(err)
	}

	client.stop(t)
	if _, err := core.Put("example", "Endpoint", "offline", models.Resource{APIVersion: "example.ks-tool.dev/v1", Spec: json.RawMessage(`{"title":"Offline","address":"192.0.2.2"}`)}); err != nil {
		t.Fatal(err)
	}
	waitRemoteStatus(t, core, "offline", models.ResourceDegraded)
	manifest := core.Manifest()
	if len(manifest.Providers) != 1 || manifest.Providers[0].Name != "example" {
		t.Fatalf("core lost provider after disconnect: %+v", manifest)
	}

	client = startProviderClient(t, socket, definition, externalRuntime)
	defer client.stop(t)
	waitRemoteStatus(t, core, "offline", models.ResourceReady)
	if err := core.Delete("example", "Endpoint", "public"); err != nil {
		t.Fatal(err)
	}
	waitRemoteDelete(t, core, "public")
}

type delayedProvider struct {
	resource.Provider
	release <-chan struct{}
}

func (p delayedProvider) Reconcile(ctx context.Context, item models.Resource) (json.RawMessage, error) {
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return p.Provider.Reconcile(ctx, item)
}

func TestRemoteReconcileAcceptsLateReady(t *testing.T) {
	exampleProvider, err := example.New()
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	core, externalRuntime, registry, socket, definition := startProviderPair(t, delayedProvider{Provider: exampleProvider, release: release})
	defer closeProviderPair(core, externalRuntime, registry)
	client := startProviderClient(t, socket, definition, externalRuntime)
	defer client.stop(t)
	waitManifest(t, core)
	if _, err := core.Put("example", "Endpoint", "slow", models.Resource{APIVersion: "example.ks-tool.dev/v1", Spec: json.RawMessage(`{"title":"Slow","address":"192.0.2.3"}`)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	item, err := core.Get("example", "Endpoint", "slow")
	if err != nil || item.Status.Phase != models.ResourcePending {
		t.Fatalf("slow resource did not remain pending: %+v, %v", item.Status, err)
	}
	close(release)
	waitRemoteStatus(t, core, "slow", models.ResourceReady)
}

func TestLoadProviderRegistry(t *testing.T) {
	definition := models.ProviderDefinition{Name: "example", Version: "1", Resources: []models.ResourceDefinition{{APIVersion: "example/v1", Kind: "Endpoint", SpecSchema: json.RawMessage(`{"type":"object"}`), SchemaHash: "hash"}}}
	config := Config{ProtocolVersion: models.ProtocolVersion, Providers: []models.ProviderRegistration{{Provider: definition}}}
	data, _ := json.Marshal(config)
	path := filepath.Join(t.TempDir(), "providers.conf")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	providers, err := Registrations(path)
	if err != nil || len(providers) != 1 {
		t.Fatalf("Registrations: %+v, %v", providers, err)
	}
	config.ProtocolVersion = "unsupported"
	data, _ = json.Marshal(config)
	_ = os.WriteFile(path, data, 0600)
	if _, err := Registrations(path); err == nil {
		t.Fatal("unsupported protocol version was accepted")
	}
}

func TestLoadProviderRegistryDirectory(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "providers.conf")
	dir := filepath.Join(root, "providers.d")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, registration := range []struct {
		path string
		name string
	}{{legacy, "legacy"}, {filepath.Join(dir, "one.conf"), "one"}, {filepath.Join(dir, "two.conf"), "two"}} {
		definition := models.ProviderDefinition{Name: registration.name, Version: "1"}
		config := Config{ProtocolVersion: models.ProtocolVersion, Providers: []models.ProviderRegistration{{Provider: definition}}}
		data, _ := json.Marshal(config)
		if err := os.WriteFile(registration.path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	providers, failures := RegistrationsDir(dir, legacy)
	if len(failures) != 0 || len(providers) != 3 {
		t.Fatalf("RegistrationsDir returned %d providers, %v", len(providers), failures)
	}
}

func TestRegistryRejectsUnexpectedProvider(t *testing.T) {
	exampleProvider, err := example.New()
	if err != nil {
		t.Fatal(err)
	}
	core, externalRuntime, registry, socket, definition := startProviderPair(t, exampleProvider)
	defer closeProviderPair(core, externalRuntime, registry)
	definition.Name = "unknown"
	client := startProviderClient(t, socket, definition, externalRuntime)
	defer client.stop(t)
	time.Sleep(50 * time.Millisecond)
	if len(core.Manifest().Providers) != 1 || core.Manifest().Providers[0].Name != "example" {
		t.Fatal("unexpected provider was registered")
	}
}

func waitManifest(t *testing.T, runtime *resource.Runtime) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		manifest := runtime.Manifest()
		if len(manifest.Providers) == 1 && len(manifest.Providers[0].Resources) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("provider manifest was not registered: %+v", runtime.Manifest())
}

func waitRemoteStatus(t *testing.T, runtime *resource.Runtime, name string, phase models.ResourcePhase) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		item, err := runtime.Get("example", "Endpoint", name)
		if err == nil && item.Status.Phase == phase {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	item, _ := runtime.Get("example", "Endpoint", name)
	t.Fatalf("resource %s did not reach %s: %+v", name, phase, item.Status)
}

func waitRemoteDelete(t *testing.T, runtime *resource.Runtime, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, err := runtime.Get("example", "Endpoint", name)
		if storage.IsNotFound(err) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("resource %s was not deleted", name)
}

type flakyRemoteActionProvider struct {
	resource.Provider
	mu    sync.Mutex
	calls int
}

func (p *flakyRemoteActionProvider) Action(ctx context.Context, item models.Resource, name string, arguments json.RawMessage) (models.ActionResult, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		<-ctx.Done()
		return models.ActionResult{}, ctx.Err()
	}
	if actionProvider, ok := p.Provider.(resource.ActionProvider); ok {
		return actionProvider.Action(ctx, item, name, arguments)
	}
	return models.ActionResult{}, resource.ErrUnknownAction
}

func TestRemoteTimeoutQuarantinesUntilReconnect(t *testing.T) {
	exampleProvider, err := example.New()
	if err != nil {
		t.Fatal(err)
	}
	provider := &flakyRemoteActionProvider{Provider: exampleProvider}
	core, externalRuntime, registry, socket, definition := startProviderPair(t, provider, resource.WithOperationTimeout(30*time.Millisecond))
	defer closeProviderPair(core, externalRuntime, registry)
	client := startProviderClient(t, socket, definition, externalRuntime)
	defer client.stop(t)
	waitManifest(t, core)
	if _, err := core.Put("example", "Endpoint", "recover", models.Resource{APIVersion: "example.ks-tool.dev/v1", Spec: json.RawMessage(`{"title":"Recover","address":"192.0.2.1"}`)}); err != nil {
		t.Fatal(err)
	}
	waitRemoteStatus(t, core, "recover", models.ResourceReady)
	if _, err := core.Action(context.Background(), "example", "Endpoint", "recover", "inspect", json.RawMessage(`{}`)); err == nil {
		t.Fatal("blocked provider action unexpectedly succeeded")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(core.Manifest().ProviderErrors["example"]) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(core.Manifest().ProviderErrors["example"]) == 0 {
		t.Fatal("timed out provider session was not quarantined")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, err := core.Action(context.Background(), "example", "Endpoint", "recover", "inspect", json.RawMessage(`{}`))
		if err == nil && result.Type == models.ActionResultJSON {
			if len(core.Manifest().ProviderErrors["example"]) > 0 {
				t.Fatalf("provider error survived reconnect: %+v", core.Manifest().ProviderErrors)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("provider action did not recover after reconnect")
}

func TestProviderSessionGenerationRejectsStaleDisconnect(t *testing.T) {
	provider := &Provider{name: "example"}
	first := newProviderSession(nil)
	second := newProviderSession(nil)
	definition := models.ProviderDefinition{Name: "example", Version: "1"}
	if err := provider.connect(definition, first); err != nil {
		t.Fatal(err)
	}
	if err := provider.connect(definition, second); err != nil {
		t.Fatal(err)
	}
	if provider.disconnect(first) {
		t.Fatal("stale session disconnected the replacement")
	}
	if !provider.disconnect(second) {
		t.Fatal("current session was not disconnected")
	}
}

func TestRegistryRejectsProtocolV1(t *testing.T) {
	definition := models.ProviderDefinition{Name: "example", Version: "1"}
	config := Config{ProtocolVersion: "v1", Providers: []models.ProviderRegistration{{Provider: definition}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "providers.conf")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Registrations(path); err == nil {
		t.Fatal("provider protocol v1 was accepted")
	}
}

type blockingSendStream struct {
	providerv1.ProviderChannel_ConnectServer
	release chan struct{}
}

func (s *blockingSendStream) Send(*providerv1.AgentMessage) error {
	<-s.release
	return nil
}

func TestProviderSessionSendHonorsContext(t *testing.T) {
	stream := &blockingSendStream{release: make(chan struct{})}
	session := newProviderSession(stream)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	time.AfterFunc(100*time.Millisecond, func() { close(stream.release) })
	started := time.Now()
	_, err := session.request(ctx, &providerv1.AgentMessage{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("send error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 80*time.Millisecond {
		t.Fatalf("send ignored context: %v", elapsed)
	}
}
