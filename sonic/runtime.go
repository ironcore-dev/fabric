// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ironcore-dev/fabric/cellruntime"
	"github.com/ironcore-dev/fabric/sonic/sonicpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const providerName = "sonic"

var ifaceNameRe = regexp.MustCompile(`^.+-(eth\d+-\d+)$`)

type Runtime struct {
	client sonicpb.WireSonicSwitchServiceClient
}

func NewRuntime(conn *grpc.ClientConn) (*Runtime, error) {
	return &Runtime{
		client: sonicpb.NewWireSonicSwitchServiceClient(conn),
	}, nil
}

func (r *Runtime) ProviderName() string {
	return providerName
}

func (r *Runtime) NodeID(ctx context.Context, node string) (string, error) {
	return node, nil
}
func (r *Runtime) ApplyCell(ctx context.Context, node string, cfg *cellruntime.CellConfig) error {

	// Map CellConfig fields to ApplySwitchRequest.
	//
	// Error handling:
	//   - Return a plain error for transient failures (gRPC timeout, network error) — the
	//     controller will requeue and retry.
	//   - Return cellruntime.TerminalError(err) for permanent/unrecoverable failures — the
	//     controller will move the Cell to CellFailed and stop retrying.
	//
	// On success the controller sets the Cell to CellPending and begins polling CellStatus
	// until it reports CellPhaseActive.

	loopbackIPs := make([]string, 0, len(cfg.LoopbackIPs))
	for _, ip := range cfg.LoopbackIPs {
		if !ip.Is6() {
			return cellruntime.TerminalError(fmt.Errorf("loopback IP %s is not an IPv6 address", ip))
		}
		loopbackIPs = append(loopbackIPs, ip.String()+"/128")
	}

	prefixes := make([]string, 0, len(cfg.Prefixes))
	for _, p := range cfg.Prefixes {
		prefixes = append(prefixes, p.String())
	}

	var vlans []*sonicpb.VLAN
	var uplinkMembers []*sonicpb.VLANMember
	accessIdx := int32(0)
	for _, peer := range cfg.Peers {
		if peer.DHCPRelay != "" {
			accessIdx++
			var members []*sonicpb.VLANMember
			if peer.Interface != nil {
				members = []*sonicpb.VLANMember{{InterfaceId: peer.Interface.ID}}
			}
			vlans = append(vlans, &sonicpb.VLAN{
				Id:        1000 + accessIdx,
				DhcpRelay: peer.DHCPRelay,
				Members:   members,
			})
		} else {
			if peer.Interface != nil {
				uplinkMembers = append(uplinkMembers, &sonicpb.VLANMember{InterfaceId: peer.Interface.ID})
			}
		}
	}
	if len(uplinkMembers) > 0 {
		vlans = append(vlans, &sonicpb.VLAN{Id: 0, Members: uplinkMembers})
	}

	id, err := strconv.ParseUint(cfg.ID, 10, 32)
	if err != nil {
		return cellruntime.TerminalError(fmt.Errorf("parsing cell ID %q: %w", cfg.ID, err))
	}

	isSpine := true
	for _, peer := range cfg.Peers {
		if peer.DHCPRelay != "" {
			isSpine = false
			break
		}
	}

	var asnBase uint32
	var routerIDPrefix string
	if isSpine {
		asnBase = 4_212_000_000
		routerIDPrefix = "2"
	} else {
		asnBase = 4_211_000_000
		routerIDPrefix = "1"
	}

	bgpConfig := &sonicpb.BGPConfig{
		Asn:      asnBase + uint32(id),
		RouterId: fmt.Sprintf("%s.0.0.%d", routerIDPrefix, id),
	}

	req := &sonicpb.ApplySwitchRequest{
		Device: node,
		Config: &sonicpb.SwitchConfig{
			Metadata: &sonicpb.SwitchMetadata{
				Namespace: cfg.Metadata.Namespace,
				Name:      cfg.Metadata.Name,
				Uid:       cfg.Metadata.UID,
			},
			Hostname:    cfg.Hostname,
			LoopbackIps: loopbackIPs,
			Prefixes:    prefixes,
			Vlans:       vlans,
			Bgp:         bgpConfig,
		},
	}

	_, err = r.client.ApplySwitch(ctx, req)
	return err

}

func (r *Runtime) DeleteCell(ctx context.Context, node string) error {
	// Error handling:
	//  - nil - reprovision is in progress; controller requeues and retries.
	//  - ErrNotFound - reprovision is complete (cell is gone); controller expires the Cell
	//    and removes the finalizer.
	resp, err := r.client.DeleteSwitch(ctx, &sonicpb.DeleteSwitchRequest{Device: node})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
		}
		return err
	}
	// resp.State == "" means reprovision completed; any other value (e.g. "Reprovisioning")
	// means it is still in progress — return nil so the controller requeues.
	if resp.State == "" {
		return fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	}
	return nil
}

func (r *Runtime) CellStatus(ctx context.Context, node string) (*cellruntime.CellStatus, error) {
	resp, err := r.client.GetCellStatus(ctx, &sonicpb.GetCellStatusRequest{Device: node})
	if err != nil {
		return nil, err
	}
	switch resp.State {
	case "":
		// Key absent in STATE_DB — no provisioning has been attempted yet.
		return nil, fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	case "Creating":
		return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseCreated}, nil
	case "Active":
		return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseActive}, nil
	case "Error":
		return &cellruntime.CellStatus{Phase: cellruntime.CellPhaseError}, nil
	default:
		return nil, fmt.Errorf("unknown cell state %q for %s", resp.State, node)
	}
}

func (r *Runtime) InterfaceID(ctx context.Context, iface string) (string, error) {

	m := ifaceNameRe.FindStringSubmatch(iface)
	if len(m) > 1 {
		return m[1], nil
	}
	return "", fmt.Errorf("wrong format of the interface name %s", iface)
}

func stripProviderPrefix(iface string) string {
	return strings.TrimPrefix(iface, providerName+"://")
}

func (r *Runtime) InterfaceState(ctx context.Context, iface string) (*cellruntime.InterfaceState, error) {
	resp, err := r.client.GetInterfaceState(ctx, &sonicpb.WireGetInterfaceStateRequest{Iface: stripProviderPrefix(iface)})
	if err != nil {
		return nil, err
	}
	return &cellruntime.InterfaceState{
		AdminUp: resp.AdminUp,
		OperUp:  resp.OperUp,
	}, nil
}

func (r *Runtime) SetInterfaceAdminState(ctx context.Context, iface string, adminState bool) error {
	_, err := r.client.SetInterfaceAdminState(ctx, &sonicpb.WireSetInterfaceAdminStateRequest{
		Iface:      stripProviderPrefix(iface),
		AdminState: adminState,
	})
	return err
}
