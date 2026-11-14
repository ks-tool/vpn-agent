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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/ks-tool/vpn-agent/v2/storage"

	"github.com/natefinch/atomic"
)

type StoredResource struct {
	Provider string          `json:"provider"`
	Deleted  bool            `json:"deleted,omitempty"`
	Resource models.Resource `json:"resource"`
}

type Store interface {
	List() ([]StoredResource, error)
	Get(provider, kind, name string) (StoredResource, error)
	Put(StoredResource) error
	Delete(provider, kind, name string) error
}

type FileStore struct {
	root string
	mu   sync.RWMutex
}

func NewFileStore(root string) (*FileStore, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return &FileStore{root: root}, nil
}

func (s *FileStore) List() ([]StoredResource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	result := make([]StoredResource, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".resource" {
			continue
		}
		item, err := s.read(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *FileStore) Get(provider, kind, name string) (StoredResource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.read(s.filename(provider, kind, name))
}

func (s *FileStore) Put(item StoredResource) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return atomic.WriteFile(s.filename(item.Provider, item.Resource.Kind, item.Resource.Name), bytes.NewReader(data))
}

func (s *FileStore) Delete(provider, kind, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.filename(provider, kind, name)); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound
		}
		return err
	}
	return nil
}

func (s *FileStore) read(path string) (StoredResource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StoredResource{}, storage.ErrNotFound
		}
		return StoredResource{}, err
	}
	var item StoredResource
	if err := json.Unmarshal(data, &item); err != nil {
		return StoredResource{}, fmt.Errorf("decode resource %s: %w", filepath.Base(path), err)
	}
	return item, nil
}

func (s *FileStore) filename(provider, kind, name string) string {
	hash := sha256Sum(provider + "\x00" + kind + "\x00" + name)
	return filepath.Join(s.root, hash+".resource")
}
