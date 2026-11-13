// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ironcore-dev/fabric/cellruntime"
)

var update = flag.Bool("update", false, "update golden files")

// compareGolden compares got with the golden file testdata/<name>.
// Regenerate with: go test ./sonic/ -update
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file %s: %v (run with -update to create it)", path, err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("rendered script differs from %s (-want +got):\n%s", path, diff)
	}
}

// testCell builds a small leaf cell config: one summary prefix, one /80
// interface prefix per south-facing server port (Ethernet0, 4) and two
// spine uplinks (Ethernet108, 112) as fabric peers.
func testCell(hostname string) *cellruntime.CellConfig {
	prefixes := []netip.Prefix{netip.MustParsePrefix("fd00:1234:5678:f2::/64")}
	peers := make([]cellruntime.Peer, 0, 4)
	for i := 0; i <= 4; i += 4 {
		prefixes = append(prefixes, netip.MustParsePrefix(fmt.Sprintf("fd00:1234:5678:f2:%04x::/80", i+1)))
		peers = append(peers, cellruntime.Peer{
			Interface: &cellruntime.Interface{
				Metadata: cellruntime.InterfaceMetadata{Name: fmt.Sprintf("eth%d", i)},
				ID:       fmt.Sprintf("Ethernet%d", i),
			},
			DHCPRelay: "fd00:1234:5678:3201::1:547",
		})
	}
	for i := 108; i <= 112; i += 4 {
		peers = append(peers, cellruntime.Peer{
			Interface: &cellruntime.Interface{
				Metadata: cellruntime.InterfaceMetadata{Name: fmt.Sprintf("eth%d", i)},
				ID:       fmt.Sprintf("Ethernet%d", i),
			},
		})
	}
	return &cellruntime.CellConfig{
		Metadata: cellruntime.CellMetadata{
			Namespace: "default", Name: "cell-42", Labels: map[string]string{CellRoleLabel: RoleLeaf},
		},
		ID:          "42",
		Hostname:    hostname,
		LoopbackIPs: []netip.Addr{netip.MustParseAddr("fd00:1234:5678:f2::")},
		Prefixes:    prefixes,
		Peers:       peers,
	}
}

func TestRenderCell(t *testing.T) {
	for _, test := range []struct {
		name     string
		hostname string
		mutate   func(*cellruntime.CellConfig)
	}{
		{name: "leaf", hostname: "swi1-ab-42.fabric.example.com"},
		{name: "spine", hostname: "swi2-ab-7.fabric.example.com", mutate: func(c *cellruntime.CellConfig) {
			// A spine cell has the spine role label, the summary prefix and
			// only fabric peers.
			c.Metadata.Labels[CellRoleLabel] = RoleSpine
			var fabricPeers []cellruntime.Peer
			for _, peer := range c.Peers {
				if peer.DHCPRelay == "" {
					fabricPeers = append(fabricPeers, peer)
				}
			}
			c.Peers = fabricPeers
			c.Prefixes = c.Prefixes[:1]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := testCell(test.hostname)
			if test.mutate != nil {
				test.mutate(cfg)
			}

			got, err := NewScriptRuntime().renderCell(cfg)
			if err != nil {
				t.Fatalf("rendering cell: %v", err)
			}

			if strings.Contains(string(got), "{{") {
				t.Error("rendered script still contains a template action")
			}
			compareGolden(t, test.name+".golden.sh", got)
		})
	}
}

func TestRenderReset(t *testing.T) {
	got, err := NewScriptRuntime().renderReset("swi1-ab-42")
	if err != nil {
		t.Fatalf("rendering reset: %v", err)
	}
	if strings.Contains(string(got), "{{") {
		t.Error("rendered script still contains a template action")
	}
	compareGolden(t, "reset.golden.sh", got)
}

