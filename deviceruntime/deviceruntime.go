// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package deviceruntime

import (
	"context"
	"errors"
	"net/netip"
)

type SwitchMetadata struct {
	Namespace string
	Name      string
	UID       string
}

type SwitchConfig struct {
	Metadata SwitchMetadata

	Hostname    string
	LoopbackIPs []netip.Addr
	Prefixes    []netip.Prefix

	VLANs []VLAN
	BGP   *BGP
}

type VLAN struct {
	ID        int32
	Prefix    netip.Prefix
	DHCPRelay string
	Members   []VLANMember
}

type VLANMember struct {
	Interface *Interface
}

type BGP struct {
	ASN        int32
	RouterID   string
	PeerGroups []BGPPeerGroup
}

type BGPPeerGroup struct {
	Name      string
	Neighbors []BGPNeighbor
}

type InterfaceMetadata struct {
	Name string
	UID  string
}

type Interface struct {
	Metadata InterfaceMetadata
	ID       string
}

type BGPNeighbor struct {
	VLANID    int32
	Interface *Interface
}

type Runtime interface {
	// ProviderName is the name of the provider.
	ProviderName() string

	// DeviceID returns the provider internal ID of the device specified with by the given device name.
	DeviceID(ctx context.Context, device string) (string, error)
	// ApplySwitch applies the given switch configuration to the specified device.
	ApplySwitch(ctx context.Context, device string, cfg *SwitchConfig) error
	// DeleteSwitch deletes the given switch configuration from the specified device.
	DeleteSwitch(ctx context.Context, device string) error

	// InterfaceID returns the provider internal ID of the interface specified by the given interface name.
	InterfaceID(ctx context.Context, iface string) (string, error)
	// InterfaceState returns the state of the interface specified by the given interface name.
	InterfaceState(ctx context.Context, iface string) (*InterfaceState, error)
	// SetInterfaceAdminState sets the admin state of the interface specified by the given interface
	// name to the given value.
	SetInterfaceAdminState(ctx context.Context, iface string, adminState bool) error
}

type InterfaceState struct {
	Up bool
}

type terminalError struct {
	err error
}

func TerminalError(err error) error {
	return &terminalError{err: err}
}

// Unwrap returns nil if te.err is nil.
func (te *terminalError) Unwrap() error {
	return te.err
}

func (te *terminalError) Error() string {
	if te.err == nil {
		return "nil terminal error"
	}
	return "terminal error: " + te.err.Error()
}

func (te *terminalError) Is(target error) bool {
	tp := &terminalError{}
	return errors.As(target, &tp)
}
