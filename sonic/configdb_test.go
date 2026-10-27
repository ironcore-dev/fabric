// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ironcore-dev/fabric/cellruntime"
	sonicconfigdb "github.com/ironcore-dev/fabric/sonic/configdb"
	"github.com/redis/go-redis/v9"
)

const (
	testDHCPRelayAddress = "fd00:1234:5678:3201::1:547"
	testLeafHostname     = "swi1-ab-42.fabric.example.com"
	testFabricPort       = "Ethernet108"
	testFabricPort2      = "Ethernet112"
	testHostnameField    = "hostname"
	testPort0Key         = "PORT|Ethernet0"
	testLinkLocalValue   = "enable"
	testSaveCall         = "save"
	testDatabaseFailure  = "database"
	testNullValue        = "NULL"
	testEthernet4        = "Ethernet4"
	testAdminStatusField = "admin_status"
	testTrueValue        = "true"
)

type fakeHostOperations struct {
	applied  bool
	fail     string
	hostname string
	calls    []string
}

func (f *fakeHostOperations) record(name string) error {
	f.calls = append(f.calls, name)
	if f.fail == name {
		return errors.New("injected " + name + " failure")
	}
	return nil
}

func (f *fakeHostOperations) SaveConfig(context.Context) error { return f.record("save") }
func (f *fakeHostOperations) CellApplied(context.Context) (bool, error) {
	if err := f.record("cell-applied"); err != nil {
		return false, err
	}
	return f.applied, nil
}
func (f *fakeHostOperations) MarkCellApplied(context.Context) error {
	if err := f.record("mark-applied"); err != nil {
		return err
	}
	f.applied = true
	return nil
}
func (f *fakeHostOperations) ClearCellApplied(context.Context) error {
	if err := f.record("clear-applied"); err != nil {
		return err
	}
	f.applied = false
	return nil
}
func (f *fakeHostOperations) Reboot(context.Context) error { return f.record("reboot") }

type configDBRuntimeFixture struct {
	runtime *ConfigDBRuntime
	host    *fakeHostOperations
	server  *miniredis.Miniredis
	config  *redis.Client
	appl    *redis.Client
}

func newConfigDBRuntimeFixture(t *testing.T) *configDBRuntimeFixture {
	t.Helper()
	server := miniredis.RunT(t)
	db := sonicconfigdb.New(server.Addr())
	t.Cleanup(func() { _ = db.Close() })
	host := &fakeHostOperations{hostname: "leaf-42"}
	config := redis.NewClient(&redis.Options{Addr: server.Addr(), DB: 4})
	appl := redis.NewClient(&redis.Options{Addr: server.Addr(), DB: 0})
	t.Cleanup(func() {
		_ = config.Close()
		_ = appl.Close()
	})
	return &configDBRuntimeFixture{
		runtime: newConfigDBRuntime(db, host),
		host:    host,
		server:  server,
		config:  config,
		appl:    appl,
	}
}

