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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ks-tool/vpn-agent/v2/internal/provider/transport/providerv1"
	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"
)

type providerSession struct {
	stream providerv1.ProviderChannel_ConnectServer
	done   chan struct{}
	once   sync.Once

	sendMu sync.Mutex
	mu     sync.Mutex
	nextID atomic.Uint64
	calls  map[string]chan *providerv1.Response
}

func newProviderSession(stream providerv1.ProviderChannel_ConnectServer) *providerSession {
	return &providerSession{stream: stream, done: make(chan struct{}), calls: make(map[string]chan *providerv1.Response)}
}

func (s *providerSession) close() {
	s.once.Do(func() { close(s.done) })
}

func (s *providerSession) request(ctx context.Context, message *providerv1.AgentMessage) (*providerv1.Response, error) {
	message.Id = strconv.FormatUint(s.nextID.Add(1), 10)
	response := make(chan *providerv1.Response, 1)
	s.mu.Lock()
	s.calls[message.Id] = response
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.calls, message.Id)
		s.mu.Unlock()
	}()
	s.sendMu.Lock()
	err := s.stream.Send(message)
	s.sendMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case result := <-response:
		return result, nil
	case <-s.done:
		return nil, errors.New("provider disconnected")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *providerSession) deliver(response *providerv1.Response) {
	s.mu.Lock()
	call := s.calls[response.Id]
	s.mu.Unlock()
	if call != nil {
		call <- response
	}
}

type statusSink interface {
	UpdateProviderStatus(resource.Event) error
}

type Provider struct {
	name string
	sink statusSink

	mu         sync.RWMutex
	definition models.ProviderDefinition
	session    *providerSession
}

type httpError struct {
	statusCode int
	message    string
}

func (e *httpError) Error() string { return e.message }

func NewProvider(definition models.ProviderDefinition, sink statusSink) (*Provider, error) {
	if len(definition.Name) == 0 {
		return nil, errors.New("remote provider name is required")
	}
	return &Provider{name: definition.Name, definition: definition, sink: sink}, nil
}

func (p *Provider) Name() string { return p.name }
func (p *Provider) Version() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.definition.Version
}
func (p *Provider) Definitions() []models.ResourceDefinition {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.definition.Resources
}

func (p *Provider) connect(definition models.ProviderDefinition, session *providerSession) error {
	if definition.Name != p.name || len(definition.Version) == 0 {
		return errors.New("invalid remote provider registration")
	}
	p.mu.Lock()
	p.definition = definition
	previous := p.session
	p.session = session
	p.mu.Unlock()
	if previous != nil {
		previous.close()
	}
	return nil
}

func (p *Provider) disconnect(session *providerSession) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != session {
		return false
	}
	p.session = nil
	return true
}

func (p *Provider) close() {
	p.mu.Lock()
	session := p.session
	p.session = nil
	p.mu.Unlock()
	if session != nil {
		session.close()
	}
}

func (p *Provider) handleEvent(data []byte) error {
	var event resource.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	if event.Provider != p.name {
		return fmt.Errorf("resource event provider %q does not match %q", event.Provider, p.name)
	}
	return p.sink.UpdateProviderStatus(event)
}

func (p *Provider) Validate(context.Context, models.Resource) error { return nil }

func (p *Provider) Reconcile(ctx context.Context, item models.Resource) (json.RawMessage, error) {
	var result models.Resource
	path := resourcePath(p.name, item.Kind, item.Name)
	if err := p.do(ctx, http.MethodPut, path, item, &result); err != nil {
		return nil, err
	}
	var status models.ResourceStatus
	if err := p.do(ctx, http.MethodGet, path+"/status", nil, &status); err != nil {
		return nil, err
	}
	if status.ObservedGeneration >= result.Generation {
		switch status.Phase {
		case models.ResourceReady:
			return status.Provider, nil
		case models.ResourceDegraded:
			return nil, errors.New(statusMessage(status))
		}
	}
	return nil, resource.ErrReconciliationPending
}

func (p *Provider) Delete(ctx context.Context, item models.Resource) error {
	path := resourcePath(p.name, item.Kind, item.Name)
	if err := p.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	var status models.ResourceStatus
	err := p.do(ctx, http.MethodGet, path+"/status", nil, &status)
	if err != nil {
		var responseErr *httpError
		if errors.As(err, &responseErr) && responseErr.statusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	if status.Phase == models.ResourceDegraded {
		return errors.New(statusMessage(status))
	}
	return resource.ErrReconciliationPending
}

func (p *Provider) Action(ctx context.Context, item models.Resource, action string, arguments json.RawMessage) (models.ActionResult, error) {
	var result models.ActionResult
	path := resourcePath(p.name, item.Kind, item.Name) + "/actions/" + url.PathEscape(action)
	err := p.do(ctx, http.MethodPost, path, models.ActionRequest{Arguments: arguments}, &result)
	return result, err
}

func (p *Provider) PrepareLink(ctx context.Context, link models.Link, endpoint models.LinkEndpoint) (models.LinkOffer, error) {
	var offer models.LinkOffer
	request := models.LinkRequest{Link: link, Endpoint: endpoint}
	err := p.do(ctx, http.MethodPost, "/v1/providers/"+url.PathEscape(p.name)+"/links/prepare", request, &offer)
	return offer, err
}

func (p *Provider) CommitLink(ctx context.Context, link models.Link, endpoint models.LinkEndpoint, remote models.LinkOffer) error {
	request := models.LinkRequest{Link: link, Endpoint: endpoint, RemoteOffer: &remote}
	return p.do(ctx, http.MethodPost, "/v1/providers/"+url.PathEscape(p.name)+"/links/commit", request, nil)
}

func (p *Provider) DeleteLink(ctx context.Context, link models.Link, endpoint models.LinkEndpoint, remote *models.LinkOffer) error {
	request := models.LinkRequest{Link: link, Endpoint: endpoint, RemoteOffer: remote}
	return p.do(ctx, http.MethodPost, "/v1/providers/"+url.PathEscape(p.name)+"/links/delete", request, nil)
}

func resourcePath(provider, kind, name string) string {
	return "/v1/providers/" + url.PathEscape(provider) + "/resources/" + url.PathEscape(kind) + "/" + url.PathEscape(name)
}

func (p *Provider) request(ctx context.Context, method, path string, body []byte, headers map[string]string) (*providerv1.Response, error) {
	p.mu.RLock()
	session := p.session
	p.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("remote provider %s is not connected", p.name)
	}
	return session.request(ctx, &providerv1.AgentMessage{Method: method, Path: path, Body: body, Headers: headers})
}

func (p *Provider) do(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	response, err := p.request(ctx, method, path, body, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return fmt.Errorf("remote provider %s: %w", p.name, err)
	}
	if len(response.Error) > 0 {
		return errors.New(response.Error)
	}
	if response.Status < 200 || response.Status >= 300 {
		return &httpError{statusCode: int(response.Status), message: fmt.Sprintf("remote provider %s returned %d: %s", p.name, response.Status, strings.TrimSpace(string(response.Body)))}
	}
	if output == nil || response.Status == http.StatusNoContent || len(response.Body) == 0 {
		return nil
	}
	return json.Unmarshal(response.Body, output)
}

func statusMessage(status models.ResourceStatus) string {
	if len(status.Conditions) > 0 && len(status.Conditions[0].Message) > 0 {
		return status.Conditions[0].Message
	}
	return "remote provider reconciliation failed"
}

var _ resource.Provider = (*Provider)(nil)
var _ resource.ActionProvider = (*Provider)(nil)
var _ resource.LinkProvider = (*Provider)(nil)
