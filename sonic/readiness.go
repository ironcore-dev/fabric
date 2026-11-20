// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	stateFile  = "/etc/sonic/fabric-cell.state"
	bootIDFile = "/etc/sonic/fabric-cell.bootid"
)

func readBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("reading boot_id: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func bgpEstablishedFromVtysh(ctx context.Context) (configured, established int, err error) {
	out, err := hostCommand(ctx, "vtysh", "-c", "show bgp neighbors json").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("vtysh: %w", err)
	}
	var neighbors map[string]struct {
		BGPState string `json:"bgpState"`
	}
	if err := json.Unmarshal(out, &neighbors); err != nil {
		return 0, 0, fmt.Errorf("parsing BGP JSON: %w", err)
	}
	for _, n := range neighbors {
		configured++
		if n.BGPState == "Established" {
			established++
		}
	}
	return configured, established, nil
}

func ifaceUpFromSysfs(name string) (bool, error) {
	data, err := os.ReadFile(filepath.Join("/sys/class/net", name, "operstate"))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(data)) == "up", nil
}

func readStateFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var vlans []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			vlans = append(vlans, line)
		}
	}
	return vlans, nil
}
