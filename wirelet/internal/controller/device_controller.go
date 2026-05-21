// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/deviceruntime"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/lru"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type DeviceReconciler struct {
	client.Client
	APIReader       client.Reader
	DeviceRuntime   deviceruntime.Runtime
	DevicePredicate func(*v1alpha1.Device) bool
	AbsenceCache    *lru.Cache
}

// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=devices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=devices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=deviceinterfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=switches,verbs=get;list;watch

func (r *DeviceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	device := &v1alpha1.Device{}
	if err := r.Get(ctx, req.NamespacedName, device); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.DevicePredicate != nil && r.DevicePredicate(device) {
		return ctrl.Result{}, nil
	}
	if !device.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if device.Spec.ProviderID == "" {
		log.Info("Determining and setting provider ID on device")
		deviceID, err := r.DeviceRuntime.DeviceID(ctx, device.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("getting device %s handle: %w", device.Name, err)
		}

		return ctrl.Result{}, r.setDeviceProviderID(ctx, device, fmt.Sprintf("%s://%s", r.DeviceRuntime.ProviderName(), deviceID))
	}

	switchRef := device.Spec.SwitchRef
	if switchRef == nil {
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Checking if switch exists")
	ok, err := r.deviceSwitchExists(ctx, device)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("checking if switch exists: %w", err)
	}
	if ok {
		log.V(1).Info("Switch is present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Switch does not exist, deleting runtime switch (if any)")
	if err := r.DeviceRuntime.DeleteSwitch(ctx, device.Name); err != nil {
		return ctrl.Result{}, fmt.Errorf("deleting runtime switch %s: %w", device.Name, err)
	}

	log.V(1).Info("Releasing device")
	return ctrl.Result{}, r.releaseDevice(ctx, device)
}

func (r *DeviceReconciler) releaseDevice(ctx context.Context, device *v1alpha1.Device) error {
	base := device.DeepCopy()
	device.Spec.SwitchRef = nil
	return r.Patch(ctx, device, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *DeviceReconciler) deviceSwitchExists(ctx context.Context, device *v1alpha1.Device) (bool, error) {
	switchRef := device.Spec.SwitchRef
	if _, ok := r.AbsenceCache.Get(switchRef.UID); ok {
		return false, nil
	}

	swtch := &v1alpha1.Switch{}
	switchKey := client.ObjectKey{Namespace: switchRef.Namespace, Name: switchRef.Name}
	if err := r.APIReader.Get(ctx, switchKey, swtch); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error getting switch %s: %w", switchKey, err)
		}

		r.AbsenceCache.Add(switchRef.UID, nil)
		return false, nil
	}
	return true, nil
}

func (r *DeviceReconciler) setDeviceProviderID(
	ctx context.Context,
	device *v1alpha1.Device,
	providerID string,
) error {
	base := device.DeepCopy()
	device.Spec.ProviderID = providerID
	if err := r.Patch(ctx, device, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("setting device provider id: %w", err)
	}
	return nil
}

func (r *DeviceReconciler) enqueueBySwitch() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrl.LoggerFrom(ctx)

		swtch := obj.(*v1alpha1.Switch)

		deviceList := &v1alpha1.DeviceList{}
		if err := r.List(ctx, deviceList, client.MatchingFields{deviceSwitchKey: client.ObjectKeyFromObject(swtch).String()}); err != nil {
			log.Error(err, "Error listing devices for switch")
			return nil
		}

		var reqs []reconcile.Request
		for _, device := range deviceList.Items {
			switchRef := device.Spec.SwitchRef
			if switchRef == nil {
				continue
			}

			if switchRef.UID != swtch.UID {
				continue
			}

			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&device)})
		}
		return reqs
	})
}

const deviceSwitchKey = ".spec.switchRef.{namespace,name}"

func (r *DeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.Device{}, deviceSwitchKey, func(obj client.Object) []string {
		device := obj.(*v1alpha1.Device)
		switchRef := device.Spec.SwitchRef
		if switchRef == nil {
			return nil
		}
		return []string{(client.ObjectKey{Namespace: switchRef.Namespace, Name: switchRef.Name}).String()}
	}); err != nil {
		return fmt.Errorf("indexing device %s: %w", deviceSwitchKey, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(
			&v1alpha1.Device{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				device := obj.(*v1alpha1.Device)
				if r.DevicePredicate != nil {
					return r.DevicePredicate(device)
				}
				return true
			})),
		).
		Watches(
			&v1alpha1.Switch{},
			r.enqueueBySwitch(),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				swtch := obj.(*v1alpha1.Switch)
				return !swtch.DeletionTimestamp.IsZero()
			})),
		).
		Complete(r)
}
