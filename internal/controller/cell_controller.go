// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// CellReconciler reports the binding state of a Cell. The Fabriclet for the
// Node selected by Cell.spec.nodeRef owns all binding outcomes once that Node
// exists.
type CellReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cells,verbs=get;list;watch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cells/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=nodes,verbs=get;list;watch

func (r *CellReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cell := &fabricv1alpha1.Cell{}
	if err := r.Get(ctx, req.NamespacedName, cell); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !cell.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	node := &fabricv1alpha1.Node{}
	if err := r.Get(ctx, client.ObjectKey{Name: cell.Spec.NodeRef.Name}, node); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.setBoundCondition(ctx, cell, metav1.ConditionFalse, "NodeNotFound",
				fmt.Sprintf("Node %q does not exist", cell.Spec.NodeRef.Name))
		}
		return ctrl.Result{}, fmt.Errorf("getting Node %s: %w", cell.Spec.NodeRef.Name, err)
	}

	return ctrl.Result{}, nil
}

func (r *CellReconciler) setBoundCondition(
	ctx context.Context,
	cell *fabricv1alpha1.Cell,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	base := cell.DeepCopy()
	if !meta.SetStatusCondition(&cell.Status.Conditions, metav1.Condition{
		Type:               fabricv1alpha1.CellConditionTypeBound,
		Status:             status,
		ObservedGeneration: cell.Generation,
		Reason:             reason,
		Message:            message,
	}) {
		return nil
	}
	if err := r.Status().Patch(ctx, cell, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("updating bound condition for Cell %s: %w", client.ObjectKeyFromObject(cell), err)
	}
	return nil
}

func (r *CellReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cell").
		For(&fabricv1alpha1.Cell{}).
		Watches(&fabricv1alpha1.Node{}, handler.EnqueueRequestsFromMapFunc(r.cellsForNode)).
		Complete(r)
}

func (r *CellReconciler) cellsForNode(ctx context.Context, obj client.Object) []reconcile.Request {
	node := obj.(*fabricv1alpha1.Node)
	requests := make(map[client.ObjectKey]struct{})

	if ref := node.Spec.CellRef; ref != nil {
		requests[client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}] = struct{}{}
	}

	cells := &fabricv1alpha1.CellList{}
	if err := r.List(ctx, cells); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Could not list Cells for Node", "node", node.Name)
		return nil
	}
	for i := range cells.Items {
		cell := &cells.Items[i]
		if cell.Spec.NodeRef.Name == node.Name {
			requests[client.ObjectKeyFromObject(cell)] = struct{}{}
		}
	}

	result := make([]reconcile.Request, 0, len(requests))
	for key := range requests {
		result = append(result, reconcile.Request{NamespacedName: key})
	}
	return result
}
