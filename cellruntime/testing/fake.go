// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package testing

import (
	"context"
	"fmt"
	"sync"

	"github.com/ironcore-dev/wire/cellruntime"
)

const ProviderName = "fake"

type FakeCellRuntime struct {
	sync.RWMutex
	Cells           map[string]*cellruntime.CellConfig
	InterfaceStates map[string]bool
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
		r.Cells = make(map[string]*cellruntime.CellConfig)
	}
	r.Cells[node] = cfg
	return nil
}

func (r *FakeCellRuntime) DeleteCell(ctx context.Context, node string) error {
	r.Lock()
	defer r.Unlock()

	delete(r.Cells, node)
	return nil
}

func (r *FakeCellRuntime) InterfaceState(ctx context.Context, iface string) (*cellruntime.InterfaceState, error) {
	r.RLock()
	defer r.RUnlock()

	state, ok := r.InterfaceStates[iface]
	if !ok {
		return nil, fmt.Errorf("no such interface: %s", iface)
	}
	return &cellruntime.InterfaceState{
		Up: state,
	}, nil
}

func (r *FakeCellRuntime) SetInterfaceAdminState(ctx context.Context, handle string, up bool) error {
	r.Lock()
	defer r.Unlock()
	r.InterfaceStates[handle] = up
	return nil
}
