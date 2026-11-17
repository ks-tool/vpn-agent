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
	return execFirewallSystem{}.OutputContext(context.Background(), name, args...)
}

func (execFirewallSystem) OutputContext(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(output), err
}

func (execFirewallSystem) Run(name string, args ...string) error {
	return execFirewallSystem{}.RunContext(context.Background(), name, args...)
}

func (execFirewallSystem) RunContext(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

type listenerFirewall struct {
	system firewallSystem
}

type contextFirewallSystem interface {
	OutputContext(context.Context, string, ...string) (string, error)
	RunContext(context.Context, string, ...string) error
}

func (f *listenerFirewall) output(ctx context.Context, name string, args ...string) (string, error) {
	if system, ok := f.system.(contextFirewallSystem); ok {
		return system.OutputContext(ctx, name, args...)
	}
	return f.system.Output(name, args...)
}

func (f *listenerFirewall) run(ctx context.Context, name string, args ...string) error {
	if system, ok := f.system.(contextFirewallSystem); ok {
		return system.RunContext(ctx, name, args...)
	}
	return f.system.Run(name, args...)
}

func newListenerFirewall() *listenerFirewall {
	return &listenerFirewall{system: execFirewallSystem{}}
}

func (f *listenerFirewall) apply(port int, protocol string, add bool) error {
	return f.applyContext(context.Background(), port, protocol, add)
}

func (f *listenerFirewall) applyContext(ctx context.Context, port int, protocol string, add bool) error {
	if port == 0 {
		return nil
	}
	if protocol != "tcp" && protocol != "udp" {
		return errors.New("firewall protocol must be tcp or udp")
	}
	backend := "iptables"
	if _, err := f.output(ctx, "systemctl", "is-active", "--quiet", "firewalld"); err == nil {
		backend = "firewalld"
	} else if _, err := f.output(ctx, "pgrep", "-x", "firewalld"); err == nil {
		backend = "firewalld"
	} else if output, err := f.output(ctx, "ufw", "status"); err == nil && strings.Contains(output, "Status: active") {
		backend = "ufw"
	}
	switch backend {
	case "firewalld":
		return f.applyFirewalld(ctx, port, protocol, add)
	case "ufw":
		return f.applyUFW(ctx, port, protocol, add)
	default:
		return f.applyIptables(ctx, port, protocol, add)
	}
}

func (f *listenerFirewall) applyFirewalld(ctx context.Context, port int, protocol string, add bool) error {
	value := strconv.Itoa(port) + "/" + protocol
	for _, permanent := range []bool{false, true} {
		prefix := []string{}
		if permanent {
			prefix = append(prefix, "--permanent")
		}
		_, queryErr := f.output(ctx, "firewall-cmd", append(append([]string{}, prefix...), "--query-port="+value)...)
		if add == (queryErr == nil) {
			continue
		}
		option := "--remove-port="
		if add {
			option = "--add-port="
		}
		if err := f.run(ctx, "firewall-cmd", append(append([]string{}, prefix...), option+value)...); err != nil {
			return err
		}
	}
	return nil
}

func (f *listenerFirewall) applyUFW(ctx context.Context, port int, protocol string, add bool) error {
	if add {
		return f.run(ctx, "ufw", "allow", strconv.Itoa(port)+"/"+protocol)
	}
	return f.run(ctx, "ufw", "delete", "allow", strconv.Itoa(port)+"/"+protocol)
}

func (f *listenerFirewall) applyIptables(ctx context.Context, port int, protocol string, add bool) error {
	rule := []string{"-p", protocol, "--dport", strconv.Itoa(port), "-j", "ACCEPT"}
	if add {
		changed, err := f.applyIptablesInputRule(ctx, rule)
		if err != nil || !changed {
			return err
		}
	} else {
		check := append([]string{"-C", "INPUT"}, rule...)
		if err := f.run(ctx, "iptables", check...); err != nil {
			return nil
		}
		command := append([]string{"-D", "INPUT"}, rule...)
		if err := f.run(ctx, "iptables", command...); err != nil {
			return err
		}
	}
	if _, err := exec.LookPath("netfilter-persistent"); err != nil {
		return nil
	}
	return f.run(ctx, "netfilter-persistent", "save")
}

func (f *listenerFirewall) applyIptablesInputRule(ctx context.Context, rule []string) (bool, error) {
	output, err := f.output(ctx, "iptables", "-S", "INPUT")
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
		if err := f.run(ctx, "iptables", command...); err != nil {
			return false, err
		}
	}
	command := []string{"-A", "INPUT"}
	if rejectPosition > 0 {
		command = []string{"-I", "INPUT", strconv.Itoa(rejectPosition)}
	}
	command = append(command, rule...)
	if err := f.run(ctx, "iptables", command...); err != nil {
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
