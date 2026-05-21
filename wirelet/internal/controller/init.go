// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/wire/api/v1alpha1/applyconfiguration/api/v1alpha1"
	"github.com/ironcore-dev/wire/wirelet/controller"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=devices,verbs=create;update;patch;apply
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=deviceinterfaces,verbs=create;update;patch;apply

func Init(ctx context.Context, c client.Client, deviceName, providerID string, opts controller.InitOptions) error {
	fieldOwner := client.FieldOwner("device.wirelet.ironcore.dev/" + deviceName)

	device := v1alpha1.Device(deviceName, "").
		WithSpec(v1alpha1.DeviceSpec().
			WithProviderID(providerID))
	if err := c.Apply(ctx, device, fieldOwner); err != nil {
		return fmt.Errorf("applying device: %w", err)
	}

	for _, ifaceOpts := range opts.Interfaces {
		iface := v1alpha1.DeviceInterface(ifaceOpts.Name, "").
			WithSpec(v1alpha1.DeviceInterfaceSpec().
				WithHandle(ifaceOpts.Handle))
		if err := c.Apply(ctx, iface, fieldOwner); err != nil {
			return fmt.Errorf("[interface %s] applying interface: %w", ifaceOpts.Name, err)
		}
	}

	return nil
}
