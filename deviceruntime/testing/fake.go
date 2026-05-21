// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package testing

import (
	"context"
	"fmt"
	"sync"

	"github.com/ironcore-dev/wire/deviceruntime"
)

const ProviderName = "fake"

type FakeDeviceRuntime struct {
	sync.RWMutex
	Switches        map[string]*deviceruntime.SwitchConfig
	InterfaceStates map[string]bool
}

var _ deviceruntime.Runtime = (*FakeDeviceRuntime)(nil)

func (r *FakeDeviceRuntime) ProviderName() string {
	return ProviderName
}

func (r *FakeDeviceRuntime) DeviceID(ctx context.Context, device string) (string, error) {
	return device, nil
}

func (r *FakeDeviceRuntime) InterfaceID(ctx context.Context, iface string) (string, error) {
	return iface, nil
}

func NewFakeDeviceRuntime() *FakeDeviceRuntime {
	return &FakeDeviceRuntime{}
}

func (r *FakeDeviceRuntime) ApplySwitch(ctx context.Context, device string, cfg *deviceruntime.SwitchConfig) error {
	r.Lock()
	defer r.Unlock()

	if r.Switches == nil {
		r.Switches = make(map[string]*deviceruntime.SwitchConfig)
	}
	r.Switches[device] = cfg
	return nil
}

func (r *FakeDeviceRuntime) DeleteSwitch(ctx context.Context, device string) error {
	r.Lock()
	defer r.Unlock()

	delete(r.Switches, device)
	return nil
}

func (r *FakeDeviceRuntime) InterfaceState(ctx context.Context, iface string) (*deviceruntime.InterfaceState, error) {
	r.RLock()
	defer r.RUnlock()

	state, ok := r.InterfaceStates[iface]
	if !ok {
		return nil, fmt.Errorf("no such interface: %s", iface)
	}
	return &deviceruntime.InterfaceState{
		Up: state,
	}, nil
}

func (r *FakeDeviceRuntime) SetInterfaceAdminState(ctx context.Context, handle string, up bool) error {
	r.Lock()
	defer r.Unlock()
	r.InterfaceStates[handle] = up
	return nil
}
