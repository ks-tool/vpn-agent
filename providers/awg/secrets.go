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

package awg

import (
	"sync"

	awgmodels "github.com/ks-tool/vpn-agent/v2/providers/awg/models"

	"github.com/ks-tool/vpn-agent/v2/storage"
)

type secretRecord struct {
	Interface         string         `json:"interface"`
	PrivateKey        awgmodels.Key  `json:"privateKey"`
	PresharedKey      *awgmodels.Key `json:"presharedKey,omitempty"`
	PreviousPublicKey string         `json:"previousPublicKey,omitempty"`
}

type secretStore struct {
	mu      sync.RWMutex
	records map[string]secretRecord
}

func newSecretStore() *secretStore {
	return &secretStore{records: make(map[string]secretRecord)}
}

func (s *secretStore) get(resourceID string) (secretRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[resourceID]
	if !ok {
		return secretRecord{}, storage.ErrNotFound
	}
	return record, nil
}

func (s *secretStore) put(resourceID string, record secretRecord) error {
	s.mu.Lock()
	s.records[resourceID] = record
	s.mu.Unlock()
	return nil
}

func (s *secretStore) delete(resourceID string) error {
	s.mu.Lock()
	delete(s.records, resourceID)
	s.mu.Unlock()
	return nil
}
