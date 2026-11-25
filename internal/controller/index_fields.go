// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	cellConfigInstanceOwnerKey = ".metadata.controller.cellconfigrollout"
	cellOwnerKey               = ".metadata.controller.cellconfiginstance"
)

func RegisterIndexFields(ctx context.Context, indexer client.FieldIndexer) error {
	if err := indexer.IndexField(ctx, &fabricv1alpha1.CellConfigInstance{}, cellConfigInstanceOwnerKey,
		func(obj client.Object) []string {
			return ownerNameForKind(obj, "CellConfigRollout")
		}); err != nil {
		return err
	}

	if err := indexer.IndexField(ctx, &fabricv1alpha1.Cell{}, cellOwnerKey,
		func(obj client.Object) []string {
			return ownerNameForKind(obj, "CellConfigInstance")
		}); err != nil {
		return err
	}

	return nil
}
