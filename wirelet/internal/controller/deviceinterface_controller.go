// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/deviceruntime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type DeviceInterfaceReconciler struct {
	client.Client
	DeviceRuntime            deviceruntime.Runtime
	DeviceInterfacePredicate func(*v1alpha1.DeviceInterface) bool
}

// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=deviceinterfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=deviceinterfaces/status,verbs=get;patch;update

func (r *DeviceInterfaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	iface := &v1alpha1.DeviceInterface{}
	if err := r.Get(ctx, req.NamespacedName, iface); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.DeviceInterfacePredicate != nil && !r.DeviceInterfacePredicate(iface) {
		return ctrl.Result{}, nil
	}
	if !iface.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if iface.Spec.Handle == "" {
		log.V(1).Info("Determining and setting handle on interface")
		id, err := r.DeviceRuntime.InterfaceID(ctx, iface.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("getting interface %s handle: %w", iface.Name, err)
		}

		return ctrl.Result{}, r.setInterfaceHandle(ctx, iface, fmt.Sprintf("%s://%s", r.DeviceRuntime.ProviderName(), id))
	}

	actualState, err := r.DeviceRuntime.InterfaceState(ctx, iface.Name)
	if err != nil {
		log.Error(err, "Checking runtime device interface state, setting state to unknown")
		return ctrl.Result{}, r.applyOperationState(ctx, iface, v1alpha1.OperationStateUnknown)
	}

	desiredUp := iface.Spec.AdminState == v1alpha1.AdminStateUp
	if desiredUp == actualState.Up {
		log.V(1).Info("Desired up matches actual", "Up", actualState.Up)
		return ctrl.Result{}, r.applyOperationState(ctx, iface, boolToOperationState(actualState.Up))
	}

	log.V(1).Info("Setting runtime device interface state", "Up", desiredUp)
	if err := r.DeviceRuntime.SetInterfaceAdminState(ctx, iface.Name, desiredUp); err != nil {
		log.Error(err, "Setting device interface state, setting state to unknown")
		return ctrl.Result{}, r.applyOperationState(ctx, iface, v1alpha1.OperationStateUnknown)
	}

	log.V(1).Info("Set runtime device interface state, updating state")
	return ctrl.Result{}, r.applyOperationState(ctx, iface, boolToOperationState(desiredUp))
}

func boolToOperationState(value bool) v1alpha1.OperationState {
	if value {
		return v1alpha1.OperationStateUp
	}
	return v1alpha1.OperationStateDown
}

func (r *DeviceInterfaceReconciler) setInterfaceHandle(
	ctx context.Context,
	iface *v1alpha1.DeviceInterface,
	handle string,
) error {
	base := iface.DeepCopy()
	iface.Spec.Handle = handle
	if err := r.Patch(ctx, iface, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("setting interface handle: %w", err)
	}
	return nil
}

func (r *DeviceInterfaceReconciler) applyOperationState(
	ctx context.Context,
	deviceInterface *v1alpha1.DeviceInterface,
	state v1alpha1.OperationState,
) error {
	if deviceInterface.Status.OperationState == state {
		return nil
	}

	base := deviceInterface.DeepCopy()
	deviceInterface.Status.OperationState = state
	return r.Status().Patch(ctx, deviceInterface, client.MergeFrom(base))
}

func (r *DeviceInterfaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&v1alpha1.DeviceInterface{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				deviceInterface := obj.(*v1alpha1.DeviceInterface)
				if r.DeviceInterfacePredicate != nil {
					return r.DeviceInterfacePredicate(deviceInterface)
				}
				return true
			})),
		).
		Complete(r)
}
