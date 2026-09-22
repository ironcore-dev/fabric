// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package sonic

import (
	"context"
	"errors"

	"github.com/ironcore-dev/fabric/cellruntime"
)

var errNotImplemented = errors.New("sonic configdb runtime: not implemented")

// ConfigDBRuntime is a cellruntime.Runtime that writes directly to the SONiC CONFIG_DB.
// TODO: implement. The script-based ScriptRuntime is the first step; select the
// implementation with the fabriclet's --backend flag.
type ConfigDBRuntime struct {
	role         string
	region       string
	ipv6Base     string
	searchDomain string
}

var _ cellruntime.Runtime = (*ConfigDBRuntime)(nil)

func NewConfigDBRuntime(role, region, ipv6Base, searchDomain string) (*ConfigDBRuntime, error) {
	return &ConfigDBRuntime{
		role:         role,
		region:       region,
		ipv6Base:     ipv6Base,
		searchDomain: searchDomain,
	}, nil
}

func (r *ConfigDBRuntime) ProviderName() string {
	return ProviderName
}

func (r *ConfigDBRuntime) NodeID(ctx context.Context, node string) (string, error) {
	return "", errNotImplemented
}

func (r *ConfigDBRuntime) ApplyCell(ctx context.Context, node string, cfg *cellruntime.CellConfig) error {
	return errNotImplemented
}

func (r *ConfigDBRuntime) DeleteCell(ctx context.Context, node string) error {
	return errNotImplemented
}

func (r *ConfigDBRuntime) CellStatus(ctx context.Context, node string) (*cellruntime.CellStatus, error) {
	return nil, errNotImplemented
}

func (r *ConfigDBRuntime) InterfaceID(ctx context.Context, iface string) (string, error) {
	return "", errNotImplemented
}

func (r *ConfigDBRuntime) InterfaceState(ctx context.Context, iface string) (*cellruntime.InterfaceState, error) {
	return nil, errNotImplemented
}

func (r *ConfigDBRuntime) SetInterfaceAdminState(ctx context.Context, iface string, adminState bool) error {
	return errNotImplemented
}
