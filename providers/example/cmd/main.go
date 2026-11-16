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

package main

import (
	"os"
	"path/filepath"

	agentconfig "github.com/ks-tool/vpn-agent/v2/internal/config"
	"github.com/ks-tool/vpn-agent/v2/internal/resource"
	"github.com/ks-tool/vpn-agent/v2/providers/example"
	"github.com/ks-tool/vpn-agent/v2/providers/proxy"

	"github.com/rs/zerolog/log"
)

type config struct {
	AgentAddr string `env:"AGENT_ADDR" flag:"agent-addr" help:"agent provider control address"`
	DB        string `env:"DB" flag:"db" help:"provider state directory"`
}

func main() {
	var cfg config
	if err := agentconfig.Load(&cfg, "VPN_PROVIDER_", os.Args[1:]); err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}
	if cfg.DB == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal().Err(err).Send()
		}
		cfg.DB = filepath.Join(home, ".vpn-example-provider")
	}
	store, err := resource.NewFileStore(cfg.DB)
	if err != nil {
		log.Fatal().Err(err).Send()
	}
	provider, err := example.New()
	if err != nil {
		log.Fatal().Err(err).Send()
	}
	if err := proxy.Serve(proxy.ServeOptions{Address: cfg.AgentAddr, Store: store, Provider: provider}); err != nil {
		log.Fatal().Err(err).Msg("provider control connection failed")
	}
}
