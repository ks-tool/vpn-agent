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
	"os"
	"path/filepath"
	"testing"

	packcmd "github.com/horchestra/pack/cmd"
	"github.com/horchestra/pack/pkg/plan"
	"github.com/horchestra/pack/sdk"
	"github.com/horchestra/pack/sdk/packschema"
	"github.com/ks-tool/vpn-agent/v2/models"
)

func TestApplyLocalPackUsesStatelessInProcessTarget(t *testing.T) {
	original := embeddedLocalPackApply
	t.Cleanup(func() { embeddedLocalPackApply = original })
	component, values, err := New(models.ProviderDeployment{Name: "awg"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	embeddedLocalPackApply = func(_ context.Context, options packcmd.ApplyOptions) error {
		calls++
		if options.Refresh {
			t.Fatal("local apply tried to download a target")
		}
		if options.Pack != component || options.Values["registrationPath"] != values["registrationPath"] {
			t.Fatal("pack and values were not passed in memory")
		}
		if options.Planned == nil {
			t.Fatal("local apply does not report its plan")
		}
		var release map[string]any
		readDeployJSON(t, filepath.Join(options.Dir, "release.yaml"), &release)
		if release["state"] != "observe" {
			t.Fatalf("state = %#v", release["state"])
		}
		if _, exists := release["pack"]; exists {
			t.Fatalf("embedded pack was written to release.yaml: %#v", release)
		}
		var env map[string]any
		readDeployJSON(t, filepath.Join(options.Dir, "env.yaml"), &env)
		if env["target"] != "host" {
			t.Fatalf("target = %#v", env["target"])
		}
		if _, exists := env["hosts"]; exists {
			t.Fatalf("local environment declares SSH hosts: %#v", env["hosts"])
		}
		return nil
	}

	dir := t.TempDir()
	for range 2 {
		if err := applyLocalPack(context.Background(), component, values, dir); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("apply calls = %d", calls)
	}
	for _, name := range []string{"packs", "values.yaml", "cache", filepath.Join(".pack", "state.json")} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("embedded apply wrote %s: %v", name, err)
		}
	}
}

type localApplyTestPack struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

func (*localApplyTestPack) SetDefaults() {}

func (*localApplyTestPack) Meta() sdk.Meta {
	return sdk.Meta{Name: "local-apply-test", Version: "1.0.0"}
}

func (p *localApplyTestPack) PackRoot() packschema.Root {
	install := packschema.OSInstall{Content: []packschema.File{{Path: p.Path, Values: "text", Format: "text", Mode: "0600"}}}
	return packschema.Root{Packages: map[string]packschema.OSInstall{
		"": install, "debian": install, "fedora": install, "rhel": install, "darwin": install,
	}}
}

func TestApplyLocalPackSkipsUnchangedInstallation(t *testing.T) {
	original := embeddedLocalPackApply
	t.Cleanup(func() { embeddedLocalPackApply = original })
	var plans []*plan.Plan
	embeddedLocalPackApply = func(ctx context.Context, options packcmd.ApplyOptions) error {
		planned := options.Planned
		options.Planned = func(p *plan.Plan) {
			plans = append(plans, p)
			planned(p)
		}
		return original(ctx, options)
	}

	path := filepath.Join(t.TempDir(), "result")
	component := &localApplyTestPack{Path: path, Text: "configured"}
	values := map[string]any{"path": path, "text": "configured"}
	dir := t.TempDir()
	for range 2 {
		if err := applyLocalPack(context.Background(), component, values, dir); err != nil {
			t.Fatal(err)
		}
	}
	if len(plans) != 2 {
		t.Fatalf("plans = %d", len(plans))
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "configured\n" {
		t.Fatalf("delivered content = %q, %v", content, err)
	}
	if len(plans[1].Changes) == 0 {
		t.Fatal("second plan has no installation status")
	}
	for _, change := range plans[1].Changes {
		if change.Kind != "none" {
			t.Fatalf("unchanged installation was applied again: %+v", plans[1].Changes)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := applyLocalPack(context.Background(), component, values, dir); err != nil {
		t.Fatal(err)
	}
	if len(plans) != 3 {
		t.Fatalf("plans = %d", len(plans))
	}
	content, err = os.ReadFile(path)
	if err != nil || string(content) != "configured\n" {
		t.Fatalf("missing file was not restored: %q, %v", content, err)
	}
}

func readDeployJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
