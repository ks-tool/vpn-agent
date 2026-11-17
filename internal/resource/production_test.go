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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ks-tool/vpn-agent/v2/models"
)

func TestPersistentStoreRestoresResource(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resources")
	store, err := NewPersistentStore(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("resource directory permissions = %o", info.Mode().Perm())
	}
	expected := StoredResource{Provider: "test", Resource: models.Resource{ID: "stable-id", APIVersion: "test/v2", Kind: "Service", Name: "public", Generation: 7, Spec: json.RawMessage(`{"enabled":true}`)}}
	if err := store.Put(expected); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPersistentStore(root)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := reopened.Get("test", "Service", "public")
	if err != nil {
		t.Fatal(err)
	}
	if actual.Resource.ID != expected.Resource.ID || actual.Resource.Generation != 7 || string(actual.Resource.Spec) != string(expected.Resource.Spec) {
		t.Fatalf("restored resource = %+v", actual.Resource)
	}
}

func TestPersistentStoreRejectsCorruptResource(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resources")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "corrupt.resource"), []byte(`{"broken"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPersistentStore(root); err == nil {
		t.Fatal("corrupt persistent state was accepted")
	}
}
