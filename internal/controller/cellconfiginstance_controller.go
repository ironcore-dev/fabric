// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"maps"
	"time"

	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const cellConfigInstanceFinalizer = "fabric.ironcore.dev/cellconfiginstance"

// CellConfigInstanceReconciler reconciles a CellConfigInstance object.
type CellConfigInstanceReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfiginstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfiginstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfiginstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cells,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cells/status,verbs=get

func (r *CellConfigInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	instance := &fabricv1alpha1.CellConfigInstance{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.reconcileExists(ctx, instance)
}

func (r *CellConfigInstanceReconciler) reconcileExists(
	ctx context.Context,
	instance *fabricv1alpha1.CellConfigInstance,
) (ctrl.Result, error) {
	if !instance.DeletionTimestamp.IsZero() {
		return r.delete(ctx, instance)
	}
	return r.reconcile(ctx, instance)
}

func (r *CellConfigInstanceReconciler) delete(
	ctx context.Context,
	instance *fabricv1alpha1.CellConfigInstance,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(instance, cellConfigInstanceFinalizer) {
		return ctrl.Result{}, nil
	}

	remaining, err := r.deleteOwnedCell(ctx, instance)
	if err != nil {
		return ctrl.Result{}, err
	}
	// TODO: Improve the requeue behaviour
	if remaining {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	base := instance.DeepCopy()
	if controllerutil.RemoveFinalizer(instance, cellConfigInstanceFinalizer) {
		if err := r.Patch(ctx, instance, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing finalizer from CellConfigInstance %s: %w",
				client.ObjectKeyFromObject(instance), err)
		}
	}
	return ctrl.Result{}, nil
}

func (r *CellConfigInstanceReconciler) reconcile(
	ctx context.Context,
	instance *fabricv1alpha1.CellConfigInstance,
) (ctrl.Result, error) {
	base := instance.DeepCopy()
	if controllerutil.AddFinalizer(instance, cellConfigInstanceFinalizer) {
		if err := r.Patch(ctx, instance, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer to CellConfigInstance %s: %w",
				client.ObjectKeyFromObject(instance), err)
		}
	}

	if !instance.Spec.Enabled {
		if _, err := r.deleteOwnedCell(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.updateStatus(ctx, instance, nil, metav1.ConditionFalse, "Disabled",
			"Instance is disabled; no Cell is materialized")
	}

	cell := &fabricv1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: instance.Namespace,
			Name:      instance.Name,
		},
	}

	if err := r.Get(ctx, client.ObjectKeyFromObject(cell), cell); err == nil {
		if cell.Spec.NodeRef != instance.Spec.NodeRef {
			if err := r.Delete(ctx, cell); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, fmt.Errorf("deleting Cell %s to retarget nodeRef: %w",
					client.ObjectKeyFromObject(cell), err)
			}
			// TODO: Improve the requeue behaviour
			return ctrl.Result{RequeueAfter: 1 * time.Second}, r.updateStatus(ctx, instance, nil, metav1.ConditionFalse,
				"Retargeting", "Recreating Cell to change its immutable nodeRef")
		}
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("getting Cell %s: %w", client.ObjectKeyFromObject(cell), err)
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cell, func() error {
		if cell.Labels == nil {
			cell.Labels = map[string]string{}
		}
		maps.Copy(cell.Labels, instance.Labels)
		cell.Spec = cellSpecFromConfig(instance.Spec.CellConfigSpec)
		return controllerutil.SetControllerReference(instance, cell, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("creating or updating Cell %s: %w",
			client.ObjectKeyFromObject(cell), err)
	}

	status, reason, message := readinessFromCellPhase(cell.Status.Phase)
	return ctrl.Result{}, r.updateStatus(ctx, instance,
		&fabricv1alpha1.LocalObjectReference{Name: cell.Name}, status, reason, message)
}

func (r *CellConfigInstanceReconciler) deleteOwnedCell(
	ctx context.Context,
	instance *fabricv1alpha1.CellConfigInstance,
) (bool, error) {
	cells := &fabricv1alpha1.CellList{}
	if err := r.List(ctx, cells,
		client.InNamespace(instance.Namespace),
		client.MatchingFields{cellOwnerKey: instance.Name}); err != nil {
		return false, fmt.Errorf("listing owned Cells of CellConfigInstance %s: %w",
			client.ObjectKeyFromObject(instance), err)
	}
	remaining := false
	for i := range cells.Items {
		cell := &cells.Items[i]
		if err := r.Delete(ctx, cell); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, fmt.Errorf("deleting Cell %s: %w", client.ObjectKeyFromObject(cell), err)
		}
		// Delete accepted; the Cell is gone or still terminating.
		remaining = true
	}
	return remaining, nil
}

func (r *CellConfigInstanceReconciler) updateStatus(
	ctx context.Context,
	instance *fabricv1alpha1.CellConfigInstance,
	cellRef *fabricv1alpha1.LocalObjectReference,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	base := instance.DeepCopy()
	instance.Status.ObservedGeneration = instance.Generation
	instance.Status.CellRef = cellRef
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               fabricv1alpha1.CellConfigInstanceConditionReady,
		Status:             status,
		ObservedGeneration: instance.Generation,
		Reason:             reason,
		Message:            message,
	})
	if err := r.Status().Patch(ctx, instance, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("updating status of CellConfigInstance %s: %w",
			client.ObjectKeyFromObject(instance), err)
	}
	return nil
}

func (r *CellConfigInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cellconfiginstance").
		For(&fabricv1alpha1.CellConfigInstance{}).
		Owns(&fabricv1alpha1.Cell{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		Complete(r)
}
