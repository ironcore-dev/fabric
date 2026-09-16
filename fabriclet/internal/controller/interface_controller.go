// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/fabric/api/v1alpha1"
	"github.com/ironcore-dev/fabric/cellruntime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type InterfaceReconciler struct {
	client.Client
	CellRuntime        cellruntime.Runtime
	InterfacePredicate func(*v1alpha1.Interface) bool
}

// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=interfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=interfaces/status,verbs=get;patch;update

func (r *InterfaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	iface := &v1alpha1.Interface{}
	if err := r.Get(ctx, req.NamespacedName, iface); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.InterfacePredicate != nil && !r.InterfacePredicate(iface) {
		return ctrl.Result{}, nil
	}
	if !iface.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if iface.Spec.Handle == "" {
		log.V(1).Info("Determining and setting handle on interface")
		id, err := r.CellRuntime.InterfaceID(ctx, iface.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("getting interface %s handle: %w", iface.Name, err)
		}

		return ctrl.Result{}, r.setInterfaceHandle(ctx, iface, fmt.Sprintf("%s://%s", r.CellRuntime.ProviderName(), id))
	}

	actualState, err := r.CellRuntime.InterfaceState(ctx, iface.Name)
	if err != nil {
		log.Error(err, "Checking runtime interface state, setting state to unknown")
		return ctrl.Result{}, r.applyOperationState(ctx, iface, v1alpha1.OperationStateUnknown)
	}

	desiredUp := iface.Spec.AdminState == v1alpha1.AdminStateUp
	if desiredUp == actualState.Up {
		log.V(1).Info("Desired up matches actual", "Up", actualState.Up)
		return ctrl.Result{}, r.applyOperationState(ctx, iface, boolToOperationState(actualState.Up))
	}

	log.V(1).Info("Setting runtime interface state", "Up", desiredUp)
	if err := r.CellRuntime.SetInterfaceAdminState(ctx, iface.Name, desiredUp); err != nil {
		log.Error(err, "Setting interface state, setting state to unknown")
		return ctrl.Result{}, r.applyOperationState(ctx, iface, v1alpha1.OperationStateUnknown)
	}

	log.V(1).Info("Set runtime interface state, updating state")
	return ctrl.Result{}, r.applyOperationState(ctx, iface, boolToOperationState(desiredUp))
}

func boolToOperationState(value bool) v1alpha1.OperationState {
	if value {
		return v1alpha1.OperationStateUp
	}
	return v1alpha1.OperationStateDown
}

func (r *InterfaceReconciler) setInterfaceHandle(
	ctx context.Context,
	iface *v1alpha1.Interface,
	handle string,
) error {
	base := iface.DeepCopy()
	iface.Spec.Handle = handle
	if err := r.Patch(ctx, iface, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("setting interface handle: %w", err)
	}
	return nil
}

func (r *InterfaceReconciler) applyOperationState(
	ctx context.Context,
	iface *v1alpha1.Interface,
	state v1alpha1.OperationState,
) error {
	if iface.Status.OperationState == state {
		return nil
	}

	base := iface.DeepCopy()
	iface.Status.OperationState = state
	return r.Status().Patch(ctx, iface, client.MergeFrom(base))
}

func (r *InterfaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&v1alpha1.Interface{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				iface := obj.(*v1alpha1.Interface)
				if r.InterfacePredicate != nil {
					return r.InterfacePredicate(iface)
				}
				return true
			})),
		).
		Complete(r)
}
