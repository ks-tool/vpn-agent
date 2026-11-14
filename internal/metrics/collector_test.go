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
	"path/filepath"
	"testing"
	"time"
)

func TestCollectorSystemMetricsPersistWithoutProviderData(t *testing.T) {
	path := filepath.Join(t.TempDir(), HistoryFilename)
	now := time.Now()
	first := NewSystemCollector(time.Minute)
	first.SetPersistence(path)
	first.store.write(systemDB, metricCPUPercent, now, 42.5)
	first.store.write(systemDB, metricNetRxBytes, now, 1000)
	if err := first.SaveHistory(); err != nil {
		t.Fatal(err)
	}

	second := NewSystemCollector(time.Minute)
	second.SetPersistence(path)
	if err := second.LoadHistory(); err != nil {
		t.Fatal(err)
	}
	snapshot := second.Snapshot()
	if snapshot.CPUPercent != 42.5 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	history := second.SystemHistory()
	if len(history.Points) != 1 || history.Points[0].NetRxBytes != 1000 {
		t.Fatalf("unexpected history: %+v", history)
	}
}

func TestCollectorLoadHistoryDropsStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), HistoryFilename)
	first := NewSystemCollector(time.Minute)
	first.SetPersistence(path)
	first.store.write(systemDB, metricNetRxBytes, time.Now().Add(-retentionPeriod-time.Hour), 1)
	first.store.write(systemDB, metricNetRxBytes, time.Now(), 2)
	if err := first.SaveHistory(); err != nil {
		t.Fatal(err)
	}
	second := NewSystemCollector(time.Minute)
	second.SetPersistence(path)
	if err := second.LoadHistory(); err != nil {
		t.Fatal(err)
	}
	series := second.store.series(systemDB, metricNetRxBytes)
	if len(series) != 1 || series[0].value != 2 {
		t.Fatalf("unexpected retained series: %+v", series)
	}
}
