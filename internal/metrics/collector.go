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

// Package metrics collects host metrics only. Provider metrics stay behind
// the provider bridge and are never decoded or aggregated by the agent.
package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ks-tool/vpn-agent/v2/models"
	atomicfile "github.com/natefinch/atomic"
	"github.com/rs/zerolog/log"
)

const (
	systemDB = "system"

	metricCPUPercent    = "cpu_percent"
	metricMemUsedBytes  = "mem_used_bytes"
	metricMemTotalBytes = "mem_total_bytes"
	metricLoad1         = "load1"
	metricLoad5         = "load5"
	metricLoad15        = "load15"
	metricNetRxBytes    = "net_rx_bytes"
	metricNetTxBytes    = "net_tx_bytes"

	retentionPeriod = 48 * time.Hour
	persistInterval = 5 * time.Minute

	HistoryFilename = "metrics-history"
)

type Collector struct {
	store       *store
	interval    time.Duration
	enabled     atomic.Bool
	persistPath string

	mu      sync.Mutex
	prevCPU cpuTotals
	haveCPU bool
	prevNet netStats
	haveNet bool
}

func NewSystemCollector(interval time.Duration) *Collector {
	capacity := int(retentionPeriod/interval) + 1
	collector := &Collector{store: newStore(capacity), interval: interval}
	collector.enabled.Store(true)
	return collector
}

func (c *Collector) SetEnabled(enabled bool)    { c.enabled.Store(enabled) }
func (c *Collector) Enabled() bool              { return c.enabled.Load() }
func (c *Collector) SetPersistence(path string) { c.persistPath = path }

func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	var persistC <-chan time.Time
	if len(c.persistPath) > 0 {
		persistTicker := time.NewTicker(max(c.interval, persistInterval))
		defer persistTicker.Stop()
		persistC = persistTicker.C
	}

	c.collectOnce()
	for {
		select {
		case <-ctx.Done():
			c.persist("shutdown")
			return
		case <-ticker.C:
			c.collectOnce()
		case <-persistC:
			c.persist("checkpoint")
		}
	}
}

func (c *Collector) persist(reason string) {
	if len(c.persistPath) == 0 {
		return
	}
	if err := c.SaveHistory(); err != nil {
		log.Warn().Err(err).Str("reason", reason).Msg("failed to persist metrics history")
	}
}

func (c *Collector) SaveHistory() error {
	if len(c.persistPath) == 0 {
		return nil
	}
	c.store.prune(time.Now().Add(-retentionPeriod))
	buf, err := json.Marshal(c.store.export())
	if err != nil {
		return fmt.Errorf("marshal metrics history: %w", err)
	}
	return atomicfile.WriteFile(c.persistPath, bytes.NewReader(buf))
}

func (c *Collector) LoadHistory() error {
	if len(c.persistPath) == 0 {
		return nil
	}
	buf, err := os.ReadFile(c.persistPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read metrics history: %w", err)
	}
	var data persistedStore
	if err := json.Unmarshal(buf, &data); err != nil {
		return fmt.Errorf("unmarshal metrics history: %w", err)
	}
	c.store.restore(data, time.Now().Add(-retentionPeriod))
	return nil
}

func (c *Collector) collectOnce() {
	if !c.Enabled() {
		return
	}
	if err := c.collectSystem(time.Now()); err != nil {
		log.Warn().Err(err).Msg("failed to collect system metrics")
	}
}

