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

package proxy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ks-tool/vpn-agent/models"
)

func TestDeployCommand(t *testing.T) {
	var called string
	handled, err := DeployCommand([]string{"deploy", "--config", "/tmp/provider.json"}, func(_ context.Context, path string) error {
		called = path
		return nil
	})
	if err != nil || !handled || called != "/tmp/provider.json" {
		t.Fatalf("deploy command: handled=%v called=%q err=%v", handled, called, err)
	}
}

func TestWriteRegistrationMarksDeployment(t *testing.T) {
	agentDB := t.TempDir()
	if err := WriteRegistration("awg", agentDB, Deployment{Port: 9091, EnvPrefix: "VPN_PROVIDER_AWG", StatePath: filepath.Join(agentDB, "state")}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(agentDB, "providers.d", "awg.conf"))
	if err != nil {
		t.Fatal(err)
	}
	var registration struct {
		Providers []models.ProviderRegistration `json:"providers"`
	}
	if err := json.Unmarshal(data, &registration); err != nil {
		t.Fatal(err)
	}
	if len(registration.Providers) != 1 || len(registration.Providers[0].Env["VPN_PROVIDER_DEPLOYED_AT"]) == 0 {
		t.Fatalf("deployment marker is missing: %+v", registration.Providers)
	}
}
