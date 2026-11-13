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
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

type firewallSystem interface {
	Output(string, ...string) (string, error)
	Run(string, ...string) error
}

type execFirewallSystem struct{}

func (execFirewallSystem) Output(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	return string(output), err
}

func (execFirewallSystem) Run(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

type listenerFirewall struct {
	system firewallSystem
}

func newListenerFirewall() *listenerFirewall {
	return &listenerFirewall{system: execFirewallSystem{}}
}

func (f *listenerFirewall) apply(port int, protocol string, add bool) error {
	if port == 0 {
		return nil
	}
	if protocol != "tcp" && protocol != "udp" {
		return errors.New("firewall protocol must be tcp or udp")
	}
	backend := "iptables"
	if _, err := f.system.Output("systemctl", "is-active", "--quiet", "firewalld"); err == nil {
		backend = "firewalld"
	} else if _, err := f.system.Output("pgrep", "-x", "firewalld"); err == nil {
		backend = "firewalld"
	} else if output, err := f.system.Output("ufw", "status"); err == nil && strings.Contains(output, "Status: active") {
		backend = "ufw"
	}
	switch backend {
	case "firewalld":
		return f.applyFirewalld(port, protocol, add)
	case "ufw":
		return f.applyUFW(port, protocol, add)
	default:
		return f.applyIptables(port, protocol, add)
	}
}

func (f *listenerFirewall) applyFirewalld(port int, protocol string, add bool) error {
	value := strconv.Itoa(port) + "/" + protocol
	for _, permanent := range []bool{false, true} {
		prefix := []string{}
		if permanent {
			prefix = append(prefix, "--permanent")
		}
		_, queryErr := f.system.Output("firewall-cmd", append(append([]string{}, prefix...), "--query-port="+value)...)
		if add == (queryErr == nil) {
			continue
		}
		option := "--remove-port="
		if add {
			option = "--add-port="
		}
		if err := f.system.Run("firewall-cmd", append(append([]string{}, prefix...), option+value)...); err != nil {
			return err
		}
	}
	return nil
}

func (f *listenerFirewall) applyUFW(port int, protocol string, add bool) error {
	if add {
		return f.system.Run("ufw", "allow", strconv.Itoa(port)+"/"+protocol)
	}
	return f.system.Run("ufw", "delete", "allow", strconv.Itoa(port)+"/"+protocol)
}

func (f *listenerFirewall) applyIptables(port int, protocol string, add bool) error {
	rule := []string{"-p", protocol, "--dport", strconv.Itoa(port), "-j", "ACCEPT"}
	if add {
		changed, err := f.applyIptablesInputRule(rule)
		if err != nil || !changed {
			return err
		}
	} else {
		check := append([]string{"-C", "INPUT"}, rule...)
		if err := f.system.Run("iptables", check...); err != nil {
			return nil
		}
		command := append([]string{"-D", "INPUT"}, rule...)
		if err := f.system.Run("iptables", command...); err != nil {
			return err
		}
	}
	if _, err := exec.LookPath("netfilter-persistent"); err != nil {
		return nil
	}
	return f.system.Run("netfilter-persistent", "save")
}

func (f *listenerFirewall) applyIptablesInputRule(rule []string) (bool, error) {
	output, err := f.system.Output("iptables", "-S", "INPUT")
	if err != nil {
		return false, err
	}
	rejectPosition, rulePosition, position := 0, 0, 0
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "-A" || fields[1] != "INPUT" {
			continue
		}
		position++
		if rulePosition == 0 && sameIptablesRule(fields[2:], rule) {
			rulePosition = position
			continue
		}
		if rejectPosition == 0 && len(fields) >= 4 && fields[2] == "-j" && (fields[3] == "REJECT" || fields[3] == "DROP") &&
			(len(fields) == 4 || len(fields) == 6 && fields[4] == "--reject-with") {
			rejectPosition = position
		}
	}
	if rulePosition > 0 && (rejectPosition == 0 || rulePosition < rejectPosition) {
		return false, nil
	}
	if rulePosition > 0 {
		command := append([]string{"-D", "INPUT"}, rule...)
		if err := f.system.Run("iptables", command...); err != nil {
			return false, err
		}
	}
	command := []string{"-A", "INPUT"}
	if rejectPosition > 0 {
		command = []string{"-I", "INPUT", strconv.Itoa(rejectPosition)}
	}
	command = append(command, rule...)
	if err := f.system.Run("iptables", command...); err != nil {
		return false, err
	}
	return true, nil
}

func sameIptablesRule(actual, desired []string) bool {
	normalized := make([]string, 0, len(actual))
	for i := 0; i < len(actual); i++ {
		if actual[i] == "-m" && i+1 < len(actual) && (actual[i+1] == "tcp" || actual[i+1] == "udp") {
			i++
			continue
		}
		normalized = append(normalized, actual[i])
	}
	return slices.Equal(normalized, desired)
}
