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

package metrics

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ks-tool/vpn-agent/v2/models"
)

func WritePrometheus(w io.Writer, snapshot models.SystemSnapshot) error {
	timestamp := snapshot.Timestamp.UnixMilli()
	metrics := []struct {
		name, help, typ string
		value           float64
	}{
		{"awg_agent_cpu_percent", "Host CPU utilization percent.", "gauge", snapshot.CPUPercent},
		{"awg_agent_mem_used_bytes", "Host memory used, in bytes.", "gauge", float64(snapshot.MemUsedBytes)},
		{"awg_agent_mem_total_bytes", "Host memory total, in bytes.", "gauge", float64(snapshot.MemTotalBytes)},
		{"awg_agent_load1", "Host load average over 1 minute.", "gauge", snapshot.Load1},
		{"awg_agent_load5", "Host load average over 5 minutes.", "gauge", snapshot.Load5},
		{"awg_agent_load15", "Host load average over 15 minutes.", "gauge", snapshot.Load15},
		{"awg_agent_net_rx_bytes", "Host network bytes received.", "counter", float64(snapshot.NetRxBytes)},
		{"awg_agent_net_tx_bytes", "Host network bytes transmitted.", "counter", float64(snapshot.NetTxBytes)},
	}
	for _, metric := range metrics {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %s %d\n", metric.name, metric.help, metric.name, metric.typ, metric.name, strconv.FormatFloat(metric.value, 'f', -1, 64), timestamp); err != nil {
			return err
		}
	}
	return nil
}
