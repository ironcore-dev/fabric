// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ironcore-dev/fabric/cellruntime"
	sonicconfigdb "github.com/ironcore-dev/fabric/sonic/configdb"
)

const emptyFRRTemplate = `frr version 8.1
frr defaults traditional
hostname %s
log syslog informational
service integrated-vtysh-config
`

type hostOperations interface {
	WriteFRR(ctx context.Context, data []byte) error
	SaveConfig(ctx context.Context) error
	RestartBGP(ctx context.Context) error
	Hostname(ctx context.Context) (string, error)
	CellApplied(ctx context.Context) (bool, error)
	MarkCellApplied(ctx context.Context) error
	ClearCellApplied(ctx context.Context) error
	Reboot(ctx context.Context) error
}

// ConfigDBRuntime is a cellruntime.Runtime that writes directly to the SONiC CONFIG_DB.
type ConfigDBRuntime struct {
	db   sonicconfigdb.DB
	host hostOperations
}

var _ cellruntime.Runtime = (*ConfigDBRuntime)(nil)

func NewConfigDBRuntime(redisAddress string) *ConfigDBRuntime {
	return newConfigDBRuntime(sonicconfigdb.New(redisAddress), &systemHostOperations{})
}

func newConfigDBRuntime(db sonicconfigdb.DB, host hostOperations) *ConfigDBRuntime {
	return &ConfigDBRuntime{db: db, host: host}
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

	frr, err := renderFRR(desired)
	if err != nil {
		return err
	}
	if err := r.host.WriteFRR(ctx, frr); err != nil {
		return fmt.Errorf("writing FRR configuration: %w", err)
	}
	if err := r.host.SaveConfig(ctx); err != nil {
		return fmt.Errorf("saving SONiC configuration: %w", err)
	}
	if err := r.host.RestartBGP(ctx); err != nil {
		return fmt.Errorf("restarting BGP: %w", err)
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
	hostname, err := r.host.Hostname(ctx)
	if err != nil {
		return fmt.Errorf("reading switch hostname: %w", err)
	}
	frr := fmt.Appendf(nil, emptyFRRTemplate, hostname)
	if err := r.host.WriteFRR(ctx, frr); err != nil {
		return fmt.Errorf("writing FRR configuration: %w", err)
	}
	if err := r.host.RestartBGP(ctx); err != nil {
		return fmt.Errorf("restarting BGP: %w", err)
	}
	if err := r.host.ClearCellApplied(ctx); err != nil {
		return fmt.Errorf("clearing cell marker: %w", err)
	}
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
