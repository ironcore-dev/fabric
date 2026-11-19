// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/ironcore-dev/fabric/cellruntime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ProviderName = "sonic"
	doneFlagFile = "/etc/sonic/fabric-cell.done"
	stateFile    = "/etc/sonic/fabric-cell.state"
	scriptDir    = "/etc/sonic"
)

// CellRoleLabel labels a cell with the role the switch gets provisioned
// with (fabric.ironcore.dev/role).
const CellRoleLabel = "fabric.ironcore.dev/role"

// Cell roles a cell can be labeled with.
const (
	RoleLeaf  = "leaf"
	RoleSpine = "spine"
)

//go:embed templates
var templates embed.FS

type ScriptRuntime struct {
	// probe functions; swapped by tests
	uptime         func() (time.Duration, error)
	bgpEstablished func(ctx context.Context) (configured, established int, err error)
	ifaceUp        func(name string) (bool, error)
	// path overrides for tests
	doneFlagPath  string
	stateFilePath string
}

var _ cellruntime.Runtime = (*ScriptRuntime)(nil)

func NewScriptRuntime() *ScriptRuntime {
	r := &ScriptRuntime{}
	r.uptime = readUptime
	r.bgpEstablished = bgpEstablishedFromVtysh
	r.ifaceUp = ifaceUpFromSysfs
	r.doneFlagPath = doneFlagFile
	r.stateFilePath = stateFile
	return r
}

func (r *ScriptRuntime) ProviderName() string {
	return ProviderName
}

func (r *ScriptRuntime) NodeID(ctx context.Context, node string) (string, error) {
	return node, nil
}

func (r *ScriptRuntime) ApplyCell(ctx context.Context, node string, cfg *cellruntime.CellConfig) error {
	script, err := r.renderCell(cfg)
	if err != nil {
		return err
	}
	if err := runScript(ctx, script); err != nil {
		return err
	}
	// Collect VLANs from the rendered south ports so CellStatus can check them
	// without re-deriving them from the API.
	var vlans []string
	for _, peer := range cfg.Peers {
		if peer.DHCPRelay == "" || peer.Interface == nil {
			continue
		}
		portNum, err := strconv.Atoi(strings.TrimPrefix(peer.Interface.ID, "Ethernet"))
		if err != nil {
			continue
		}
		vlans = append(vlans, fmt.Sprintf("Vlan%d", portNum/4+1001))
	}
	return os.WriteFile(r.stateFilePath, []byte(strings.Join(vlans, "\n")), 0o644)
}

func (r *ScriptRuntime) DeleteCell(ctx context.Context, node string) error {
	if !fileExists(r.doneFlagPath) {
		return fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	}
	script, err := r.renderReset(node)
	if err != nil {
		return err
	}
	if err := runScript(ctx, script); err != nil {
		return err
	}
	_ = os.Remove(r.stateFilePath)
	return nil
}

type southPort struct {
	Port  string
	VLAN  int
	IP    string
	Relay string
}

type switchTemplateData struct {
	Hostname    string
	ASN         int64
	RouterID    string
	LoopbackIPs []string
	Summary     string
	AllPorts    []string
	SouthPorts  []southPort
	FabricPorts []string
	IsLeaf      bool
}

