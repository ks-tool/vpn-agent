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

package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/ks-tool/vpn-agent/v2/storage"
)

type testProvider struct {
	mu            sync.Mutex
	reconcile     int
	deletes       int
	failReconcile int
	failDelete    int
	last          models.Resource
}

func (p *testProvider) Name() string    { return "test" }
func (p *testProvider) Version() string { return "1.0.0" }
func (p *testProvider) Definitions() []models.ResourceDefinition {
	return []models.ResourceDefinition{{
		APIVersion: "test/v1", Kind: "Service", SpecSchema: json.RawMessage(`{"type":"object"}`), SchemaHash: "hash",
		Actions: []models.ActionDefinition{{Name: "inspect", Title: "Inspect", ResultType: models.ActionResultJSON}},
	}}
}
func (p *testProvider) Validate(context.Context, models.Resource) error { return nil }
func (p *testProvider) Reconcile(_ context.Context, resource models.Resource) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reconcile++
	p.last = resource
	if p.reconcile <= p.failReconcile {
		return nil, errors.New("temporary apply error")
	}
	return json.RawMessage(`{"endpoint":"192.0.2.1"}`), nil
}
func (p *testProvider) Delete(_ context.Context, resource models.Resource) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deletes++
	p.last = resource
	if p.deletes <= p.failDelete {
		return errors.New("temporary delete error")
	}
	return nil
}
func (p *testProvider) Action(_ context.Context, _ models.Resource, name string, arguments json.RawMessage) (models.ActionResult, error) {
	if name != "inspect" {
		return models.ActionResult{}, ErrUnknownAction
	}
	return models.ActionResult{Type: models.ActionResultJSON, Data: arguments}, nil
}
func (p *testProvider) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reconcile, p.deletes
}

func TestRuntimeLifecycleRestartAndResync(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &testProvider{failReconcile: 1, failDelete: 1}
	runtime := NewRuntime(store, time.Millisecond, time.Hour)
	if err := runtime.Register(provider); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())

	desired := models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{"enabled":true}`)}
	created, err := runtime.Put("test", "Service", "public", desired)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Generation != 1 {
		t.Fatalf("unexpected created resource: %+v", created)
	}
	waitForStatus(t, runtime, 1, models.ResourceReady)
	reconciles, _ := provider.counts()
	if reconciles != 2 {
		t.Fatalf("expected failed apply and retry, got %d applies", reconciles)
	}

	desired.ID = created.ID
	desired.Generation = created.Generation
	repeated, err := runtime.Put("test", "Service", "public", desired)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Generation != 1 {
		t.Fatalf("repeated revision changed generation: %d", repeated.Generation)
	}
	time.Sleep(10 * time.Millisecond)
	reconciles, _ = provider.counts()
	if reconciles != 2 {
		t.Fatalf("repeated revision triggered apply: %d", reconciles)
	}

	desired.Spec = json.RawMessage(`{"enabled":false}`)
	updated, err := runtime.Put("test", "Service", "public", desired)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", updated.Generation)
	}
	waitForStatus(t, runtime, 2, models.ResourceReady)

	result, err := runtime.Action(context.Background(), "test", "Service", "public", "inspect", json.RawMessage(`{"verbose":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != `{"verbose":true}` {
		t.Fatalf("unexpected action result: %s", result.Data)
	}

	before, _ := provider.counts()
	runtime.Resync()
	waitForApplies(t, provider, before+1)
	runtime.Close()

	restartedProvider := &testProvider{failDelete: 1}
	restarted := NewRuntime(store, time.Millisecond, time.Hour)
	if err := restarted.Register(restartedProvider); err != nil {
		t.Fatal(err)
	}
	restarted.Start(context.Background())
	defer restarted.Close()
	waitForApplies(t, restartedProvider, 1)
	waitForStatus(t, restarted, 2, models.ResourceReady)

	if err := restarted.Delete("test", "Service", "public"); err != nil {
		t.Fatal(err)
	}
	waitForDelete(t, restartedProvider, 2)
	if _, err := restarted.Get("test", "Service", "public"); !storage.IsNotFound(err) {
		t.Fatalf("deleted resource is still available: %v", err)
	}
	if err := restarted.Delete("test", "Service", "public"); err != nil {
		t.Fatalf("repeated delete failed: %v", err)
	}
}

