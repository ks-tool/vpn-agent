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
	"sync"

	"github.com/ks-tool/vpn-agent/v2/storage"
)

type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]StoredResource
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]StoredResource)}
}

func memoryKey(provider, kind, name string) string {
	return provider + "\x00" + kind + "\x00" + name
}

func (s *MemoryStore) List() ([]StoredResource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]StoredResource, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	return items, nil
}

func (s *MemoryStore) Get(provider, kind, name string) (StoredResource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[memoryKey(provider, kind, name)]
	if !ok {
		return StoredResource{}, storage.ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) Put(item StoredResource) error {
	s.mu.Lock()
	s.items[memoryKey(item.Provider, item.Resource.Kind, item.Resource.Name)] = item
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Delete(provider, kind, name string) error {
	key := memoryKey(provider, kind, name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[key]; !ok {
		return storage.ErrNotFound
	}
	delete(s.items, key)
	return nil
}
