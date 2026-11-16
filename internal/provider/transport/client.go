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

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ks-tool/vpn-agent/v2/internal/provider/transport/providerv1"
	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const ControlAddressEnv = "VPN_PROVIDER_CONTROL_ADDRESS"

const maxMessageSize = 128 << 20

func ControlAddress() string {
	return os.Getenv(ControlAddressEnv)
}

func Serve(ctx context.Context, address string, definition models.ProviderDefinition, handler http.Handler, runtime *resource.Runtime) error {
	if len(address) == 0 {
		return errors.New("provider control address is required")
	}
	events, unsubscribe := runtime.Subscribe()
	defer unsubscribe()
	for ctx.Err() == nil {
		err := connect(ctx, address, definition, handler, runtime, events)
		if ctx.Err() != nil {
			return nil
		}
		log.Warn().Err(err).Str("provider", definition.Name).Msg("provider control connection lost; reconnecting")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	return nil
}

func connect(parent context.Context, address string, definition models.ProviderDefinition, handler http.Handler, runtime *resource.Runtime, events <-chan resource.Event) error {
	connection, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageSize), grpc.MaxCallSendMsgSize(maxMessageSize)),
	)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	connectTimer := time.AfterFunc(5*time.Second, cancel)
	defer connectTimer.Stop()
	stream, err := providerv1.NewProviderChannelClient(connection).Connect(ctx)
	if err != nil {
		return err
	}
	registered, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	if err := stream.Send(&providerv1.ProviderMessage{Message: &providerv1.ProviderMessage_Register{Register: &providerv1.Register{
		ProtocolVersion: models.ProtocolVersion, Definition: registered,
	}}}); err != nil {
		return err
	}
	if !connectTimer.Stop() {
		return errors.New("provider control connection timed out")
	}

	var sendMu sync.Mutex
	send := func(message *providerv1.ProviderMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(message)
	}
	var handlers sync.WaitGroup
	handlers.Add(1)
	go func() {
		defer handlers.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case event, open := <-events:
				if !open {
					cancel()
					return
				}
				data, marshalErr := json.Marshal(event)
				if marshalErr != nil {
					log.Error().Err(marshalErr).Msg("failed to encode provider resource event")
					continue
				}
				if sendErr := send(&providerv1.ProviderMessage{Message: &providerv1.ProviderMessage_Event{Event: data}}); sendErr != nil {
					cancel()
					return
				}
			}
		}
	}()
	runtime.Resync()
	log.Info().Str("provider", definition.Name).Msg("provider connected to agent")

	for {
		message, recvErr := stream.Recv()
		if recvErr != nil {
			cancel()
			handlers.Wait()
			return recvErr
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			handleRequest(ctx, handler, message, send)
		}()
	}
}

func handleRequest(ctx context.Context, handler http.Handler, message *providerv1.AgentMessage, send func(*providerv1.ProviderMessage) error) {
	response := &providerv1.Response{Id: message.Id, Status: http.StatusOK, Headers: map[string]string{}}
	request := httptest.NewRequest(message.Method, message.Path, bytes.NewReader(message.Body))
	request = request.WithContext(ctx)
	for name, value := range message.Headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	result := recorder.Result()
	response.Status = int32(result.StatusCode)
	for name, values := range result.Header {
		response.Headers[name] = strings.Join(values, ", ")
	}
	body, err := io.ReadAll(result.Body)
	_ = result.Body.Close()
	if err != nil {
		response.Status = http.StatusInternalServerError
		response.Error = err.Error()
	} else {
		response.Body = body
	}
	if err := send(&providerv1.ProviderMessage{Message: &providerv1.ProviderMessage_Response{Response: response}}); err != nil && ctx.Err() == nil {
		log.Error().Err(err).Str("request", message.Id).Msg("failed to send provider response")
	}
}
