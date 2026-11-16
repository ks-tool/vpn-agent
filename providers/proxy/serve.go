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

package proxy

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ks-tool/vpn-agent/v2/internal/api"
	"github.com/ks-tool/vpn-agent/v2/internal/provider/transport"
	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/models"
	"github.com/ks-tool/vpn-agent/v2/mux/middleware"
)

type ServeOptions struct {
	Address  string
	Store    resource.Store
	Provider resource.Provider
	Start    func() error
	Handler  func(*resource.Runtime) http.Handler
	Close    func() error
}

func Serve(options ServeOptions) error {
	address := options.Address
	if len(address) == 0 {
		address = transport.ControlAddress()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtime := resource.NewRuntime(options.Store, time.Second, 30*time.Second)
	defer runtime.Close()
	if err := runtime.Register(options.Provider); err != nil {
		return err
	}
	runtime.Start(ctx)
	if options.Start != nil {
		if err := options.Start(); err != nil {
			return err
		}
	}
	handler := options.Handler
	if handler == nil {
		handler = func(runtime *resource.Runtime) http.Handler {
			return api.NewResourceHandler(runtime, middleware.RequestID, middleware.Logging)
		}
	}
	definition := models.ProviderDefinition{Name: options.Provider.Name(), Version: options.Provider.Version(), Resources: options.Provider.Definitions()}
	serveErr := transport.Serve(ctx, address, definition, handler(runtime), runtime)
	if options.Close != nil {
		return errors.Join(serveErr, options.Close())
	}
	return serveErr
}
