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

package fs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	awgmodels "github.com/ks-tool/vpn-agent/v2/providers/awg/models"
	"github.com/ks-tool/vpn-agent/v2/storage"

	"github.com/natefinch/atomic"
)

const ext = ".json"

type Dir struct {
	root string
}

func New(root string) (*Dir, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	return &Dir{root: root}, nil
}

func (dir *Dir) Close() error {
	return nil
}

func (dir *Dir) List() ([]awgmodels.InterfaceConfig, error) {
	entries, err := os.ReadDir(dir.root)
	if err != nil {
		return nil, err
	}
	list := make([]awgmodels.InterfaceConfig, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ext {
			continue
		}
		cfg, err := dir.get(filepath.Join(dir.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		list = append(list, *cfg)
	}
	return list, nil
}

func (dir *Dir) Set(cfg *awgmodels.InterfaceConfig) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}

	return atomic.WriteFile(filepath.Join(dir.root, cfg.Interface)+ext, &buf)
}

func (dir *Dir) Get(iface string) (*awgmodels.InterfaceConfig, error) {
	filename := filepath.Join(dir.root, iface) + ext
	return dir.get(filename)
}

func (dir *Dir) get(filename string) (*awgmodels.InterfaceConfig, error) {
	fi, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	defer func() { _ = fi.Close() }()

	var cfg awgmodels.InterfaceConfig
	if err = json.NewDecoder(fi).Decode(&cfg); err != nil {
		return nil, err
	}
	if len(cfg.Interface) == 0 {
		return nil, fmt.Errorf("%s: interface name is empty", filename)
	}
	return &cfg, nil
}

func (dir *Dir) Delete(iface string) error {
	return os.Remove(filepath.Join(dir.root, iface) + ext)
}
