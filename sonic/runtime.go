// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"

	"github.com/ironcore-dev/wire/cellruntime"
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

func (r *Runtime) NodeID(ctx context.Context, node string) (string, error) {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) ApplyCell(ctx context.Context, node string, cfg *cellruntime.CellConfig) error {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) DeleteCell(ctx context.Context, node string) error {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) CellStatus(ctx context.Context, node string) (*cellruntime.CellStatus, error) {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) InterfaceID(ctx context.Context, iface string) (string, error) {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) InterfaceState(ctx context.Context, iface string) (*cellruntime.InterfaceState, error) {
	// TODO implement me
	panic("implement me")
}

func (r *Runtime) SetInterfaceAdminState(ctx context.Context, iface string, adminState bool) error {
	// TODO implement me
	panic("implement me")
}
