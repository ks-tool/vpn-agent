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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/ks-tool/vpn-agent/v2/internal/provider/transport/providerv1"
	"github.com/ks-tool/vpn-agent/v2/models"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Registry struct {
	providerv1.UnimplementedProviderChannelServer
	runtime ProviderRuntime

	mu        sync.RWMutex
	providers map[string]*Provider
	server    *grpc.Server
	listener  net.Listener
	socket    string
	wg        sync.WaitGroup
}

func NewRegistry(runtime ProviderRuntime) *Registry {
	return &Registry{runtime: runtime, providers: make(map[string]*Provider)}
}

func (r *Registry) Start(socket string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.server != nil {
		return errors.New("provider registry is already started")
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("provider control socket %s exists and is not a socket", socket)
		}
		if err := os.Remove(socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		return err
	}
	server := grpc.NewServer(grpc.MaxRecvMsgSize(128<<20), grpc.MaxSendMsgSize(128<<20))
	providerv1.RegisterProviderChannelServer(server, r)
	r.server, r.listener, r.socket = server, listener, socket
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := server.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, grpc.ErrServerStopped) {
			log.Error().Err(err).Msg("provider control server stopped")
		}
	}()
	return nil
}

func (r *Registry) Close() {
	r.mu.Lock()
	server, listener, socket := r.server, r.listener, r.socket
	r.server, r.listener, r.socket = nil, nil, ""
	for _, provider := range r.providers {
		provider.close()
	}
	r.mu.Unlock()
	if server != nil {
		server.Stop()
	}
	if listener != nil {
		_ = listener.Close()
	}
	r.wg.Wait()
	if len(socket) > 0 {
		_ = os.Remove(socket)
	}
}

func (r *Registry) Reload(registrations []models.ProviderRegistration) error {
	desired := make(map[string]models.ProviderRegistration, len(registrations))
	for _, registration := range registrations {
		name := registration.Provider.Name
		if len(name) == 0 {
			return errors.New("provider name is required")
		}
		if _, exists := desired[name]; exists {
			return fmt.Errorf("provider %s is registered more than once", name)
		}
		desired[name] = registration
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, provider := range r.providers {
		if _, exists := desired[name]; exists {
			continue
		}
		provider.close()
		r.runtime.Unregister(name)
		delete(r.providers, name)
	}
	for name, registration := range desired {
		if _, exists := r.providers[name]; exists {
			continue
		}
		provider, err := NewProvider(registration.Provider, r.runtime)
		if err != nil {
			return err
		}
		if err := r.runtime.Register(provider); err != nil {
			return err
		}
		r.providers[name] = provider
	}
	r.runtime.Resync()
	return nil
}

func (r *Registry) Connect(stream providerv1.ProviderChannel_ConnectServer) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	registration := message.GetRegister()
	if registration == nil {
		return status.Error(codes.InvalidArgument, "first provider message must be a registration")
	}
	if registration.ProtocolVersion != models.ProtocolVersion {
		return status.Error(codes.FailedPrecondition, "unsupported provider protocol version")
	}
	var definition models.ProviderDefinition
	if err := json.Unmarshal(registration.Definition, &definition); err != nil {
		return status.Error(codes.InvalidArgument, "invalid provider definition")
	}
	r.mu.RLock()
	provider := r.providers[definition.Name]
	r.mu.RUnlock()
	if provider == nil {
		return status.Error(codes.NotFound, "provider is not registered")
	}
	session := newProviderSession(stream)
	if err := provider.connect(definition, session); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	r.runtime.SetProviderError(definition.Name, nil)
	events := make(chan []byte, 64)
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for data := range events {
			if err := provider.handleEvent(data); err != nil {
				log.Warn().Err(err).Str("provider", definition.Name).Msg("failed to apply provider resource event")
			}
		}
	}()
	defer func() {
		close(events)
		<-eventsDone
	}()
	r.runtime.Resync()
	log.Info().Str("provider", definition.Name).Str("version", definition.Version).Msg("provider registered")
	defer func() {
		session.close()
		if provider.disconnect(session) {
			r.runtime.SetProviderError(definition.Name, errors.New("provider disconnected"))
			r.runtime.Resync()
			log.Warn().Str("provider", definition.Name).Msg("provider disconnected")
		}
	}()
	messages := make(chan *providerv1.ProviderMessage)
	receiveErrors := make(chan error, 1)
	go func() {
		for {
			message, err := stream.Recv()
			if err != nil {
				select {
				case receiveErrors <- err:
				case <-stream.Context().Done():
				}
				return
			}
			select {
			case messages <- message:
			case <-stream.Context().Done():
				return
			}
		}
	}()
	for {
		select {
		case <-session.done:
			return errors.New("provider session closed")
		case <-stream.Context().Done():
			return stream.Context().Err()
		case err := <-receiveErrors:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case message := <-messages:
			switch value := message.Message.(type) {
			case *providerv1.ProviderMessage_Response:
				session.deliver(value.Response)
			case *providerv1.ProviderMessage_Event:
				select {
				case events <- value.Event:
				case <-stream.Context().Done():
					return stream.Context().Err()
				case <-session.done:
					return errors.New("provider session closed")
				}
			default:
				return status.Error(codes.InvalidArgument, "unexpected provider message")
			}
		}
	}
}
