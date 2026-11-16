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

	"github.com/ks-tool/vpn-agent/v2/internal/metrics"
	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/ks-tool/vpn-agent/v2/mux"
)

type metricsStateRequest struct {
	Enabled bool `json:"enabled"`
}

type agentHandler struct {
	system *metrics.Collector
}

func NewAgent(hostInfo models.HostInfo, runtime *resource.Runtime, system *metrics.Collector, mws ...mux.Middleware) (http.Handler, error) {
	h := &agentHandler{system: system}
	router := mux.NewServeMux()
	router.Handle("/v1/", NewResourceHandler(runtime, mws...))
	router.GET("/info", func(w http.ResponseWriter, _ *http.Request) { encode(w, hostInfo) })
	router.GET("/metrics", h.systemMetrics)
	router.GET("/metrics/history", h.systemMetricsHistory)
	router.PATCH("/metrics", h.setMetricsState)
	router.HandleFunc("/", http.NotFound)
	return router, nil
}

func (h *agentHandler) systemMetrics(w http.ResponseWriter, r *http.Request) {
	if h.system == nil {
		http.Error(w, "metrics collection is disabled", http.StatusServiceUnavailable)
		return
	}
	snapshot := h.system.Snapshot()
	if r.URL.Query().Get("fmt") == "prom" {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = metrics.WritePrometheus(w, snapshot)
		return
	}
	encode(w, snapshot)
}

func (h *agentHandler) systemMetricsHistory(w http.ResponseWriter, _ *http.Request) {
	if h.system == nil {
		http.Error(w, "metrics collection is disabled", http.StatusServiceUnavailable)
		return
	}
	encode(w, h.system.SystemHistory())
}

func (h *agentHandler) setMetricsState(w http.ResponseWriter, r *http.Request) {
	var state metricsStateRequest
	if err := decode(r, &state); err != nil {
		badRequest(w, err)
		return
	}
	if h.system != nil {
		h.system.SetEnabled(state.Enabled)
	}
	encode(w, state)
}
