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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ks-tool/vpn-agent/v2/models"
)

func TestRegistrationsDirSkipsInvalidProvider(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "providers.d")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"protocolVersion":"v1","providers":[{"address":"http://127.0.0.1:9091","discover":true,"command":"/usr/local/bin/vpn-provider-awg","provider":{"name":"awg"}}]}`)
	if err := os.WriteFile(filepath.Join(dir, "awg.conf"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	valid := Config{ProtocolVersion: models.ProtocolVersion, Providers: []models.ProviderRegistration{{Provider: models.ProviderDefinition{Name: "xray", Version: "1"}}}}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "xray.conf"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	registrations, failures := RegistrationsDir(dir, filepath.Join(root, "providers.conf"))
	if len(registrations) != 1 || registrations[0].Provider.Name != "xray" {
		t.Fatalf("valid provider was rejected with invalid registration: %+v", registrations)
	}
	if failures["awg"] == nil {
		t.Fatalf("invalid AWG registration was not reported: %+v", failures)
	}
}
