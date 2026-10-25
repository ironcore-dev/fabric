// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package configdb

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type commandRecorder struct {
	commands []string
}

func (r *commandRecorder) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (r *commandRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.commands = append(r.commands, fmt.Sprint(cmd.Args()))
		return next(ctx, cmd)
	}
}

func (r *commandRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			r.commands = append(r.commands, fmt.Sprint(cmd.Args()))
		}
		return next(ctx, cmds)
	}
}

func commandIndex(commands []string, fragment string) int {
	for i, command := range commands {
		if strings.Contains(command, fragment) {
			return i
		}
	}
	return -1
}

const (
	testNull              = "NULL"
	testAdminStatus       = "admin_status"
	testAdminDown         = "down"
	testFECField          = "fec"
	testMTUField          = "mtu"
	testPort0             = "Ethernet0"
	testVLANIDField       = "vlanid"
	testDHCPRelayField    = "dhcp_servers@"
	testTaggingModeField  = "tagging_mode"
	testSpeedField        = "speed"
	testUntagged          = "untagged"
	testDHCPRelay         = "2001:db8::53"
	testLoopbackBase      = "LOOPBACK_INTERFACE|Loopback0"
	testLoopbackAddress   = "LOOPBACK_INTERFACE|Loopback0|2001:db8::1/128"
	testVLAN              = "VLAN|Vlan1001"
	testVLANInterface     = "VLAN_INTERFACE|Vlan1001"
	testVLANAddress       = "VLAN_INTERFACE|Vlan1001|2001:db8:1::/80"
	testVLANMember        = "VLAN_MEMBER|Vlan1001|Ethernet0"
	testEthernetInterface = "INTERFACE|Ethernet108"
	testLinkLocalEnabled  = "enable"
)

func newTestDB(t *testing.T) (context.Context, DB, *redis.Client, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	db := New(mr.Addr())
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing ConfigDB client: %v", err)
		}
	})
	config := redis.NewClient(&redis.Options{Addr: mr.Addr(), DB: configDBIndex})
	appl := redis.NewClient(&redis.Options{Addr: mr.Addr(), DB: applDBIndex})
	t.Cleanup(func() {
		_ = config.Close()
		_ = appl.Close()
	})
	return context.Background(), db, config, appl
}

func TestSetHostnameWritesDeviceMetadata(t *testing.T) {
	ctx, db, config, _ := newTestDB(t)
	if err := db.SetHostname(ctx, "leaf-1"); err != nil {
		t.Fatalf("setting hostname: %v", err)
	}
	got, err := config.HGet(ctx, "DEVICE_METADATA|localhost", "hostname").Result()
	if err != nil {
		t.Fatalf("reading hostname: %v", err)
	}
	if got != "leaf-1" {
		t.Fatalf("hostname = %q, want leaf-1", got)
	}
}

func TestEnsureLoopbackSynchronizesPrefixKeys(t *testing.T) {
	ctx, db, config, _ := newTestDB(t)
	if err := config.HSet(ctx, "LOOPBACK_INTERFACE|Loopback0|192.0.2.1/32", "NULL", "NULL").Err(); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureLoopback(ctx, "Loopback0", []string{"2001:db8::1/128"}); err != nil {
		t.Fatalf("ensuring loopback: %v", err)
	}

	for key, want := range map[string]bool{
		testLoopbackBase:                            true,
		testLoopbackAddress:                         true,
		"LOOPBACK_INTERFACE|Loopback0|192.0.2.1/32": false,
	} {
		got, err := config.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("checking %s: %v", key, err)
		}
		if (got > 0) != want {
			t.Errorf("key %s exists = %t, want %t", key, got > 0, want)
		}
	}
}

func TestEnsurePortWritesPhysicalConfiguration(t *testing.T) {
	ctx, db, config, _ := newTestDB(t)
	if err := db.EnsurePort(ctx, "Ethernet4", PortConfig{MTU: 9100, FEC: "rs", Speed: 100000, AdminUp: true}); err != nil {
		t.Fatalf("ensuring port: %v", err)
	}
	want := map[string]string{
		testMTUField: "9100", testFECField: "rs", testSpeedField: "100000", testAdminStatus: "up",
	}
	got, err := config.HGetAll(ctx, "PORT|Ethernet4").Result()
	if err != nil {
		t.Fatalf("reading port: %v", err)
	}
	for field, value := range want {
		if got[field] != value {
			t.Errorf("PORT|Ethernet4 %s = %q, want %q", field, got[field], value)
		}
	}
}

