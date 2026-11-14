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
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ks-tool/vpn-agent/v2/models"
)

func TestWritePrometheus(t *testing.T) {
	snapshot := models.SystemSnapshot{Timestamp: time.Unix(1700000000, 0), CPUPercent: 12.5, Load1: 0.5}
	var output bytes.Buffer
	if err := WritePrometheus(&output, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# TYPE awg_agent_cpu_percent gauge", "awg_agent_cpu_percent 12.5 1700000000000", "awg_agent_load1 0.5 1700000000000"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output missing %q:\n%s", expected, output.String())
		}
	}
}
