// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/fabric/api/v1alpha1/applyconfiguration/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type InitInterfaceOptions struct {
	Name   string
	Handle string
}

type InitOptions struct {
	Interfaces []InitInterfaceOptions
}

func Init(ctx context.Context, c client.Client, nodeName, providerID string, opts InitOptions) error {
	fieldOwner := client.FieldOwner("node.fabriclet.ironcore.dev/" + nodeName)

	node := v1alpha1.Node(nodeName, "").
		WithSpec(v1alpha1.NodeSpec().
			WithProviderID(providerID))
	if err := c.Apply(ctx, node, fieldOwner); err != nil {
		return fmt.Errorf("applying node: %w", err)
	}

	for _, ifaceOpts := range opts.Interfaces {
		iface := v1alpha1.Interface(ifaceOpts.Name, "").
			WithSpec(v1alpha1.InterfaceSpec().
				WithNodeRef(v1alpha1.LocalObjectReference().WithName(nodeName)).
				WithHandle(ifaceOpts.Handle))
		if err := c.Apply(ctx, iface, fieldOwner); err != nil {
			return fmt.Errorf("[interface %s] applying interface: %w", ifaceOpts.Name, err)
		}
	}

	return nil
}