func TestRuntimeRetainsDesiredStateAndErrorStatus(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &testProvider{failReconcile: 100}
	runtime := NewRuntime(store, time.Hour, time.Hour)
	if err := runtime.Register(provider); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()
	created, err := runtime.Put("test", "Service", "broken", models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{"value":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, runtime, 1, models.ResourceDegraded)
	current, err := runtime.Get("test", "Service", "broken")
	if err != nil {
		t.Fatalf("desired state was removed after failure: %v", err)
	}
	if current.ID != created.ID || string(current.Spec) != `{"value":1}` {
		t.Fatalf("desired state changed: %+v", current)
	}
	condition := current.Status.Conditions[0]
	if condition.Reason != "ApplyFailed" || condition.Message != "temporary apply error" || condition.ObservedGeneration != 1 {
		t.Fatalf("unexpected failure condition: %+v", condition)
	}
}

func TestRuntimeNormalizesStoredAPIVersionOnResync(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item := StoredResource{Provider: "test", Resource: models.Resource{
		ID: "legacy", APIVersion: "test/v1alpha1", Kind: "Service", Name: "public", Generation: 1,
		Spec: json.RawMessage(`{"enabled":true}`), Status: models.ResourceStatus{Phase: models.ResourcePending},
	}}
	if err := store.Put(item); err != nil {
		t.Fatal(err)
	}
	provider := &testProvider{}
	runtime := NewRuntime(store, time.Millisecond, time.Hour)
	if err := runtime.Register(provider); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()
	waitForStatus(t, runtime, 1, models.ResourceReady)

	provider.mu.Lock()
	reconciled := provider.last
	provider.mu.Unlock()
	if reconciled.APIVersion != "test/v1" {
		t.Fatalf("provider received legacy apiVersion %q", reconciled.APIVersion)
	}
	stored, err := store.Get("test", "Service", "public")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Resource.APIVersion != "test/v1" {
		t.Fatalf("stored apiVersion was not normalized: %q", stored.Resource.APIVersion)
	}
}

func waitForStatus(t *testing.T, runtime *Runtime, generation int64, phase models.ResourcePhase) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		items, err := runtime.List("test")
		if err == nil && len(items) == 1 && items[0].Status.ObservedGeneration == generation && items[0].Status.Phase == phase {
			return
		}
		time.Sleep(time.Millisecond)
	}
	items, _ := runtime.List("test")
	t.Fatalf("status did not reach %s at generation %d: %+v", phase, generation, items)
}

func waitForApplies(t *testing.T, provider *testProvider, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		applies, _ := provider.counts()
		if applies >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("provider did not reach %d applies", count)
}

func waitForDelete(t *testing.T, provider *testProvider, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, deletes := provider.counts()
		if deletes >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("provider did not reach %d deletes", count)
}

func TestRuntimeFailedDeleteRemainsVisibleAndCanBeRetried(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &testProvider{failDelete: 1}
	runtime := NewRuntime(store, time.Hour, time.Hour)
	if err := runtime.Register(provider); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()
	if _, err := runtime.Put("test", "Service", "public", models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, runtime, 1, models.ResourceReady)
	if err := runtime.Delete("test", "Service", "public"); err != nil {
		t.Fatal(err)
	}
	waitForDelete(t, provider, 1)
	waitForStatus(t, runtime, 2, models.ResourceDegraded)
	items, err := runtime.List("test")
	if err != nil || len(items) != 1 || items[0].Status.Phase != models.ResourceDegraded {
		t.Fatalf("failed deletion disappeared from inventory: %+v, %v", items, err)
	}
	if err := runtime.Delete("test", "Service", "public"); err != nil {
		t.Fatal(err)
	}
	waitForDelete(t, provider, 2)
	items, err = runtime.List("test")
	if err != nil || len(items) != 0 {
		t.Fatalf("resource survived successful deletion: %+v, %v", items, err)
	}
}

