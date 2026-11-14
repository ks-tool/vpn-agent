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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/ks-tool/vpn-agent/v2/internal/metrics"
	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/ks-tool/vpn-agent/v2/mux"
)

type metricsStateRequest struct {
	Enabled bool `json:"enabled"`
}

type agentHandler struct {
	system      *metrics.Collector
	proxy       http.Handler
	providerURL string
	client      *http.Client
}

func NewAgent(hostInfo models.HostInfo, runtime *resource.Runtime, legacyProxy string, system *metrics.Collector, mws ...mux.Middleware) (http.Handler, error) {
	h := &agentHandler{system: system, providerURL: legacyProxy, client: &http.Client{}}
	router := mux.NewServeMux()
	router.Handle("/v1/", NewResourceHandler(runtime, mws...))
	router.GET("/info", func(w http.ResponseWriter, _ *http.Request) { encode(w, hostInfo) })
	router.GET("/metrics", h.systemMetrics)
	router.GET("/metrics/history", h.systemMetricsHistory)
	router.PATCH("/metrics", h.setMetricsState)
	if len(legacyProxy) > 0 {
		target, err := url.Parse(legacyProxy)
		if err != nil {
			return nil, err
		}
		h.proxy = httputil.NewSingleHostReverseProxy(target)
		router.Handle("/metrics/", h.proxy)
		router.Handle("/", h.proxy)
	} else {
		router.HandleFunc("/", http.NotFound)
	}
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
	if len(h.providerURL) == 0 {
		encode(w, state)
		return
	}
	body, _ := json.Marshal(state)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPatch, h.providerURL+"/metrics", bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
