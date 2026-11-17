// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/ironcore-dev/fabric/cellruntime"
)

type configDBSouthPort struct {
	Port  string
	VLAN  int
	IP    string
	Relay string
}

type switchConfig struct {
	Hostname    string
	ASN         int64
	RouterID    string
	LoopbackIPs []string
	Summary     string
	AllPorts    []string
	SouthPorts  []configDBSouthPort
	FabricPorts []string
	IsLeaf      bool
}

func resolveSwitchConfig(cfg *cellruntime.CellConfig) (*switchConfig, error) {
	id, err := strconv.Atoi(cfg.ID)
	if err != nil {
		return nil, cellruntime.TerminalError(fmt.Errorf("cell ID %q must be numeric: %w", cfg.ID, err))
	}
	cell := cfg.Metadata.Namespace + "/" + cfg.Metadata.Name
	switch {
	case cfg.Hostname == "":
		return nil, configDBTerminalCellErrorf(cell, "must set a hostname")
	case len(cfg.LoopbackIPs) == 0:
		return nil, configDBTerminalCellErrorf(cell, "must set at least one loopback IP")
	case len(cfg.Prefixes) == 0:
		return nil, configDBTerminalCellErrorf(cell, "must set at least one prefix")
	case len(cfg.Peers) == 0:
		return nil, configDBTerminalCellErrorf(cell, "must set at least one peer")
	}

	role := cfg.Metadata.Labels[CellRoleLabel]
	var asnBase int64
	var routerIDBase string
	var isLeaf bool
	switch role {
	case RoleLeaf:
		asnBase, routerIDBase, isLeaf = 4211000000, "1.0", true
	case RoleSpine:
		asnBase, routerIDBase = 4212000000, "2.0"
	default:
		return nil, configDBTerminalCellErrorf(cell, "must be labeled %s %s or %s, got %q",
			CellRoleLabel, RoleLeaf, RoleSpine, role)
	}

	var southPeers, fabricPeers []cellruntime.Peer
	for _, peer := range cfg.Peers {
		if peer.Interface == nil {
			return nil, configDBTerminalCellErrorf(cell, "has a peer without interface")
		}
		if _, err := ethernetPortNumber(peer.Interface.ID); err != nil {
			return nil, configDBTerminalCellErrorf(cell, "has invalid Ethernet port ID %q", peer.Interface.ID)
		}
		if peer.DHCPRelay == "" {
			fabricPeers = append(fabricPeers, peer)
			continue
		}
		if _, err := netip.ParseAddr(peer.DHCPRelay); err != nil {
			return nil, configDBTerminalCellErrorf(cell, "has invalid DHCP relay %q", peer.DHCPRelay)
		}
		southPeers = append(southPeers, peer)
	}
	if role == RoleSpine && len(southPeers) > 0 {
		return nil, configDBTerminalCellErrorf(cell, "has role %s but peers with a DHCP relay", RoleSpine)
	}
	if len(cfg.Prefixes) != len(southPeers)+1 {
		return nil, configDBTerminalCellErrorf(cell, "must set %d prefixes, got %d", len(southPeers)+1, len(cfg.Prefixes))
	}

	data := &switchConfig{
		Hostname: cfg.Hostname,
		ASN:      asnBase + int64(id),
		RouterID: fmt.Sprintf("%s.%d.%d", routerIDBase, id/256, id%256),
		Summary:  cfg.Prefixes[0].String(),
		IsLeaf:   isLeaf,
	}
	for _, ip := range cfg.LoopbackIPs {
		data.LoopbackIPs = append(data.LoopbackIPs, netip.PrefixFrom(ip, ip.BitLen()).String())
	}
	for i, peer := range southPeers {
		portNum, _ := ethernetPortNumber(peer.Interface.ID)
		data.SouthPorts = append(data.SouthPorts, configDBSouthPort{
			Port:  peer.Interface.ID,
			VLAN:  portNum/4 + 1001,
			IP:    cfg.Prefixes[i+1].String(),
			Relay: peer.DHCPRelay,
		})
		data.AllPorts = append(data.AllPorts, peer.Interface.ID)
	}
	for _, peer := range fabricPeers {
		data.FabricPorts = append(data.FabricPorts, peer.Interface.ID)
		data.AllPorts = append(data.AllPorts, peer.Interface.ID)
	}

	return data, nil
}

func configDBTerminalCellErrorf(cell, format string, args ...any) error {
	return cellruntime.TerminalError(fmt.Errorf("cell "+cell+": "+format, args...))
}

func ethernetPortNumber(name string) (int, error) {
	if !strings.HasPrefix(name, "Ethernet") {
		return 0, fmt.Errorf("missing Ethernet prefix")
	}
	port, err := strconv.Atoi(strings.TrimPrefix(name, "Ethernet"))
	if err != nil || port < 0 {
		return 0, fmt.Errorf("invalid Ethernet port number")
	}
	return port, nil
}