func (r *ScriptRuntime) renderCell(cfg *cellruntime.CellConfig) ([]byte, error) {
	id, err := strconv.Atoi(cfg.ID)
	if err != nil {
		return nil, cellruntime.TerminalError(fmt.Errorf("cell ID %q must be numeric: %w", cfg.ID, err))
	}
	cell := cfg.Metadata.Namespace + "/" + cfg.Metadata.Name
	switch {
	case cfg.Hostname == "":
		return nil, terminalCellErrorf(cell, "must set a hostname")
	case len(cfg.LoopbackIPs) == 0:
		return nil, terminalCellErrorf(cell, "must set at least one loopback IP")
	case len(cfg.Prefixes) == 0:
		return nil, terminalCellErrorf(cell, "must set at least one prefix")
	case len(cfg.Peers) == 0:
		return nil, terminalCellErrorf(cell, "must set at least one peer")
	}

	// The cell's role label picks ASN base and router ID base.
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
		return nil, terminalCellErrorf(cell, "must be labeled %s %s or %s, got %q",
			CellRoleLabel, RoleLeaf, RoleSpine, role)
	}

	// Peers with a DHCP relay are south-facing server ports, peers without
	// one are fabric-facing routed ports.
	var southPeers, fabricPeers []cellruntime.Peer
	for _, peer := range cfg.Peers {
		if peer.Interface == nil {
			return nil, terminalCellErrorf(cell, "has a peer without interface")
		}
		if peer.DHCPRelay == "" {
			fabricPeers = append(fabricPeers, peer)
			continue
		}
		if _, err := netip.ParseAddr(peer.DHCPRelay); err != nil {
			return nil, terminalCellErrorf(cell, "has invalid DHCP relay %q", peer.DHCPRelay)
		}
		southPeers = append(southPeers, peer)
	}
	if role == RoleSpine && len(southPeers) > 0 {
		return nil, terminalCellErrorf(cell, "has role %s but peers with a DHCP relay", RoleSpine)
	}

	// The first prefix is the summary that gets advertised and rejected,
	// prefixes[1+i] is the interface prefix of the i-th south peer.
	if len(cfg.Prefixes) != len(southPeers)+1 {
		return nil, terminalCellErrorf(cell, "must set %d prefixes, got %d", len(southPeers)+1, len(cfg.Prefixes))
	}

	data := &switchTemplateData{
		Hostname: cfg.Hostname,
		ASN:      asnBase + int64(id),
		RouterID: fmt.Sprintf("%s.%d.%d", routerIDBase, id/256, id%256),
		Summary:  cfg.Prefixes[0].String(),
		IsLeaf:   isLeaf,
	}
	for _, ip := range cfg.LoopbackIPs {
		data.LoopbackIPs = append(data.LoopbackIPs, ip.String())
	}
	for i, peer := range southPeers {
		portNum, err := strconv.Atoi(strings.TrimPrefix(peer.Interface.ID, "Ethernet"))
		if err != nil {
			return nil, terminalCellErrorf(cell, "has invalid Ethernet port ID %q", peer.Interface.ID)
		}
		data.SouthPorts = append(data.SouthPorts, southPort{
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

	return render("switch.sh", data)
}

func (r *ScriptRuntime) renderReset(node string) ([]byte, error) {
	return render("reset.sh", struct{ Node string }{Node: node})
}

func (r *ScriptRuntime) CellStatus(ctx context.Context, node string) (*cellruntime.CellStatus, error) {
	if !fileExists(r.doneFlagPath) {
		return nil, fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	}

	// Check if device has rebooted since the done flag was written.
	flagInfo, err := os.Stat(r.doneFlagPath)
	if err != nil {
		return nil, fmt.Errorf("stat done flag: %w", err)
	}
	uptime, err := r.uptime()
	if err != nil {
		return nil, fmt.Errorf("reading uptime: %w", err)
	}
	bootTime := time.Now().Add(-uptime)
	if flagInfo.ModTime().After(bootTime) {
		// Flag is newer than boot — device hasn't rebooted yet after provisioning.
		return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseCreated}, nil
	}

	// Check BGP sessions.
	configured, established, err := r.bgpEstablished(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking BGP: %w", err)
	}
	if configured > 0 && established < configured {
		return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseCreated}, nil
	}

	// Check VLAN interfaces from the state file.
	vlans, err := readStateFile(r.stateFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	for _, vlan := range vlans {
		up, err := r.ifaceUp(vlan)
		if err != nil || !up {
			return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseCreated}, nil
		}
	}

	return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseActive}, nil
}

func (r *ScriptRuntime) InterfaceID(ctx context.Context, iface string) (string, error) {
	return iface, nil
}

func (r *ScriptRuntime) InterfaceState(ctx context.Context, iface string) (*cellruntime.InterfaceState, error) {
	operstate, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "operstate"))
	if err != nil {
		return nil, fmt.Errorf("reading operstate of interface %s: %w", iface, err)
	}
	return &cellruntime.InterfaceState{
		Up: strings.TrimSpace(string(operstate)) == "up",
	}, nil
}

func (r *ScriptRuntime) SetInterfaceAdminState(ctx context.Context, iface string, adminState bool) error {
	return nil
}

func terminalCellErrorf(cell, format string, args ...any) error {
	return cellruntime.TerminalError(fmt.Errorf("cell "+cell+": "+format, args...))
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

func render(name string, data any) ([]byte, error) {
	tmpl, err := template.ParseFS(templates, "templates/"+name)
	if err != nil {
		return nil, cellruntime.TerminalError(fmt.Errorf("parsing cell template: %w", err))
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, cellruntime.TerminalError(fmt.Errorf("rendering cell template: %w", err))
	}
	return buf.Bytes(), nil
}

func readUptime() (time.Duration, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("unexpected /proc/uptime format")
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing /proc/uptime: %w", err)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

func bgpEstablishedFromVtysh(ctx context.Context) (configured, established int, error error) {
	out, err := exec.CommandContext(ctx, "vtysh", "-c", "show bgp neighbors json").Output()
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
	if os.IsNotExist(err) {
		return nil, nil
	}
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

func runScript(ctx context.Context, script []byte) error {
	f, err := os.CreateTemp(scriptDir, "fabric-cell-*.sh")
	if err != nil {
		return fmt.Errorf("creating cell script file: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	if _, err := f.Write(script); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing cell script %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing cell script %s: %w", f.Name(), err)
	}

	out, err := exec.CommandContext(ctx, "bash", f.Name()).CombinedOutput()
	if err != nil {
		return cellruntime.TerminalError(fmt.Errorf("running cell script: %w: %s", err, strings.TrimSpace(string(out))))
	}

	log.FromContext(ctx).V(1).Info("Cell script completed", "output", strings.TrimSpace(string(out)))
	return nil
}
