// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package cellruntime

import (
	"context"
	"errors"
	"net/netip"
)

type CellMetadata struct {
	Namespace string
	Name      string
	UID       string
	Labels    map[string]string
}

type CellConfig struct {
	Metadata CellMetadata

	ID          string
	Hostname    string
	LoopbackIPs []netip.Addr
	Prefixes    []netip.Prefix

	Peers []Peer
}

type Peer struct {
	Interface *Interface
	DHCPRelay string
}

type InterfaceMetadata struct {
	Name string
	UID  string
}

type Interface struct {
	Metadata InterfaceMetadata
	ID       string
}

type Runtime interface {
	// ProviderName is the name of the provider.
	ProviderName() string

	// NodeID returns the provider internal ID of the node specified with by the given node name.
	NodeID(ctx context.Context, node string) (string, error)
	// ApplyCell applies the given cell to the specified node.
	ApplyCell(ctx context.Context, node string, cfg *CellConfig) error
	// DeleteCell deletes the given cell from the specified node.
	DeleteCell(ctx context.Context, node string) error
	// CellStatus returns the current cell status from the specified node.
	CellStatus(ctx context.Context, node string) (*CellStatus, error)

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

type CellStatus struct {
	Phase CellPhase
}

type CellPhase string

const (
	CellPhaseCreated CellPhase = "Created"
	CellPhaseActive  CellPhase = "Active"
	CellPhaseError   CellPhase = "Error"
)

var ErrNotFound = errors.New("not found")

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
