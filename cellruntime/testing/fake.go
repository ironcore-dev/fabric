// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package testing

import (
	"context"
	"fmt"
	"sync"

	"github.com/ironcore-dev/fabric/cellruntime"
)

const ProviderName = "fake"

type FakeCellRuntime struct {
	sync.RWMutex
	Cells           map[string]*FakeCell
	InterfaceStates map[string]bool
}

type FakeCell struct {
	Config *cellruntime.CellConfig
	Status *cellruntime.CellStatus
}

var _ cellruntime.Runtime = (*FakeCellRuntime)(nil)

func (r *FakeCellRuntime) ProviderName() string {
	return ProviderName
}

func (r *FakeCellRuntime) NodeID(ctx context.Context, node string) (string, error) {
	return node, nil
}

func (r *FakeCellRuntime) InterfaceID(ctx context.Context, iface string) (string, error) {
	return iface, nil
}

func NewFakeCellRuntime() *FakeCellRuntime {
	return &FakeCellRuntime{}
}

func (r *FakeCellRuntime) ApplyCell(ctx context.Context, node string, cfg *cellruntime.CellConfig) error {
	r.Lock()
	defer r.Unlock()

	if r.Cells == nil {
		r.Cells = make(map[string]*FakeCell)
	}
	r.Cells[node] = &FakeCell{
		Config: cfg,
		Status: &cellruntime.CellStatus{
			Phase: cellruntime.CellPhaseCreated,
		},
	}
	return nil
}

func (r *FakeCellRuntime) DeleteCell(ctx context.Context, node string) error {
	r.Lock()
	defer r.Unlock()

	_, ok := r.Cells[node]
	if !ok {
		return fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	}
	delete(r.Cells, node)
	return nil
}

func (r *FakeCellRuntime) CellStatus(ctx context.Context, node string) (*cellruntime.CellStatus, error) {
	r.Lock()
	defer r.Unlock()

	cell, ok := r.Cells[node]
	if !ok {
		return nil, fmt.Errorf("cell %s %w", node, cellruntime.ErrNotFound)
	}

	return cell.Status, nil
}

func (r *FakeCellRuntime) InterfaceState(ctx context.Context, iface string) (*cellruntime.InterfaceState, error) {
	r.RLock()
	defer r.RUnlock()

	state, ok := r.InterfaceStates[iface]
	if !ok {
		return nil, fmt.Errorf("interface %s %w", iface, cellruntime.ErrNotFound)
	}
	return &cellruntime.InterfaceState{
		Up: state,
	}, nil
}

func (r *FakeCellRuntime) SetInterfaceAdminState(ctx context.Context, handle string, up bool) error {
	r.Lock()
	defer r.Unlock()

	if _, ok := r.InterfaceStates[handle]; !ok {
		return fmt.Errorf("interface %s %w", handle, cellruntime.ErrNotFound)
	}
	r.InterfaceStates[handle] = up
	return nil
}
