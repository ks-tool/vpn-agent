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

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ks-tool/vpn-agent/v2/internal/metrics"
	"github.com/ks-tool/vpn-agent/v2/models"
)

func TestAgentServesOnlySystemMetrics(t *testing.T) {
	handler, err := NewAgent(models.HostInfo{}, nil, metrics.NewSystemCollector(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	response := request(t, http.MethodGet, server.URL+"/metrics", nil)
	var system models.SystemSnapshot
	decodeResponse(t, response, &system)

	response = request(t, http.MethodGet, server.URL+"/metrics/awg0", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy provider metrics status = %s, want 404", response.Status)
	}
}
