// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ironcore-dev/fabric/cellruntime"
	sonicconfigdb "github.com/ironcore-dev/fabric/sonic/configdb"
)

type hostOperations interface {
	SaveConfig(ctx context.Context) error
	CellApplied(ctx context.Context) (bool, error)
	MarkCellApplied(ctx context.Context) error
	ClearCellApplied(ctx context.Context) error
	Reboot(ctx context.Context) error
}

// ConfigDBRuntime is a cellruntime.Runtime that writes directly to the SONiC CONFIG_DB.
type ConfigDBRuntime struct {
	db   sonicconfigdb.DB
	host hostOperations
	// probe functions; swapped by tests
	readBootID     func() (string, error)
	bgpEstablished func(ctx context.Context) (established map[string]bool, err error)
	ifaceUp        func(name string) (bool, error)
	// path overrides for tests
	doneFlagPath  string
	stateFilePath string
	bgpStatePath  string
	bootIDPath    string
}

var _ cellruntime.Runtime = (*ConfigDBRuntime)(nil)

func NewConfigDBRuntime(redisAddress string) *ConfigDBRuntime {
	return newConfigDBRuntime(sonicconfigdb.New(redisAddress), &systemHostOperations{})
}

func newConfigDBRuntime(db sonicconfigdb.DB, host hostOperations) *ConfigDBRuntime {
	r := &ConfigDBRuntime{db: db, host: host}
	r.readBootID = readBootID
	r.bgpEstablished = bgpEstablishedSetFromVtysh
	r.ifaceUp = ifaceUpFromSysfs
	r.doneFlagPath = doneFlagFile
	r.stateFilePath = stateFile
	r.bgpStatePath = bgpStateFile
	r.bootIDPath = bootIDFile
	return r
}

func (r *ConfigDBRuntime) ProviderName() string {
	return ProviderName
}

func (r *ConfigDBRuntime) NodeID(_ context.Context, node string) (string, error) {
	return node, nil
}

func (r *ConfigDBRuntime) ApplyCell(ctx context.Context, _ string, cfg *cellruntime.CellConfig) error {
	desired, err := resolveSwitchConfig(cfg)
	if err != nil {
		return err
	}

	if err := r.db.SetHostname(ctx, desired.Hostname); err != nil {
		return fmt.Errorf("setting switch hostname: %w", err)
	}
	if err := r.db.EnsureLoopback(ctx, "Loopback0", desired.LoopbackIPs); err != nil {
		return fmt.Errorf("configuring Loopback0: %w", err)
	}
	for _, port := range desired.AllPorts {
		if err := r.db.EnsurePort(ctx, port, sonicconfigdb.PortConfig{
			MTU:     9100,
			FEC:     "rs",
			Speed:   100000,
			AdminUp: true,
		}); err != nil {
			return fmt.Errorf("configuring port %s: %w", port, err)
		}
	}
	for _, port := range desired.SouthPorts {
		if err := r.db.EnsureVLAN(ctx, sonicconfigdb.VLANConfig{
			ID:        port.VLAN,
			Prefix:    port.IP,
			Port:      port.Port,
			DHCPRelay: port.Relay,
		}); err != nil {
			return fmt.Errorf("configuring VLAN %d: %w", port.VLAN, err)
		}
		if err := r.db.EnableLinkLocal(ctx, "Vlan"+strconv.Itoa(port.VLAN)); err != nil {
			return fmt.Errorf("enabling link-local on VLAN %d: %w", port.VLAN, err)
		}
	}
	for _, port := range desired.FabricPorts {
		if err := r.db.EnableLinkLocal(ctx, port); err != nil {
			return fmt.Errorf("enabling link-local on port %s: %w", port, err)
		}
	}
	if desired.ASN <= 0 || desired.ASN > int64(^uint32(0)) {
		return cellruntime.TerminalError(fmt.Errorf("BGP ASN %d is outside the uint32 range", desired.ASN))
	}
	if err := r.db.EnsureBGP(ctx, sonicconfigdb.BGPConfig{
		ASN:       uint32(desired.ASN),
		RouterID:  desired.RouterID,
		Prefix:    desired.Summary,
		Neighbors: desired.FabricPorts,
	}); err != nil {
		return fmt.Errorf("configuring BGP: %w", err)
	}
	if err := r.host.SaveConfig(ctx); err != nil {
		return fmt.Errorf("saving SONiC configuration: %w", err)
	}
	// Persist VLANs so CellStatus can check them after reboot without
	// re-deriving from the API. Written before Reboot so the file is present
	// if the process is killed by the reboot before returning.
	var vlans []string
	for _, port := range desired.SouthPorts {
		vlans = append(vlans, "Vlan"+strconv.Itoa(port.VLAN))
	}
	if err := os.WriteFile(r.stateFilePath, []byte(strings.Join(vlans, "\n")), 0o644); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	// Persist the expected BGP neighbor set (the fabric-facing interface peers)
	// so CellStatus can require every one of them to be Established before
	// reporting Active. Comparing against the expected set — rather than the
	// live neighbor count — means a reboot/reconverge window where vtysh briefly
	// reports zero or partial neighbors reads as not-ready instead of ready.
	if err := os.WriteFile(r.bgpStatePath, []byte(strings.Join(desired.FabricPorts, "\n")), 0o644); err != nil {
		return fmt.Errorf("writing BGP state file: %w", err)
	}
	bootID, err := r.readBootID()
	if err != nil {
		return fmt.Errorf("reading boot ID: %w", err)
	}
	if err := os.WriteFile(r.bootIDPath, []byte(bootID), 0o644); err != nil {
		return fmt.Errorf("writing boot ID file: %w", err)
	}
	if err := r.host.MarkCellApplied(ctx); err != nil {
		return fmt.Errorf("marking cell applied: %w", err)
	}
	if err := r.host.Reboot(ctx); err != nil {
		return fmt.Errorf("rebooting switch: %w", err)
	}
	return nil
}

