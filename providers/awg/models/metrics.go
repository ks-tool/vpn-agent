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

package models

import (
	"time"

	agentmodels "github.com/ks-tool/vpn-agent/v2/models"
)

// These types are the JSON wire format of the system /metrics API and the
// provider-specific /metrics/{interface} API. The admin decodes both after
// fetching them through the agent's transparent provider bridge.

// PeerSnapshot is the latest recorded sample for one peer of one interface.
type PeerSnapshot struct {
	PublicKey     string    `json:"publicKey"`
	RxBytes       uint64    `json:"rx"`
	TxBytes       uint64    `json:"tx"`
	LastHandshake time.Time `json:"lastHandshake"`
	ProbeAddress  string    `json:"probeAddress,omitempty"`
	// LatencyP95Ms is the agent-measured 95th-percentile ICMP round trip over
	// the last 15 minutes, nil until a probe succeeds.
	LatencyP95Ms *float64 `json:"latencyP95Ms,omitempty"`
}

// InterfaceSnapshot groups peer snapshots under their interface name.
type InterfaceSnapshot struct {
	Interface string         `json:"interface"`
	Peers     []PeerSnapshot `json:"peers"`
}

// MetricsSnapshot is the AWG provider's metrics response.
type MetricsSnapshot struct {
	System     agentmodels.SystemSnapshot `json:"system"`
	Interfaces []InterfaceSnapshot        `json:"interfaces"`
}

// SystemHistory includes host history and AWG peer history.
type SystemHistory struct {
	Points     []agentmodels.SystemHistoryPoint `json:"points"`
	Interfaces []InterfaceHistory               `json:"interfaces"`
}

// PeerHistoryPoint is one retained sample for a single peer. Unlike
// SystemHistoryPoint's per-interval network deltas, RxBytes/TxBytes are the
// cumulative byte counters WireGuard reports for the peer (same as
// PeerSnapshot); differencing consecutive points yields per-interval traffic.
// LastHandshake is the peer's last successful handshake at that sample (zero
// value if it had never completed one yet).
type PeerHistoryPoint struct {
	Timestamp     time.Time `json:"timestamp"`
	RxBytes       uint64    `json:"rx"`
	TxBytes       uint64    `json:"tx"`
	LastHandshake time.Time `json:"lastHandshake"`
}

// PeerHistory is every retained sample for one peer, oldest first.
type PeerHistory struct {
	PublicKey string             `json:"publicKey"`
	Points    []PeerHistoryPoint `json:"points"`
}

// InterfaceHistory groups per-peer history under their interface name, as
// carried in SystemHistory.Interfaces.
type InterfaceHistory struct {
	Interface string        `json:"interface"`
	Peers     []PeerHistory `json:"peers"`
}
