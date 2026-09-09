// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ironcore-dev/wire/cellruntime"
	"github.com/ironcore-dev/wire/sonic/sonicpb"
	"google.golang.org/grpc"
)

const providerName = "sonic"

type Runtime struct {
	client      sonicpb.WireSonicSwitchServiceClient
	agentClient sonicpb.SwitchAgentServiceClient
}

func NewRuntime(conn *grpc.ClientConn) (*Runtime, error) {
	return &Runtime{
		client:      sonicpb.NewWireSonicSwitchServiceClient(conn),
		agentClient: sonicpb.NewSwitchAgentServiceClient(conn),
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
		loopbackIPs = append(loopbackIPs, ip.String()+"/128")
	}

	prefixes := make([]string, 0, len(cfg.Prefixes))
	for _, p := range cfg.Prefixes {
		prefixes = append(prefixes, p.String())
	}

	vlans := make([]*sonicpb.VLAN, 0, len(cfg.Peers))
	for i, peer := range cfg.Peers {
		vlanID := int32(i + 1)
		var members []*sonicpb.VLANMember
		if peer.Interface != nil {
			members = []*sonicpb.VLANMember{{InterfaceId: peer.Interface.ID}}
		}
		vlans = append(vlans, &sonicpb.VLAN{
			Id:        vlanID,
			DhcpRelay: peer.DHCPRelay,
			Members:   members,
		})
	}

	asn, err := strconv.ParseInt(cfg.ID, 10, 32)
	if err != nil {
		return cellruntime.TerminalError(fmt.Errorf("parsing cell ID %q as ASN: %w", cfg.ID, err))
	}

	neighbors := make([]*sonicpb.BGPNeighbor, 0, len(cfg.Peers))
	for i, peer := range cfg.Peers {
		if peer.Interface == nil {
			continue
		}
		neighbors = append(neighbors, &sonicpb.BGPNeighbor{
			VlanId:      int32(i + 1),
			InterfaceId: peer.Interface.ID,
		})
	}
	bgpConfig := &sonicpb.BGPConfig{
		Asn: int32(asn),
		PeerGroups: []*sonicpb.BGPPeerGroup{
			{Name: "default", Neighbors: neighbors},
		},
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
	//  - nil - deletion was issued but may not be a complete yet.
	//  - ErrNotFound - the cell is gone from switch. The controller marks the Cell as CellExpired
	//  		  and removes the finalizer, completing the deletion
	_, err := r.agentClient.FactoryReset(ctx, &sonicpb.FactoryResetRequest{})
	return err
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
	// resp, err := r.client.GetInterface(ctx, &sonicpb.WireGetInterfaceRequest{Iface: iface})
	// if err != nil {
	// 	return "", err
	// }
	re := regexp.MustCompile(`^leaf-\d+-(eth\d+-\d+)$`)
	m := re.FindStringSubmatch(iface)
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