func TestConfigDBRuntimeApplyLeaf(t *testing.T) {
	ctx := context.Background()
	fixture := newConfigDBRuntimeFixture(t)
	if err := fixture.runtime.ApplyCell(ctx, "leaf-42", testCell(testLeafHostname)); err != nil {
		t.Fatalf("applying leaf cell: %v", err)
	}

	checks := []struct{ key, field, want string }{
		{"DEVICE_METADATA|localhost", testHostnameField, testLeafHostname},
		{testPort0Key, "speed", "100000"},
		{testPort0Key, "mtu", "9100"},
		{testPort0Key, "fec", "rs"},
		{testPort0Key, testAdminStatusField, "up"},
		{"VLAN|Vlan1001", "dhcp_servers@", testDHCPRelayAddress},
		{"VLAN_MEMBER|Vlan1001|Ethernet0", "tagging_mode", "untagged"},
		{"INTERFACE|" + testFabricPort, "ipv6_use_link_local_only", testLinkLocalValue},
		{"VLAN_INTERFACE|Vlan1001", "ipv6_use_link_local_only", testLinkLocalValue},
	}
	for _, check := range checks {
		got, err := fixture.config.HGet(ctx, check.key, check.field).Result()
		if err != nil {
			t.Errorf("reading %s %s: %v", check.key, check.field, err)
			continue
		}
		if got != check.want {
			t.Errorf("%s %s = %q, want %q", check.key, check.field, got, check.want)
		}
	}
	loopbackExists, err := fixture.config.Exists(
		ctx,
		"LOOPBACK_INTERFACE|Loopback0|fd00:1234:5678:f2::/128",
	).Result()
	if err != nil || loopbackExists != 1 {
		t.Errorf("loopback prefix exists = %d, err = %v", loopbackExists, err)
	}

	wantCalls := []string{testSaveCall, "mark-applied", "reboot"}
	if len(fixture.host.calls) != len(wantCalls) {
		t.Fatalf("host calls = %v, want %v", fixture.host.calls, wantCalls)
	}
	for i := range wantCalls {
		if fixture.host.calls[i] != wantCalls[i] {
			t.Errorf("host call %d = %q, want %q", i, fixture.host.calls[i], wantCalls[i])
		}
	}
	if !fixture.host.applied {
		t.Error("cell marker was not written")
	}
}

func TestConfigDBRuntimeApplySpineDoesNotCreateVLANs(t *testing.T) {
	ctx := context.Background()
	fixture := newConfigDBRuntimeFixture(t)
	cfg := testCell("swi2-ab-7.fabric.example.com")
	cfg.Metadata.Labels[CellRoleLabel] = RoleSpine
	cfg.Peers = cfg.Peers[2:]
	cfg.Prefixes = cfg.Prefixes[:1]
	if err := fixture.runtime.ApplyCell(ctx, "spine-42", cfg); err != nil {
		t.Fatalf("applying spine cell: %v", err)
	}

	keys, err := fixture.config.Keys(ctx, "VLAN|*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("spine VLAN keys = %v, want none", keys)
	}
	for _, port := range []string{testFabricPort, testFabricPort2} {
		value, err := fixture.config.HGet(ctx, "INTERFACE|"+port, "ipv6_use_link_local_only").Result()
		if err != nil || value != testLinkLocalValue {
			t.Errorf("link-local %s = %q, err = %v", port, value, err)
		}
	}
}

func TestConfigDBRuntimeApplyPersistsBGPInConfigDB(t *testing.T) {
	ctx := context.Background()
	fixture := newConfigDBRuntimeFixture(t)
	if err := fixture.runtime.ApplyCell(ctx, "leaf-42", testCell(testLeafHostname)); err != nil {
		t.Fatalf("applying leaf cell: %v", err)
	}

	checks := []struct{ key, field, want string }{
		{"DEVICE_METADATA|localhost", "frr_mgmt_framework_config", testTrueValue},
		{"BGP_GLOBALS|default", "local_asn", "4211000042"},
		{"BGP_GLOBALS|default", "router_id", "1.0.0.42"},
		{"BGP_NEIGHBOR|default|" + testFabricPort, "peer_type", "external"},
		{"BGP_NEIGHBOR_AF|default|" + testFabricPort + "|ipv6_unicast", testAdminStatusField, testTrueValue},
		{"BGP_NEIGHBOR_AF|default|" + testFabricPort + "|ipv6_unicast", "route_map_in@", "RM_FABRIC_IN"},
		{"BGP_NEIGHBOR_AF|default|" + testFabricPort + "|ipv6_unicast", "route_map_out@", "RM_FABRIC_OUT"},
		{"BGP_NEIGHBOR|default|" + testFabricPort2, "peer_type", "external"},
		{"BGP_NEIGHBOR_AF|default|" + testFabricPort2 + "|ipv6_unicast", testAdminStatusField, testTrueValue},
		{"ROUTE_MAP|RM_FABRIC_IN|10", "route_operation", "permit"},
		{"ROUTE_MAP|RM_FABRIC_OUT|10", "route_operation", "permit"},
		{"BGP_GLOBALS_AF_NETWORK|default|ipv6_unicast|fd00:1234:5678:f2::/64", "backdoor", "false"},
	}
	for _, check := range checks {
		got, err := fixture.config.HGet(ctx, check.key, check.field).Result()
		if err != nil {
			t.Errorf("reading %s %s: %v", check.key, check.field, err)
			continue
		}
		if got != check.want {
			t.Errorf("%s %s = %q, want %q", check.key, check.field, got, check.want)
		}
	}
}