func TestEnsureVLANWritesSONiCKeys(t *testing.T) {
	ctx, db, config, _ := newTestDB(t)
	if err := db.EnsureVLAN(ctx, VLANConfig{
		ID:        1001,
		Prefix:    "2001:db8:1::/80",
		Port:      testPort0,
		DHCPRelay: testDHCPRelay,
	}); err != nil {
		t.Fatalf("ensuring VLAN: %v", err)
	}

	checks := []struct {
		key, field, want string
	}{
		{testVLAN, testVLANIDField, "1001"},
		{testVLAN, testDHCPRelayField, testDHCPRelay},
		{testVLANInterface, testNull, testNull},
		{testVLANAddress, testNull, testNull},
		{testVLANMember, testTaggingModeField, testUntagged},
	}
	for _, check := range checks {
		got, err := config.HGet(ctx, check.key, check.field).Result()
		if err != nil {
			t.Errorf("reading %s %s: %v", check.key, check.field, err)
			continue
		}
		if got != check.want {
			t.Errorf("%s %s = %q, want %q", check.key, check.field, got, check.want)
		}
	}
}

func TestEnsureVLANWritesDependenciesBeforeRelay(t *testing.T) {
	ctx, db, _, _ := newTestDB(t)
	recorder := &commandRecorder{}
	db.(*Client).config.AddHook(recorder)

	if err := db.EnsureVLAN(ctx, VLANConfig{
		ID:        1001,
		Prefix:    "2001:db8:1::/80",
		Port:      testPort0,
		DHCPRelay: testDHCPRelay,
	}); err != nil {
		t.Fatalf("ensuring VLAN: %v", err)
	}

	fragments := []string{
		"[hset VLAN|Vlan1001 vlanid 1001]",
		"[hset VLAN_MEMBER|Vlan1001|Ethernet0 tagging_mode untagged]",
		"[hset VLAN_INTERFACE|Vlan1001 NULL NULL]",
		"[hset VLAN_INTERFACE|Vlan1001|2001:db8:1::/80 NULL NULL]",
		"[hset VLAN|Vlan1001 dhcp_servers@ 2001:db8::53]",
	}
	previous := -1
	for _, fragment := range fragments {
		index := commandIndex(recorder.commands, fragment)
		if index <= previous {
			t.Fatalf("command %q index = %d after %d; commands: %v", fragment, index, previous, recorder.commands)
		}
		previous = index
	}
}

func TestEnableLinkLocalUsesInterfaceSpecificTable(t *testing.T) {
	ctx, db, config, _ := newTestDB(t)
	for _, name := range []string{"Ethernet108", "Vlan1001"} {
		if err := db.EnableLinkLocal(ctx, name); err != nil {
			t.Fatalf("enabling link-local on %s: %v", name, err)
		}
	}
	for _, key := range []string{testEthernetInterface, testVLANInterface} {
		got, err := config.HGet(ctx, key, "ipv6_use_link_local_only").Result()
		if err != nil {
			t.Fatalf("reading %s: %v", key, err)
		}
		if got != testLinkLocalEnabled {
			t.Errorf("%s ipv6_use_link_local_only = %q, want enable", key, got)
		}
	}
}

func TestInterfaceStateUsesApplDBAndAdminStateUsesConfigDB(t *testing.T) {
	ctx, db, config, appl := newTestDB(t)
	if err := appl.HSet(ctx, "PORT_TABLE:Ethernet112", "oper_status", "up").Err(); err != nil {
		t.Fatal(err)
	}
	up, err := db.InterfaceOperUp(ctx, "Ethernet112")
	if err != nil {
		t.Fatalf("reading operational state: %v", err)
	}
	if !up {
		t.Error("operational state = down, want up")
	}

	if err := db.SetInterfaceAdminState(ctx, "Ethernet112", false); err != nil {
		t.Fatalf("setting admin state: %v", err)
	}
	got, err := config.HGet(ctx, "PORT|Ethernet112", testAdminStatus).Result()
	if err != nil {
		t.Fatalf("reading admin state: %v", err)
	}
	if got != testAdminDown {
		t.Errorf("admin_status = %q, want down", got)
	}
}

func TestInterfaceOperationsRejectUnknownNames(t *testing.T) {
	ctx, db, _, _ := newTestDB(t)
	if err := db.EnableLinkLocal(ctx, "eth0"); err == nil {
		t.Error("EnableLinkLocal accepted an unknown interface name")
	}
	if _, err := db.InterfaceOperUp(ctx, "eth0"); err == nil {
		t.Error("InterfaceOperUp accepted an unknown interface name")
	}
	if err := db.SetInterfaceAdminState(ctx, "eth0", true); err == nil {
		t.Error("SetInterfaceAdminState accepted an unknown interface name")
	}
}

