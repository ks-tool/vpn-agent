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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/rs/zerolog/log"
)

type supervisedProvider struct {
	registration models.ProviderRegistration
	cancel       context.CancelFunc
	done         chan struct{}

	mu      sync.RWMutex
	exitErr error
	output  string
}

func (p *supervisedProvider) recordError(err error, output string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exitErr, p.output = err, output
}

func (p *supervisedProvider) lastError() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.exitErr == nil {
		return nil
	}
	if len(p.output) > 0 {
		return fmt.Errorf("%w\n%s", p.exitErr, p.output)
	}
	return p.exitErr
}

const providerOutputLimit = 16 << 10

type outputTail struct {
	mu   sync.Mutex
	data []byte
}

func (b *outputTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > providerOutputLimit {
		b.data = append([]byte(nil), b.data[len(b.data)-providerOutputLimit:]...)
	}
	return len(p), nil
}

func (b *outputTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

type Supervisor struct {
	registrations []models.ProviderRegistration
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	providers     map[string]*supervisedProvider
	closed        bool
	wg            sync.WaitGroup
}

func NewSupervisor(registrations []models.ProviderRegistration) *Supervisor {
	return &Supervisor{registrations: registrations, providers: make(map[string]*supervisedProvider)}
}

func (s *Supervisor) Start(parent context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("provider supervisor is already started")
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	return s.reloadLocked(s.registrations)
}

func (s *Supervisor) Reload(registrations []models.ProviderRegistration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("provider supervisor is closed")
	}
	if s.cancel == nil {
		return errors.New("provider supervisor is not started")
	}
	return s.reloadLocked(registrations)
}

func (s *Supervisor) ProviderError(name string) error {
	s.mu.Lock()
	provider := s.providers[name]
	s.mu.Unlock()
	if provider == nil {
		return nil
	}
	return provider.lastError()
}

func (s *Supervisor) reloadLocked(registrations []models.ProviderRegistration) error {
	desired := make(map[string]models.ProviderRegistration, len(registrations))
	for _, registration := range registrations {
		name := registration.Provider.Name
		if len(name) == 0 {
			return errors.New("provider name is required")
		}
		if _, exists := desired[name]; exists {
			return fmt.Errorf("provider %s is registered more than once", name)
		}
		if len(registration.Command) > 0 && !filepath.IsAbs(registration.Command) {
			return fmt.Errorf("provider %s command must be an absolute path", name)
		}
		desired[name] = registration
	}
	for name, current := range s.providers {
		next, exists := desired[name]
		if exists && reflect.DeepEqual(current.registration, next) {
			continue
		}
		current.cancel()
		<-current.done
		delete(s.providers, name)
	}
	for name, registration := range desired {
		if _, exists := s.providers[name]; exists || len(registration.Command) == 0 {
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		current := &supervisedProvider{registration: registration, cancel: cancel, done: make(chan struct{})}
		s.providers[name] = current
		s.wg.Add(1)
		go s.supervise(ctx, current)
	}
	return nil
}

func (s *Supervisor) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Supervisor) supervise(ctx context.Context, current *supervisedProvider) {
	defer s.wg.Done()
	defer close(current.done)
	registration := current.registration
	name := registration.Provider.Name
	for ctx.Err() == nil {
		output := &outputTail{}
		cmd := exec.Command(registration.Command, registration.Args...)
		cmd.Env = os.Environ()
		for key, value := range registration.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		cmd.Stdout = io.MultiWriter(os.Stdout, output)
		cmd.Stderr = io.MultiWriter(os.Stderr, output)

		done := make(chan error, 1)
		if err := cmd.Start(); err != nil {
			current.recordError(fmt.Errorf("start provider: %w", err), output.String())
			log.Error().Err(err).Str("provider", name).Str("command", registration.Command).Msg("failed to start provider")
		} else {
			log.Info().Str("provider", name).Int("pid", cmd.Process.Pid).Msg("provider started")
			go func() { done <- cmd.Wait() }()
			select {
			case <-ctx.Done():
				stopProcess(cmd, done)
				return
			case err := <-done:
				if err == nil {
					err = errors.New("provider exited")
				}
				current.recordError(err, output.String())
				if !errors.Is(err, context.Canceled) {
					log.Error().Err(err).Str("provider", name).Msg("provider exited; restarting")
				} else {
					log.Warn().Str("provider", name).Msg("provider exited; restarting")
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func stopProcess(cmd *exec.Cmd, done <-chan error) {
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