func TestRuntimeRegistersAndUnregistersProviderAfterStart(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(store, time.Hour, time.Hour)
	runtime.Start(context.Background())
	defer runtime.Close()
	if err := runtime.Register(&testProvider{}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Schema("test"); err != nil {
		t.Fatal(err)
	}
	runtime.Unregister("test")
	if _, err := runtime.Schema("test"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("provider remained registered: %v", err)
	}
	if err := runtime.Register(&testProvider{}); err != nil {
		t.Fatal(err)
	}
}

type namedTestProvider struct {
	*testProvider
	name string
}

func (p namedTestProvider) Name() string { return p.name }

type blockingProvider struct {
	name    string
	started chan struct{}
	once    sync.Once
}

func (p *blockingProvider) Name() string    { return p.name }
func (p *blockingProvider) Version() string { return "1" }
func (p *blockingProvider) Definitions() []models.ResourceDefinition {
	return []models.ResourceDefinition{{APIVersion: "test/v1", Kind: "Service"}}
}
func (p *blockingProvider) Validate(context.Context, models.Resource) error { return nil }
func (p *blockingProvider) Reconcile(ctx context.Context, _ models.Resource) (json.RawMessage, error) {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p *blockingProvider) Delete(ctx context.Context, _ models.Resource) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRuntimeHungProviderDoesNotBlockOtherProvider(t *testing.T) {
	blocked := &blockingProvider{name: "blocked", started: make(chan struct{})}
	healthy := namedTestProvider{testProvider: &testProvider{}, name: "healthy"}
	runtime := NewRuntime(NewMemoryStore(), time.Hour, time.Hour, WithOperationTimeout(20*time.Millisecond))
	if err := runtime.Register(blocked); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register(healthy); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()
	if _, err := runtime.Put("blocked", "Service", "stuck", models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Put("healthy", "Service", "ready", models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		item, err := runtime.Get("healthy", "Service", "ready")
		if err == nil && item.Status.Phase == models.ResourceReady {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("healthy provider was blocked by another provider")
}

type concurrentProvider struct {
	*testProvider
	active int
	peak   int
}

func (p *concurrentProvider) Reconcile(ctx context.Context, item models.Resource) (json.RawMessage, error) {
	p.mu.Lock()
	p.active++
	if p.active > p.peak {
		p.peak = p.active
	}
	p.mu.Unlock()
	select {
	case <-time.After(10 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return p.testProvider.Reconcile(ctx, item)
}

func TestRuntimeSameResourceOperationsAreSerialized(t *testing.T) {
	provider := &concurrentProvider{testProvider: &testProvider{}}
	runtime := NewRuntime(NewMemoryStore(), time.Millisecond, time.Hour)
	if err := runtime.Register(provider); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()
	created, err := runtime.Put("test", "Service", "public", models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{"revision":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	for revision := 2; revision <= 4; revision++ {
		created.Spec = json.RawMessage(fmt.Sprintf(`{"revision":%d}`, revision))
		updated, err := runtime.Put("test", "Service", "public", created)
		if err != nil {
			t.Fatal(err)
		}
		created = updated
	}
	waitForStatus(t, runtime, 4, models.ResourceReady)
	provider.mu.Lock()
	peak := provider.peak
	provider.mu.Unlock()
	if peak != 1 {
		t.Fatalf("same resource reconcile concurrency = %d", peak)
	}
}

type deadlineProvider struct {
	*testProvider
	calls int
}

func (p *deadlineProvider) Reconcile(ctx context.Context, item models.Resource) (json.RawMessage, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.testProvider.Reconcile(ctx, item)
}

func TestRuntimeOperationDeadlineRetries(t *testing.T) {
	provider := &deadlineProvider{testProvider: &testProvider{}}
	runtime := NewRuntime(NewMemoryStore(), time.Millisecond, time.Hour, WithOperationTimeout(20*time.Millisecond))
	if err := runtime.Register(provider); err != nil {
		t.Fatal(err)
	}
	runtime.Start(context.Background())
	defer runtime.Close()
	if _, err := runtime.Put("test", "Service", "public", models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, runtime, 1, models.ResourceReady)
}

type timeoutBridgeProvider struct {
	*testProvider
}

func (p *timeoutBridgeProvider) Bridge(ctx context.Context, _, _ string, _ []byte, _ map[string]string) (*BridgeResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRuntimeBridgeHonorsOperationTimeout(t *testing.T) {
	provider := &timeoutBridgeProvider{testProvider: &testProvider{}}
	runtime := NewRuntime(NewMemoryStore(), time.Millisecond, time.Hour, WithOperationTimeout(20*time.Millisecond))
	if err := runtime.Register(provider); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, err := runtime.Bridge(ctx, "test", "GET", "/metrics", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bridge error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("runtime did not apply operation timeout: %v", elapsed)
	}
}

type declaredTimeoutActionProvider struct {
	*testProvider
}

func (p *declaredTimeoutActionProvider) Definitions() []models.ResourceDefinition {
	return []models.ResourceDefinition{{
		APIVersion: "test/v1", Kind: "Service",
		Actions: []models.ActionDefinition{{Name: "wait", Title: "Wait", ResultType: models.ActionResultJSON, TimeoutSeconds: 1}},
	}}
}

func (p *declaredTimeoutActionProvider) Action(ctx context.Context, _ models.Resource, _ string, _ json.RawMessage) (models.ActionResult, error) {
	<-ctx.Done()
	return models.ActionResult{}, ctx.Err()
}

func TestRuntimeActionUsesDeclaredTimeout(t *testing.T) {
	provider := &declaredTimeoutActionProvider{testProvider: &testProvider{}}
	runtime := NewRuntime(NewMemoryStore(), time.Millisecond, time.Hour, WithOperationTimeout(20*time.Millisecond))
	if err := runtime.Register(provider); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Put("test", "Service", "public", models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runtime.Action(ctx, "test", "Service", "public", "wait", json.RawMessage(`{}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("action error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 1300*time.Millisecond {
		t.Fatalf("action ignored declared timeout: %v", elapsed)
	}
}

func TestRuntimePutRetryAndConflictMatrix(t *testing.T) {
	create := func(t *testing.T) (*Runtime, *testProvider, models.Resource) {
		t.Helper()
		provider := &testProvider{}
		runtime := NewRuntime(NewMemoryStore(), time.Millisecond, time.Hour)
		if err := runtime.Register(provider); err != nil {
			t.Fatal(err)
		}
		runtime.Start(context.Background())
		t.Cleanup(runtime.Close)
		created, err := runtime.Put("test", "Service", "public", models.Resource{APIVersion: "test/v1", Spec: json.RawMessage(`{"revision":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		waitForStatus(t, runtime, 1, models.ResourceReady)
		return runtime, provider, created
	}
	t.Run("exact retry after response loss", func(t *testing.T) {
		runtime, provider, created := create(t)
		updated := created
		updated.Spec = json.RawMessage(`{"revision":2}`)
		accepted, err := runtime.Put("test", "Service", "public", updated)
		if err != nil {
			t.Fatal(err)
		}
		waitForStatus(t, runtime, 2, models.ResourceReady)
		repeated, err := runtime.Put("test", "Service", "public", updated)
		if err != nil {
			t.Fatal(err)
		}
		if accepted.Generation != 2 || repeated.Generation != 2 {
			t.Fatalf("retry generation = %d", repeated.Generation)
		}
		time.Sleep(10 * time.Millisecond)
		if reconciles, _ := provider.counts(); reconciles != 2 {
			t.Fatalf("retry triggered reconcile %d", reconciles)
		}
	})
	t.Run("same spec with stale generation", func(t *testing.T) {
		runtime, provider, created := create(t)
		created.Generation = 0
		repeated, err := runtime.Put("test", "Service", "public", created)
		if err != nil || repeated.Generation != 1 {
			t.Fatalf("idempotent retry = %+v, %v", repeated, err)
		}
		if reconciles, _ := provider.counts(); reconciles != 1 {
			t.Fatalf("idempotent retry triggered reconcile %d", reconciles)
		}
	})
	t.Run("different spec with stale generation", func(t *testing.T) {
		runtime, _, created := create(t)
		created.Generation = 0
		created.Spec = json.RawMessage(`{"revision":2}`)
		if _, err := runtime.Put("test", "Service", "public", created); !errors.Is(err, ErrGenerationConflict) {
			t.Fatalf("stale update error = %v", err)
		}
	})
	t.Run("different spec with matching generation", func(t *testing.T) {
		runtime, _, created := create(t)
		created.Spec = json.RawMessage(`{"revision":2}`)
		updated, err := runtime.Put("test", "Service", "public", created)
		if err != nil || updated.Generation != 2 {
			t.Fatalf("current update = %+v, %v", updated, err)
		}
	})
	t.Run("conflicting immutable ID", func(t *testing.T) {
		runtime, _, created := create(t)
		created.ID = "different-id"
		if _, err := runtime.Put("test", "Service", "public", created); err == nil {
			t.Fatal("immutable resource ID was changed")
		}
	})
}