func (c *Collector) collectSystem(now time.Time) error {
	cpu, err := readCPUTotals()
	if err != nil {
		return fmt.Errorf("read cpu stats: %w", err)
	}
	c.mu.Lock()
	prev, haveCPU := c.prevCPU, c.haveCPU
	c.prevCPU, c.haveCPU = cpu, true
	c.mu.Unlock()
	if haveCPU {
		c.store.write(systemDB, metricCPUPercent, now, cpuPercent(prev, cpu))
	}

	mem, err := readMemStats()
	if err != nil {
		return fmt.Errorf("read mem stats: %w", err)
	}
	c.store.write(systemDB, metricMemUsedBytes, now, float64(mem.usedBytes))
	c.store.write(systemDB, metricMemTotalBytes, now, float64(mem.totalBytes))

	load, err := readLoadAvg()
	if err != nil {
		return fmt.Errorf("read load average: %w", err)
	}
	c.store.write(systemDB, metricLoad1, now, load.load1)
	c.store.write(systemDB, metricLoad5, now, load.load5)
	c.store.write(systemDB, metricLoad15, now, load.load15)

	network, err := readNetStats()
	if err != nil {
		return fmt.Errorf("read net stats: %w", err)
	}
	c.mu.Lock()
	prevNet, haveNet := c.prevNet, c.haveNet
	c.prevNet, c.haveNet = network, true
	c.mu.Unlock()
	var rxDelta, txDelta uint64
	if haveNet {
		rxDelta = deltaUint64(prevNet.rxBytes, network.rxBytes)
		txDelta = deltaUint64(prevNet.txBytes, network.txBytes)
	}
	c.store.write(systemDB, metricNetRxBytes, now, float64(rxDelta))
	c.store.write(systemDB, metricNetTxBytes, now, float64(txDelta))
	return nil
}

func deltaUint64(prev, cur uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

func (c *Collector) Snapshot() models.SystemSnapshot {
	var snapshot models.SystemSnapshot
	if point, ok := c.store.last(systemDB, metricCPUPercent); ok {
		snapshot.CPUPercent, snapshot.Timestamp = point.value, point.ts
	}
	if point, ok := c.store.last(systemDB, metricMemUsedBytes); ok {
		snapshot.MemUsedBytes, snapshot.Timestamp = uint64(point.value), point.ts
	}
	if point, ok := c.store.last(systemDB, metricMemTotalBytes); ok {
		snapshot.MemTotalBytes = uint64(point.value)
	}
	if point, ok := c.store.last(systemDB, metricLoad1); ok {
		snapshot.Load1 = point.value
	}
	if point, ok := c.store.last(systemDB, metricLoad5); ok {
		snapshot.Load5 = point.value
	}
	if point, ok := c.store.last(systemDB, metricLoad15); ok {
		snapshot.Load15 = point.value
	}
	if point, ok := c.store.last(systemDB, metricNetRxBytes); ok {
		snapshot.NetRxBytes = uint64(point.value)
	}
	if point, ok := c.store.last(systemDB, metricNetTxBytes); ok {
		snapshot.NetTxBytes = uint64(point.value)
	}
	return snapshot
}

func (c *Collector) SystemHistory() models.SystemHistory {
	rx := c.store.series(systemDB, metricNetRxBytes)
	tx := c.store.series(systemDB, metricNetTxBytes)
	memUsed := c.store.series(systemDB, metricMemUsedBytes)
	memTotal := c.store.series(systemDB, metricMemTotalBytes)
	cpu := c.store.series(systemDB, metricCPUPercent)
	cpuByTS := make(map[int64]float64, len(cpu))
	for _, point := range cpu {
		cpuByTS[point.ts.UnixNano()] = point.value
	}
	points := make([]models.SystemHistoryPoint, len(rx))
	for i, point := range rx {
		history := models.SystemHistoryPoint{
			Timestamp: point.ts, NetRxBytes: uint64(point.value), CPUPercent: cpuByTS[point.ts.UnixNano()],
		}
		if i < len(tx) {
			history.NetTxBytes = uint64(tx[i].value)
		}
		if i < len(memUsed) {
			history.MemUsedBytes = uint64(memUsed[i].value)
		}
		if i < len(memTotal) {
			history.MemTotalBytes = uint64(memTotal[i].value)
		}
		points[i] = history
	}
	return models.SystemHistory{Points: points}
}
