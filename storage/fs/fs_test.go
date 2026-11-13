package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListIgnoresNestedJSONFiles(t *testing.T) {
	root := t.TempDir()
	dir, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "awg0.json"), []byte(`{"iface":"awg0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "pack", "cache", "packs", "dir-hash", "vpn-provider-awg")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "schema.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configs, err := dir.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].Interface != "awg0" {
		t.Fatalf("configs = %+v", configs)
	}
}

func TestGetRejectsEmptyInterfaceName(t *testing.T) {
	dir, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir.root, "broken.json")
	if err := os.WriteFile(path, []byte(`{"addr":"10.0.0.1/24"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = dir.Get("broken")
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "interface name is empty") {
		t.Fatalf("empty interface name was accepted: %v", err)
	}
}
