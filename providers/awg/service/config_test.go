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
	"testing"

	awgmodels "github.com/ks-tool/vpn-agent/v2/providers/awg/models"

	"github.com/Jipok/wgctrl-go/wgtypes"
)

func TestDisabledPeerIsOmittedFromDeviceConfig(t *testing.T) {
	activeKey, err := awgmodels.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	offKey, err := awgmodels.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	activePub := activeKey.PublicKey()
	offPub := offKey.PublicKey()
	if got := wgtypes.Key(activeKey).PublicKey(); got != wgtypes.Key(activePub) {
		t.Fatalf("public key differs from wgctrl: %v != %v", got, wgtypes.Key(activePub))
	}
	cfg := awgmodels.InterfaceConfig{
		Interface: "wg0",
		Peers: []awgmodels.InterfacePeer{
			{Key: activePub, AllowedIPs: []string{"10.0.0.2/32"}},
			{Key: offPub, AllowedIPs: []string{"10.0.0.3/32"}, Disabled: true},
		},
	}
	got := toAmneziaConfig(cfg)
	if len(got.Peers) != 1 || got.Peers[0].PublicKey != wgtypes.Key(activePub) {
		t.Fatalf("active peers = %+v", got.Peers)
	}
	cfg.Peers[1].Disabled = false
	if got := toAmneziaConfig(cfg); len(got.Peers) != 2 {
		t.Fatalf("peers after reactivating = %d, want 2", len(got.Peers))
	}
}
