// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/ironcore-dev/fabric/cellruntime"
	"github.com/ironcore-dev/fabric/sonic/sonicpb"
	"google.golang.org/grpc"
)

type fakeSwitchClient struct {
	sonicpb.WireSonicSwitchServiceClient
	captured *sonicpb.ApplySwitchRequest
}

func (f *fakeSwitchClient) ApplySwitch(_ context.Context, req *sonicpb.ApplySwitchRequest, _ ...grpc.CallOption) (*sonicpb.ApplySwitchResponse, error) {
	f.captured = req
	return &sonicpb.ApplySwitchResponse{}, nil
}

func ethInterface(id string) *cellruntime.Interface {
	return &cellruntime.Interface{ID: id}
}

func ethName(port int) string {
	return fmt.Sprintf("Ethernet%d", port)
}

func TestApplyCellLeaf(t *testing.T) {
	fake := &fakeSwitchClient{}
	rt := &Runtime{client: fake}

	// 27 access peers (Ethernet0..Ethernet104, step 4) + 5 uplink peers (Ethernet108..Ethernet124)
	peers := make([]cellruntime.Peer, 0, 32)
	for i := 0; i < 27; i++ {
		peers = append(peers, cellruntime.Peer{Interface: ethInterface(ethName(i * 4)), DHCPRelay: "2a10:afc0:e015:201::fd"})
	}
	uplinkIfaces := []string{"Ethernet108", "Ethernet112", "Ethernet116", "Ethernet120", "Ethernet124"}
	for _, id := range uplinkIfaces {
		peers = append(peers, cellruntime.Peer{Interface: ethInterface(id)})
	}

	cfg := &cellruntime.CellConfig{
		Metadata:    cellruntime.CellMetadata{Namespace: "default", Name: "leaf-1", UID: "test-uid"},
		ID:          "1",
		Hostname:    "swi1-test-1",
		LoopbackIPs: []netip.Addr{netip.MustParseAddr("2a10:afc0:e015:201::1")},
		Prefixes:    []netip.Prefix{netip.MustParsePrefix("2a10:afc0:e015:201::/64")},
		Peers:       peers,
	}

	if err := rt.ApplyCell(context.Background(), "leaf-1", cfg); err != nil {
		t.Fatalf("ApplyCell: %v", err)
	}

	got := fake.captured
	if got == nil {
		t.Fatal("no request captured")
	}

	c := got.Config
	assertField(t, "device", got.Device, "leaf-1")
	assertField(t, "hostname", c.Hostname, "swi1-test-1")
	assertSlice(t, "loopbackIps", c.LoopbackIps, []string{"2a10:afc0:e015:201::1/128"})
	assertSlice(t, "prefixes", c.Prefixes, []string{"2a10:afc0:e015:201::/64"})

	if len(c.Vlans) != 28 {
		t.Fatalf("vlan count: got %d, want 28", len(c.Vlans))
	}
	for i := 0; i < 27; i++ {
		v := c.Vlans[i]
		wantID := int32(1001 + i)
		assertField(t, fmt.Sprintf("vlan[%d].id", i), v.Id, wantID)
		assertField(t, fmt.Sprintf("vlan[%d].dhcpRelay", i), v.DhcpRelay, "2a10:afc0:e015:201::fd")
		if len(v.Members) != 1 {
			t.Errorf("vlan[%d].members count: got %d, want 1", i, len(v.Members))
		} else {
			assertField(t, fmt.Sprintf("vlan[%d].members[0]", i), v.Members[0].InterfaceId, ethName(i*4))
		}
	}
	uplink := c.Vlans[27]
	assertField(t, "uplink vlan id", uplink.Id, int32(0))
	assertField(t, "uplink vlan dhcpRelay", uplink.DhcpRelay, "")
	if len(uplink.Members) != 5 {
		t.Fatalf("uplink vlan members count: got %d, want 5", len(uplink.Members))
	}
	for i, id := range uplinkIfaces {
		assertField(t, fmt.Sprintf("uplink.members[%d]", i), uplink.Members[i].InterfaceId, id)
	}

	assertField(t, "bgp.asn", c.Bgp.Asn, uint32(4211000001))
	assertField(t, "bgp.routerId", c.Bgp.RouterId, "1.0.0.1")
}

func TestApplyCellSpine(t *testing.T) {
	fake := &fakeSwitchClient{}
	rt := &Runtime{client: fake}

	// 32 uplink peers (Ethernet0..Ethernet124, step 4), no DHCPRelay
	peers := make([]cellruntime.Peer, 0, 32)
	for i := 0; i < 32; i++ {
		peers = append(peers, cellruntime.Peer{Interface: ethInterface(ethName(i * 4))})
	}

	cfg := &cellruntime.CellConfig{
		Metadata:    cellruntime.CellMetadata{Namespace: "default", Name: "spine-1", UID: "test-uid"},
		ID:          "1",
		Hostname:    "swi2-wdf4e-1",
		LoopbackIPs: []netip.Addr{netip.MustParseAddr("2a10:afc0:e015:101::1")},
		Prefixes:    []netip.Prefix{netip.MustParsePrefix("2a10:afc0:e015:101::/64")},
		Peers:       peers,
	}

	if err := rt.ApplyCell(context.Background(), "spine-1", cfg); err != nil {
		t.Fatalf("ApplyCell: %v", err)
	}

	got := fake.captured
	if got == nil {
		t.Fatal("no request captured")
	}

	c := got.Config
	assertField(t, "device", got.Device, "spine-1")
	assertField(t, "hostname", c.Hostname, "swi2-wdf4e-1")
	assertSlice(t, "loopbackIps", c.LoopbackIps, []string{"2a10:afc0:e015:101::1/128"})
	assertSlice(t, "prefixes", c.Prefixes, []string{"2a10:afc0:e015:101::/64"})

	if len(c.Vlans) != 1 {
		t.Fatalf("vlan count: got %d, want 1", len(c.Vlans))
	}
	uplink := c.Vlans[0]
	assertField(t, "vlan[0].id", uplink.Id, int32(0))
	assertField(t, "vlan[0].dhcpRelay", uplink.DhcpRelay, "")
	if len(uplink.Members) != 32 {
		t.Fatalf("uplink members count: got %d, want 32", len(uplink.Members))
	}
	for i := 0; i < 32; i++ {
		assertField(t, fmt.Sprintf("uplink.members[%d]", i), uplink.Members[i].InterfaceId, ethName(i*4))
	}

	assertField(t, "bgp.asn", c.Bgp.Asn, uint32(4212000001))
	assertField(t, "bgp.routerId", c.Bgp.RouterId, "2.0.0.1")
}

func assertField[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

func assertSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}
