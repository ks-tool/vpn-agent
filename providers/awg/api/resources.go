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

	awgstorage "github.com/ks-tool/vpn-agent/v2/providers/awg/storage"

	agentapi "github.com/ks-tool/vpn-agent/v2/internal/api"
	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/ks-tool/vpn-agent/v2/mux"
	"github.com/ks-tool/vpn-agent/v2/providers/awg/metrics"
)

func NewWithResources(hostInfo models.HostInfo, store awgstorage.Storage, collector *metrics.Collector, latency *metrics.LatencyCollector, runtime *resource.Runtime, mws ...mux.Middleware) http.Handler {
	router := mux.NewServeMux()
	router.Handle("/v1/", agentapi.NewResourceHandler(runtime, mws...))
	router.Handle("/", New(hostInfo, store, collector, latency, mws...))
	return router
}