func TestConfigDBRuntimeRejectsInvalidFabricPort(t *testing.T) {
	fixture := newConfigDBRuntimeFixture(t)
	cfg := testCell(testLeafHostname)
	cfg.Peers[2].Interface.ID = "eth108"

	err := fixture.runtime.ApplyCell(context.Background(), "leaf-42", cfg)
	if !errors.Is(err, cellruntime.TerminalError(nil)) {
		t.Fatalf("ApplyCell error = %v, want terminal error", err)
	}
	if len(fixture.host.calls) != 0 {
		t.Errorf("host calls = %v, want none", fixture.host.calls)
	}
}

func TestConfigDBRuntimeApplyDoesNotMarkFailedApply(t *testing.T) {
	for _, failure := range []string{testDatabaseFailure, "save"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newConfigDBRuntimeFixture(t)
			if failure == testDatabaseFailure {
				fixture.server.SetError("ERR injected database failure")
			} else {
				fixture.host.fail = failure
			}
			if err := fixture.runtime.ApplyCell(context.Background(), "leaf-42", testCell("leaf-42")); err == nil {
				t.Fatal("ApplyCell succeeded, want error")
			}
			if fixture.host.applied {
				t.Error("failed apply wrote the done marker")
			}
		})
	}
}

func TestConfigDBRuntimeDeleteResetsCellConfiguration(t *testing.T) {
	ctx := context.Background()
	fixture := newConfigDBRuntimeFixture(t)
	fixture.host.applied = true
	for key, fields := range map[string]map[string]string{
		"LOOPBACK_INTERFACE|Loopback0":                 {testNullValue: testNullValue},
		"LOOPBACK_INTERFACE|Loopback0|2001:db8::1/128": {testNullValue: testNullValue},
		"VLAN|Vlan1001":                                {"vlanid": "1001"},
		"VLAN_INTERFACE|Vlan1001":                      {testNullValue: testNullValue},
		"VLAN_MEMBER|Vlan1001|Ethernet0":               {"tagging_mode": "untagged"},
		testPort0Key:                                   {testAdminStatusField: "up", "speed": "100000"},
	} {
		values := make([]any, 0, len(fields)*2)
		for field, value := range fields {
			values = append(values, field, value)
		}
		if err := fixture.config.HSet(ctx, key, values...).Err(); err != nil {
			t.Fatalf("seeding %s: %v", key, err)
		}
	}

	if err := fixture.runtime.DeleteCell(ctx, "leaf-42"); err != nil {
		t.Fatalf("deleting cell: %v", err)
	}

	for _, pattern := range []string{"LOOPBACK_INTERFACE|*|*", "VLAN|*", "VLAN_INTERFACE|*", "VLAN_MEMBER|*"} {
		keys, err := fixture.config.Keys(ctx, pattern).Result()
		if err != nil {
			t.Fatalf("listing %s: %v", pattern, err)
		}
		if len(keys) != 0 {
			t.Errorf("keys matching %s = %v, want none", pattern, keys)
		}
	}
	status, err := fixture.config.HGet(ctx, testPort0Key, testAdminStatusField).Result()
	if err != nil || status != "down" {
		t.Errorf("port admin status = %q, err = %v; want down", status, err)
	}
	if speed, err := fixture.config.HGet(ctx, testPort0Key, "speed").Result(); err != nil || speed != "100000" {
		t.Errorf("port speed = %q, err = %v; want retained 100000", speed, err)
	}
	wantCalls := []string{"cell-applied", testSaveCall, "clear-applied", "reboot"}
	if len(fixture.host.calls) != len(wantCalls) {
		t.Fatalf("host calls = %v, want %v", fixture.host.calls, wantCalls)
	}
	for i := range wantCalls {
		if fixture.host.calls[i] != wantCalls[i] {
			t.Errorf("host call %d = %q, want %q", i, fixture.host.calls[i], wantCalls[i])
		}
	}
	if fixture.host.applied {
		t.Error("cell marker was not cleared")
	}
}

