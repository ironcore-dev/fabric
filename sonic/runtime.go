// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"

	"github.com/ironcore-dev/wire/deviceruntime"
)

type Runtime struct {
}

func NewRuntime() (*Runtime, error) {
	panic("implement me")
}

func (r *Runtime) ProviderName() string {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) DeviceID(ctx context.Context, device string) (string, error) {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) ApplySwitch(ctx context.Context, device string, cfg *deviceruntime.SwitchConfig) error {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) DeleteSwitch(ctx context.Context, device string) error {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) InterfaceID(ctx context.Context, iface string) (string, error) {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) InterfaceState(ctx context.Context, iface string) (*deviceruntime.InterfaceState, error) {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) SetInterfaceAdminState(ctx context.Context, iface string, adminState bool) error {
	// TODO implement me
	panic("implement me")
}