func TestRenderCellInvalid(t *testing.T) {
	rt := NewScriptRuntime()

	for name, mutate := range map[string]func(*cellruntime.CellConfig){
		"non-numeric ID":       func(c *cellruntime.CellConfig) { c.ID = "abc" },
		"missing hostname":     func(c *cellruntime.CellConfig) { c.Hostname = "" },
		"missing loopback IPs": func(c *cellruntime.CellConfig) { c.LoopbackIPs = nil },
		"missing prefixes":     func(c *cellruntime.CellConfig) { c.Prefixes = nil },
		"no peers":             func(c *cellruntime.CellConfig) { c.Peers = nil },
		"missing role":         func(c *cellruntime.CellConfig) { delete(c.Metadata.Labels, CellRoleLabel) },
		"unknown role":         func(c *cellruntime.CellConfig) { c.Metadata.Labels[CellRoleLabel] = "tor" },
		"spine with server peers": func(c *cellruntime.CellConfig) {
			c.Metadata.Labels[CellRoleLabel] = RoleSpine
		},
		"peer without interface": func(c *cellruntime.CellConfig) {
			c.Peers[0].Interface = nil
		},
		"peer with invalid DHCP relay": func(c *cellruntime.CellConfig) {
			c.Peers[0].DHCPRelay = "not-an-ip"
		},
		"prefix count mismatch": func(c *cellruntime.CellConfig) { c.Prefixes = c.Prefixes[:2] },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testCell("swi1-ab-42.fabric.example.com")
			mutate(cfg)
			if _, err := rt.renderCell(cfg); !errors.Is(err, cellruntime.TerminalError(nil)) {
				t.Errorf("expected terminal error, got %v", err)
			}
		})
	}
}

// backdateFlag creates the done-flag file with its mtime flagAge in the past.
func backdateFlag(t *testing.T, path string, flagAge time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-flagAge)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestCellStatus(t *testing.T) {
	ctx := context.Background()
	const node = "swi1"
	const vlan1, vlan2 = "Vlan1001", "Vlan1002"

	for _, tc := range []struct {
		name        string
		flagAge     time.Duration // 0 = no flag
		bootAge     time.Duration
		configured  int
		established int
		stateVLANs  []string
		vlanUp      map[string]bool
		wantPhase   cellruntime.CellPhase
		wantErr     bool
	}{
		{
			name:    "no done flag",
			wantErr: true,
		},
		{
			name:      "flag present, not yet rebooted",
			flagAge:   5 * time.Minute, // flag written 5m ago (after boot)
			bootAge:   1 * time.Hour,   // device booted 1h ago → flag newer than boot
			wantPhase: cellruntime.CellPhaseCreated,
		},
		{
			name:        "rebooted, BGP not established",
			flagAge:     2 * time.Hour,
			bootAge:     1 * time.Hour,
			configured:  2,
			established: 1,
			wantPhase:   cellruntime.CellPhaseCreated,
		},
		{
			name:        "rebooted, BGP up, no VLANs in state file",
			flagAge:     2 * time.Hour,
			bootAge:     1 * time.Hour,
			configured:  2,
			established: 2,
			wantPhase:   cellruntime.CellPhaseActive,
		},
		{
			name:        "rebooted, BGP up, VLAN down",
			flagAge:     2 * time.Hour,
			bootAge:     1 * time.Hour,
			configured:  2,
			established: 2,
			stateVLANs:  []string{vlan1},
			vlanUp:      map[string]bool{vlan1: false},
			wantPhase:   cellruntime.CellPhaseCreated,
		},
		{
			name:        "all checks pass",
			flagAge:     2 * time.Hour,
			bootAge:     1 * time.Hour,
			configured:  2,
			established: 2,
			stateVLANs:  []string{vlan1, vlan2},
			vlanUp:      map[string]bool{vlan1: true, vlan2: true},
			wantPhase:   cellruntime.CellPhaseActive,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			r := &ScriptRuntime{
				doneFlagPath:  filepath.Join(dir, "fabric-cell.done"),
				stateFilePath: filepath.Join(dir, "fabric-cell.state"),
				uptime:        func() (time.Duration, error) { return tc.bootAge, nil },
				bgpEstablished: func(context.Context) (int, int, error) {
					return tc.configured, tc.established, nil
				},
				ifaceUp: func(name string) (bool, error) { return tc.vlanUp[name], nil },
			}
			if tc.flagAge > 0 {
				backdateFlag(t, r.doneFlagPath, tc.flagAge)
			}
			if len(tc.stateVLANs) > 0 {
				if err := os.WriteFile(r.stateFilePath, []byte(strings.Join(tc.stateVLANs, "\n")), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			status, err := r.CellStatus(ctx, node)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got status %v", status)
				}
				if !errors.Is(err, cellruntime.ErrNotFound) {
					t.Errorf("expected ErrNotFound, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", status.Phase, tc.wantPhase)
			}
		})
	}
}
