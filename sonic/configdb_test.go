// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
	testRestartBGPCall   = "restart-bgp"
	testSaveCall         = "save"
	testWriteFRRCall     = "write-frr"
	testDatabaseFailure  = "database"
	testNullValue        = "NULL"
	testEthernet4        = "Ethernet4"
	wantEmptyFRR         = `frr version 8.1
frr defaults traditional
hostname leaf-42
log syslog informational
service integrated-vtysh-config
`
)

type fakeHostOperations struct {
	applied  bool
	fail     string
	hostname string
	calls    []string
	frr      []byte
}

func (f *fakeHostOperations) record(name string) error {
	f.calls = append(f.calls, name)
	if f.fail == name {
		return errors.New("injected " + name + " failure")
	}
	return nil
}

func (f *fakeHostOperations) WriteFRR(_ context.Context, data []byte) error {
	if err := f.record("write-frr"); err != nil {
		return err
	}
	f.frr = append([]byte(nil), data...)
	return nil
}

func (f *fakeHostOperations) SaveConfig(context.Context) error { return f.record("save") }
func (f *fakeHostOperations) RestartBGP(context.Context) error { return f.record("restart-bgp") }
func (f *fakeHostOperations) Hostname(context.Context) (string, error) {
	if err := f.record("hostname"); err != nil {
		return "", err
	}
	return f.hostname, nil
}
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
		{testPort0Key, "admin_status", "up"},
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

	wantFRR := goldenFRR(t, RoleLeaf)
	if !bytes.Equal(fixture.host.frr, wantFRR) {
		t.Errorf("FRR config differs from leaf golden output")
	}
	wantCalls := []string{testWriteFRRCall, testSaveCall, testRestartBGPCall, "mark-applied", "reboot"}
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
	if want := goldenFRR(t, RoleSpine); !bytes.Equal(fixture.host.frr, want) {
		t.Error("FRR config differs from spine golden output")
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
	for _, failure := range []string{testDatabaseFailure, "write-frr", "save", testRestartBGPCall} {
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
		testPort0Key:                                   {"admin_status": "up", "speed": "100000"},
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
	if status, err := fixture.config.HGet(ctx, testPort0Key, "admin_status").Result(); err != nil || status != "down" {
		t.Errorf("port admin status = %q, err = %v; want down", status, err)
	}
	if speed, err := fixture.config.HGet(ctx, testPort0Key, "speed").Result(); err != nil || speed != "100000" {
		t.Errorf("port speed = %q, err = %v; want retained 100000", speed, err)
	}
	wantFRR := []byte(wantEmptyFRR)
	if !bytes.Equal(fixture.host.frr, wantFRR) {
		t.Errorf("empty FRR config = %q, want %q", fixture.host.frr, wantFRR)
	}
	wantCalls := []string{
		"cell-applied", testSaveCall, testHostnameField, testWriteFRRCall, testRestartBGPCall, "clear-applied", "reboot",
	}
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
	for _, failure := range []string{
		testDatabaseFailure, testSaveCall, testHostnameField, testWriteFRRCall, testRestartBGPCall, "clear-applied",
	} {
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
	value, err := fixture.config.HGet(ctx, "PORT|"+testEthernet4, "admin_status").Result()
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

func goldenFRR(t *testing.T, name string) []byte {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("testdata", name+".golden.sh"))
	if err != nil {
		t.Fatalf("reading golden script: %v", err)
	}
	startMarker := []byte("cat <<EOF >/etc/sonic/frr/frr.conf\n")
	endMarker := []byte("EOF\n\n    # Restart BGP")
	start := bytes.Index(script, startMarker)
	end := bytes.Index(script, endMarker)
	if start < 0 || end < start {
		t.Fatal("golden script does not contain FRR config")
	}
	return script[start+len(startMarker) : end]
}