func TestConfigDBRuntimeDeleteAbsentCell(t *testing.T) {
	fixture := newConfigDBRuntimeFixture(t)
	err := fixture.runtime.DeleteCell(context.Background(), "leaf-42")
	if !errors.Is(err, cellruntime.ErrNotFound) {
		t.Fatalf("DeleteCell error = %v, want ErrNotFound", err)
	}
	if len(fixture.host.calls) != 1 || fixture.host.calls[0] != "cell-applied" {
		t.Errorf("host calls = %v, want [cell-applied]", fixture.host.calls)
	}
}

func TestConfigDBRuntimeDeleteRetainsMarkerOnFailure(t *testing.T) {
	for _, failure := range []string{testDatabaseFailure, testSaveCall, "clear-applied"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newConfigDBRuntimeFixture(t)
			fixture.host.applied = true
			if failure == testDatabaseFailure {
				fixture.server.SetError("ERR injected database failure")
			} else {
				fixture.host.fail = failure
			}
			if err := fixture.runtime.DeleteCell(context.Background(), "leaf-42"); err == nil {
				t.Fatal("DeleteCell succeeded, want error")
			}
			if !fixture.host.applied {
				t.Error("failed deletion cleared the done marker")
			}
		})
	}
}

func TestConfigDBRuntimeStatusAndIdentity(t *testing.T) {
	ctx := context.Background()
	fixture := newConfigDBRuntimeFixture(t)
	if id, err := fixture.runtime.NodeID(ctx, "leaf-42"); err != nil || id != "leaf-42" {
		t.Errorf("NodeID = %q, %v", id, err)
	}
	if id, err := fixture.runtime.InterfaceID(ctx, testEthernet4); err != nil || id != testEthernet4 {
		t.Errorf("InterfaceID = %q, %v", id, err)
	}
	if _, err := fixture.runtime.CellStatus(ctx, "leaf-42"); !errors.Is(err, cellruntime.ErrNotFound) {
		t.Errorf("CellStatus absent error = %v, want ErrNotFound", err)
	}
	fixture.host.applied = true
	status, err := fixture.runtime.CellStatus(ctx, "leaf-42")
	if err != nil {
		t.Fatalf("CellStatus active: %v", err)
	}
	if status.Phase != cellruntime.CellPhaseActive {
		t.Errorf("CellStatus phase = %q, want Active", status.Phase)
	}
}

func TestConfigDBRuntimeInterfaceState(t *testing.T) {
	ctx := context.Background()
	fixture := newConfigDBRuntimeFixture(t)
	if err := fixture.appl.HSet(ctx, "PORT_TABLE:"+testEthernet4, "oper_status", "up").Err(); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.runtime.InterfaceState(ctx, testEthernet4)
	if err != nil {
		t.Fatalf("reading interface state: %v", err)
	}
	if !state.Up {
		t.Error("interface state = down, want up")
	}
	if err := fixture.runtime.SetInterfaceAdminState(ctx, testEthernet4, false); err != nil {
		t.Fatalf("setting admin state: %v", err)
	}
	value, err := fixture.config.HGet(ctx, "PORT|"+testEthernet4, testAdminStatusField).Result()
	if err != nil || value != "down" {
		t.Errorf("admin state = %q, err = %v; want down", value, err)
	}
}

func TestHostCommandEntersHostUTSNamespace(t *testing.T) {
	cmd := hostCommand(context.Background(), "hostname")
	want := "nsenter --target 1 --mount --net --pid --uts -- hostname"
	if got := strings.Join(cmd.Args, " "); got != want {
		t.Errorf("host command = %q, want %q", got, want)
	}
}