func (r *ConfigDBRuntime) DeleteCell(ctx context.Context, node string) error {
	applied, err := r.host.CellApplied(ctx)
	if err != nil {
		return fmt.Errorf("checking cell %s marker: %w", node, err)
	}
	if !applied {
		return fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	}

	if err := r.db.Reset(ctx); err != nil {
		return fmt.Errorf("resetting SONiC ConfigDB: %w", err)
	}
	if err := r.host.SaveConfig(ctx); err != nil {
		return fmt.Errorf("saving SONiC configuration: %w", err)
	}
	if err := r.host.ClearCellApplied(ctx); err != nil {
		return fmt.Errorf("clearing cell marker: %w", err)
	}
	_ = os.Remove(r.stateFilePath)
	_ = os.Remove(r.bgpStatePath)
	_ = os.Remove(r.bootIDPath)
	if err := r.host.Reboot(ctx); err != nil {
		return fmt.Errorf("rebooting switch: %w", err)
	}
	return nil
}

func (r *ConfigDBRuntime) CellStatus(ctx context.Context, node string) (*cellruntime.CellStatus, error) {
	applied, err := r.host.CellApplied(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking cell %s marker: %w", node, err)
	}
	if !applied {
		return nil, fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	}

	// Check if device has rebooted since provisioning by comparing the stored
	// boot ID (written at ApplyCell time) against the current boot ID.
	storedBootID, err := os.ReadFile(r.bootIDPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No boot-ID baseline recorded yet: the reboot after provisioning
			// has not been confirmed, so the cell is still being created. Do not
			// write a baseline here — that must only happen in ApplyCell.
			return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseCreated}, nil
		}
		return nil, fmt.Errorf("reading stored boot ID: %w", err)
	}
	currentBootID, err := r.readBootID()
	if err != nil {
		return nil, fmt.Errorf("reading current boot ID: %w", err)
	}
	if strings.TrimSpace(string(storedBootID)) == currentBootID {
		// Same boot — device hasn't rebooted yet after provisioning.
		return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseCreated}, nil
	}

	// Check BGP sessions against the neighbors persisted at ApplyCell time. The
	// cell is not ready until every expected neighbor is Established. A missing
	// state file, or any expected neighbor not yet Established (including when
	// vtysh reports none during a reboot/reconverge), reads as not-ready — so a
	// cell cannot flip to Active before its fabric sessions have actually come
	// back up.
	if _, err := os.Stat(r.bgpStatePath); err != nil {
		if os.IsNotExist(err) {
			return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseCreated}, nil
		}
		return nil, fmt.Errorf("checking BGP state file: %w", err)
	}
	expectedNeighbors, err := readStateFile(r.bgpStatePath)
	if err != nil {
		return nil, fmt.Errorf("reading BGP state file: %w", err)
	}
	established, err := r.bgpEstablished(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking BGP: %w", err)
	}
	for _, neighbor := range expectedNeighbors {
		if !established[neighbor] {
			return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseCreated}, nil
		}
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

func (r *ConfigDBRuntime) InterfaceID(_ context.Context, iface string) (string, error) {
	return iface, nil
}

func (r *ConfigDBRuntime) InterfaceState(ctx context.Context, iface string) (*cellruntime.InterfaceState, error) {
	up, err := r.db.InterfaceOperUp(ctx, iface)
	if err != nil {
		return nil, fmt.Errorf("reading interface %s state: %w", iface, err)
	}
	return &cellruntime.InterfaceState{Up: up}, nil
}

func (r *ConfigDBRuntime) SetInterfaceAdminState(ctx context.Context, iface string, adminState bool) error {
	if err := r.db.SetInterfaceAdminState(ctx, iface, adminState); err != nil {
		return fmt.Errorf("setting interface %s admin state: %w", iface, err)
	}
	return nil
}