func TestResetRemovesCellConfigurationAndRetainsPhysicalSettings(t *testing.T) {
	ctx, db, config, _ := newTestDB(t)
	seed := map[string]map[string]string{
		"DEVICE_METADATA|localhost":        {"hostname": "leaf-1"},
		"MGMT_INTERFACE|eth0|192.0.2.2/24": {"gwaddr": "192.0.2.1"},
		testLoopbackBase:                   {testNull: testNull},
		testLoopbackAddress:                {testNull: testNull},
		testVLAN:                           {testVLANIDField: "1001", testDHCPRelayField: testDHCPRelay},
		testVLANInterface:                  {testNull: testNull, "ipv6_use_link_local_only": testLinkLocalEnabled},
		testVLANAddress:                    {testNull: testNull},
		testVLANMember:                     {testTaggingModeField: testUntagged},
		"PORT|" + testPort0: {
			testAdminStatus: "up",
			testMTUField:    "9100",
			testFECField:    "rs",
			testSpeedField:  "100000",
		},
		"PORT|Ethernet4": {
			testAdminStatus: testAdminDown,
			testMTUField:    "9100",
			testFECField:    "rs",
			testSpeedField:  "100000",
		},
		testEthernetInterface: {"ipv6_use_link_local_only": testLinkLocalEnabled},
	}
	for key, fields := range seed {
		values := make([]any, 0, len(fields)*2)
		for field, value := range fields {
			values = append(values, field, value)
		}
		if err := config.HSet(ctx, key, values...).Err(); err != nil {
			t.Fatalf("seeding %s: %v", key, err)
		}
	}

	if err := db.Reset(ctx); err != nil {
		t.Fatalf("resetting ConfigDB: %v", err)
	}

	for _, key := range []string{
		testLoopbackAddress,
		testVLAN,
		testVLANInterface,
		testVLANAddress,
		testVLANMember,
	} {
		if exists, err := config.Exists(ctx, key).Result(); err != nil || exists != 0 {
			t.Errorf("removed key %s exists = %d, err = %v", key, exists, err)
		}
	}
	for _, key := range []string{
		"DEVICE_METADATA|localhost",
		"MGMT_INTERFACE|eth0|192.0.2.2/24",
		testLoopbackBase,
		testEthernetInterface,
	} {
		if exists, err := config.Exists(ctx, key).Result(); err != nil || exists != 1 {
			t.Errorf("retained key %s exists = %d, err = %v", key, exists, err)
		}
	}
	for _, port := range []string{"Ethernet0", "Ethernet4"} {
		fields, err := config.HGetAll(ctx, "PORT|"+port).Result()
		if err != nil {
			t.Fatalf("reading %s: %v", port, err)
		}
		if fields[testAdminStatus] != testAdminDown {
			t.Errorf("%s admin_status = %q, want down", port, fields[testAdminStatus])
		}
		for field, want := range map[string]string{
			testMTUField: "9100", testFECField: "rs", testSpeedField: "100000",
		} {
			if fields[field] != want {
				t.Errorf("%s %s = %q, want %q", port, field, fields[field], want)
			}
		}
	}
}

func TestResetRemovesRelayBeforeVLANDependencies(t *testing.T) {
	ctx, db, config, _ := newTestDB(t)
	for key, fields := range map[string]map[string]string{
		testVLAN:          {testVLANIDField: "1001", testDHCPRelayField: testDHCPRelay},
		testVLANInterface: {testNull: testNull},
		testVLANMember:    {testTaggingModeField: "untagged"},
	} {
		values := make([]any, 0, len(fields)*2)
		for field, value := range fields {
			values = append(values, field, value)
		}
		if err := config.HSet(ctx, key, values...).Err(); err != nil {
			t.Fatalf("seeding %s: %v", key, err)
		}
	}
	recorder := &commandRecorder{}
	db.(*Client).config.AddHook(recorder)

	if err := db.Reset(ctx); err != nil {
		t.Fatalf("resetting ConfigDB: %v", err)
	}

	relay := commandIndex(recorder.commands, "[hdel VLAN|Vlan1001 dhcp_servers@]")
	fragments := []string{
		"[del VLAN_INTERFACE|Vlan1001]",
		"[del VLAN_MEMBER|Vlan1001|Ethernet0]",
		"[del VLAN|Vlan1001]",
	}
	for _, fragment := range fragments {
		dependency := commandIndex(recorder.commands, fragment)
		if relay < 0 || dependency <= relay {
			t.Fatalf("relay index = %d, dependency %q index = %d; commands: %v", relay, fragment, dependency, recorder.commands)
		}
	}
}
