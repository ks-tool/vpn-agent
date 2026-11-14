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

package service

import (
	"net"

	awgmodels "github.com/ks-tool/vpn-agent/v2/providers/awg/models"

	"github.com/Jipok/wgctrl-go/wgtypes"
)

func toAmneziaConfig(cfg awgmodels.InterfaceConfig) *wgtypes.Config {
	privateKey := wgtypes.Key(cfg.PrivateKey)
	peers := make([]wgtypes.PeerConfig, 0, len(cfg.Peers))
	for _, peer := range cfg.Peers {
		if peer.Disabled {
			continue
		}
		peers = append(peers, toPeerConfig(peer))
	}
	return &wgtypes.Config{
		PrivateKey:   &privateKey,
		ListenPort:   new(int(cfg.ListenPort)),
		FirewallMark: cfg.FirewallMark,
		Peers:        peers,
		Jc:           cfg.Jc,
		Jmin:         cfg.Jmin,
		Jmax:         cfg.Jmax,
		S1:           cfg.S1,
		S2:           cfg.S2,
		S3:           cfg.S3,
		S4:           cfg.S4,
		H1:           cfg.H1,
		H2:           cfg.H2,
		H3:           cfg.H3,
		H4:           cfg.H4,
		I1:           cfg.I1,
		I2:           cfg.I2,
		I3:           cfg.I3,
		I4:           cfg.I4,
		I5:           cfg.I5,
	}
}

func toPeerConfig(peer awgmodels.InterfacePeer) wgtypes.PeerConfig {
	cfg := wgtypes.PeerConfig{
		PublicKey:                   wgtypes.Key(peer.Key),
		PersistentKeepaliveInterval: &peer.KeepaliveInterval,
	}
	if peer.PresharedKey != nil {
		presharedKey := wgtypes.Key(*peer.PresharedKey)
		cfg.PresharedKey = &presharedKey
	}
	if len(peer.Endpoint) > 0 {
		cfg.Endpoint, _ = net.ResolveUDPAddr("udp", peer.Endpoint)
	}
	for _, ip := range peer.AllowedIPs {
		if _, network, _ := net.ParseCIDR(ip); network != nil {
			cfg.AllowedIPs = append(cfg.AllowedIPs, *network)
		}
	}
	return cfg
}
