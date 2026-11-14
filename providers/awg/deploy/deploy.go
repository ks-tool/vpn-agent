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

package providerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"uuid"

	packcmd "github.com/horchestra/pack/cmd"
	"github.com/horchestra/pack/pkg/plan"
	"github.com/horchestra/pack/sdk"
	"github.com/horchestra/targets/host/local"
	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/ks-tool/vpn-agent/v2/providers/proxy"
)

const deployStateDir = "/var/lib/vpn-provider-awg/pack"

var embeddedLocalPackApply = packcmd.Apply

func Deploy(ctx context.Context, configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var config proxy.DeployConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	if len(config.AgentDB) == 0 {
		return errors.New("agentDB is required")
	}
	pack, values, err := New(models.ProviderDeployment{Name: "awg", Options: config.Options}, config.AgentDB)
	if err != nil {
		return err
	}
	return applyLocalPack(ctx, pack, values, deployStateDir)
}

func applyLocalPack(ctx context.Context, component sdk.Pack, values map[string]any, dir string) error {
	local.Register()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeDeployJSON(filepath.Join(dir, "release.yaml"), map[string]any{
		"state": "observe",
		"id":    uuid.New().String(),
	}); err != nil {
		return err
	}
	if err := writeDeployJSON(filepath.Join(dir, "env.yaml"), map[string]any{
		"target":        "host",
		"history_count": 3,
		"keys": map[string]any{
			component.Meta().Name: map[string]any{"packages": []string{}},
		},
	}); err != nil {
		return err
	}
	return embeddedLocalPackApply(ctx, packcmd.ApplyOptions{
		Dir: dir, Refresh: false, Pack: component, Values: values,
		Env: map[string]string{"PACK_JOURNAL": "off"},
		Planned: func(p *plan.Plan) {
			fmt.Printf("pack plan:\n%s\n", p.Text())
		},
	})
}

func writeDeployJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
