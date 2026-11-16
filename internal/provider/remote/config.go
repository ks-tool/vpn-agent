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
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/ks-tool/vpn-agent/v2/models"
)

type Config struct {
	ProtocolVersion string                        `json:"protocolVersion"`
	Providers       []models.ProviderRegistration `json:"providers"`
}

func RegistrationsDir(dir, legacyFile string) ([]models.ProviderRegistration, error) {
	registrations, err := Registrations(legacyFile)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return registrations, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if entry.IsDir() || (extension != ".json" && extension != ".conf") {
			continue
		}
		registered, err := Registrations(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, registered...)
	}
	return registrations, nil
}

func Registrations(path string) ([]models.ProviderRegistration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	if config.ProtocolVersion != models.ProtocolVersion {
		return nil, errors.New("unsupported provider registry protocol version")
	}
	return config.Providers, nil
}
