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

package kernel

import (
	"errors"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestAddDoesNotDowngradeAmneziaWG(t *testing.T) {
	original := linkAdd
	defer func() { linkAdd = original }()
	var kinds []string
	linkAdd = func(link netlink.Link) error {
		kinds = append(kinds, link.Type())
		return errors.New("netlink unavailable")
	}
	if err := (Backend{}).Add("awg0", true); err == nil {
		t.Fatal("AmneziaWG creation failure was ignored")
	}
	if len(kinds) != 1 || kinds[0] != "amneziawg" {
		t.Fatalf("attempted link kinds = %v, want only amneziawg", kinds)
	}
}
