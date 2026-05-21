// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/deviceruntime"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const finalizerPrefix = "device.wirelet.ironcore.dev/"

func DeviceFinalizer(deviceName string) string {
	return finalizerPrefix + deviceName
}

type SwitchReconciler struct {
	client.Client
	events.EventRecorder
	DevicePredicate func(device *v1alpha1.Device) bool
	DeviceRuntime   deviceruntime.Runtime
}

var terminalSwitchPhases = map[v1alpha1.SwitchPhase]struct{}{
	v1alpha1.SwitchExpired: {},
	v1alpha1.SwitchFailed:  {},
}

// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=switches,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=switches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=switches/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=devices,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=devices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=deviceinterfaces,verbs=get;list;watch

func (r *SwitchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	swtch := &v1alpha1.Switch{}
	if err := r.Get(ctx, req.NamespacedName, swtch); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, swtch)
}

func (r *SwitchReconciler) reconcileExists(
	ctx context.Context,
	swtch *v1alpha1.Switch,
) (ctrl.Result, error) {
	device := &v1alpha1.Device{}
	deviceKey := client.ObjectKey{Name: swtch.Spec.DeviceRef.Name}
	if err := r.Get(ctx, deviceKey, device); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("getting device %s: %w", deviceKey, err)
		}

		return ctrl.Result{}, nil
	}
	if r.DevicePredicate != nil && !r.DevicePredicate(device) {
		return ctrl.Result{}, nil
	}

	ctx = ctrl.LoggerInto(ctx, ctrl.LoggerFrom(ctx, "device", deviceKey))

	if !swtch.DeletionTimestamp.IsZero() {
		return r.delete(ctx, device, swtch)
	}
	return r.reconcile(ctx, device, swtch)
}

func (r *SwitchReconciler) delete(
	ctx context.Context,
	device *v1alpha1.Device,
	swtch *v1alpha1.Switch,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	deviceSwitchRef := device.Spec.SwitchRef
	if deviceSwitchRef == nil {
		log.V(1).Info("Not bound to switch, nothing to do")
		return ctrl.Result{}, nil
	}

	if deviceSwitchRef.Namespace != swtch.Namespace || deviceSwitchRef.Name != swtch.Name || deviceSwitchRef.UID != swtch.UID {
		log.V(1).Info("Device is bound to different switch")
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(swtch, DeviceFinalizer(device.Name)) {
		log.V(1).Info("Switch does not contain finalizer, nothing to do")
		return ctrl.Result{}, nil
	}

	if _, ok := terminalSwitchPhases[swtch.Status.Phase]; ok {
		log.V(1).Info("Switch is in terminal phase, removing finalizer")
		base := swtch.DeepCopy()
		controllerutil.RemoveFinalizer(swtch, DeviceFinalizer(device.Name))
		if err := r.Patch(ctx, swtch, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
		}

		log.V(1).Info("Removed finalizer")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Resetting switch")
	if err := r.DeviceRuntime.DeleteSwitch(ctx, device.Name); err != nil {
		return ctrl.Result{}, fmt.Errorf("resetting switch: %w", err)
	}

	log.V(1).Info("Expiring switch")
	return ctrl.Result{}, r.applySwitchPhase(ctx, swtch, v1alpha1.SwitchExpired)
}

func (r *SwitchReconciler) patchClaimDevice(ctx context.Context, device *v1alpha1.Device, swtch *v1alpha1.Switch) error {
	base := device.DeepCopy()
	device.Spec.SwitchRef = &v1alpha1.NamespacedUIDReference{
		Namespace: swtch.Namespace,
		Name:      swtch.Name,
		UID:       swtch.UID,
	}
	if err := r.Patch(ctx, device, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patching device: %w", err)
	}
	return nil
}

func (r *SwitchReconciler) applySwitchPhase(ctx context.Context, swtch *v1alpha1.Switch, phase v1alpha1.SwitchPhase) error {
	if phase == swtch.Status.Phase {
		return nil
	}

	base := swtch.DeepCopy()
	swtch.Status.Phase = phase
	if err := r.Status().Patch(ctx, swtch, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patching switch: %w", err)
	}
	return nil
}

func (r *SwitchReconciler) reconcile(
	ctx context.Context,
	device *v1alpha1.Device,
	swtch *v1alpha1.Switch,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	deviceSwitchRef := device.Spec.SwitchRef
	if deviceSwitchRef == nil {
		if _, ok := terminalSwitchPhases[swtch.Status.Phase]; ok {
			log.V(1).Info("Switch is in terminal phase, won't bind")
			return ctrl.Result{}, nil
		}

		log.V(1).Info("Claiming device")
		return ctrl.Result{}, r.patchClaimDevice(ctx, device, swtch)
	}

	if deviceSwitchRef.Namespace != swtch.Namespace || deviceSwitchRef.Name != swtch.Name || deviceSwitchRef.UID != swtch.UID {
		log.V(1).Info("Device is bound to different switch")
		return ctrl.Result{}, r.applySwitchPhase(ctx, swtch, v1alpha1.SwitchPending)
	}

	if _, ok := terminalSwitchPhases[swtch.Status.Phase]; ok {
		log.V(1).Info("Switch is in terminal phase, won't reconcile")
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(swtch, DeviceFinalizer(device.Name)) {
		log.V(1).Info("Adding finalizer to switch")

		base := swtch.DeepCopy()
		controllerutil.AddFinalizer(swtch, DeviceFinalizer(device.Name))
		if err := r.Patch(ctx, swtch, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("patching switch %s: %w", client.ObjectKeyFromObject(swtch), err)
		}
		return ctrl.Result{}, nil
	}

	if swtch.Status.Phase == v1alpha1.SwitchActive {
		log.V(1).Info("Switch is active, skipping apply")
		return ctrl.Result{}, nil
	}

	if swtch.Status.Phase == "" {
		log.V(1).Info("Setting switch to pending")
		return ctrl.Result{}, r.applySwitchPhase(ctx, swtch, v1alpha1.SwitchPending)
	}

	log.V(1).Info("Resolving switch config")
	cfg, err := r.resolveSwitchConfig(ctx, swtch)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolving switch config: %w", err)
	}

	log.V(1).Info("Applying switch")
	if err := r.DeviceRuntime.ApplySwitch(ctx, device.Name, cfg); err != nil {
		if !errors.Is(err, deviceruntime.TerminalError(nil)) {
			r.Eventf(swtch, device, v1.EventTypeWarning, "ApplySwitchError", "ApplySwitch", "Error applying switch: %v", err)
			return ctrl.Result{}, fmt.Errorf("applying switch %s: %w", client.ObjectKeyFromObject(swtch), err)
		}

		log.Error(err, "Encountered terminal error, setting switch to failed")
		r.Eventf(swtch, device, v1.EventTypeWarning, "ApplySwitchTerminalError", "ApplySwitch", "Terminal error applying switch: %v", err)
		return ctrl.Result{}, r.applySwitchPhase(ctx, swtch, v1alpha1.SwitchFailed)
	}

	log.V(1).Info("Applied switch, setting switch to active")
	return ctrl.Result{}, r.applySwitchPhase(ctx, swtch, v1alpha1.SwitchActive)
}

func (r *SwitchReconciler) resolveSwitchConfig(
	ctx context.Context,
	swtch *v1alpha1.Switch,
) (*deviceruntime.SwitchConfig, error) {
	ips := make([]netip.Addr, 0, len(swtch.Spec.IPs))
	for _, ip := range swtch.Spec.IPs {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return nil, fmt.Errorf("parsing IP address %s: %w", ip, err)
		}

		ips = append(ips, addr)
	}

	prefixes := make([]netip.Prefix, 0, len(swtch.Spec.Prefixes))
	for _, prefix := range swtch.Spec.Prefixes {
		pfx, err := netip.ParsePrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("parsing prefix %s: %w", prefix, err)
		}

		prefixes = append(prefixes, pfx)
	}

	interfaceByKey := make(map[string]*deviceruntime.Interface)
	resolveInterface := func(ifaceName string) (*deviceruntime.Interface, error) {
		deviceInterface := &v1alpha1.DeviceInterface{}
		if err := r.Get(ctx, client.ObjectKey{Name: ifaceName}, deviceInterface); err != nil {
			return nil, fmt.Errorf("getting device interface %s: %w", ifaceName, err)
		}

		ifaceID, ok := strings.CutPrefix(deviceInterface.Spec.Handle, fmt.Sprintf("%s://", r.DeviceRuntime.ProviderName()))
		if !ok {
			return nil, fmt.Errorf("device interface %s handle %q is invalid", ifaceName, deviceInterface.Spec.Handle)
		}

		interfaceByKey[ifaceName] = &deviceruntime.Interface{
			Metadata: deviceruntime.InterfaceMetadata{
				Name: ifaceName,
				UID:  string(deviceInterface.UID),
			},
			ID: ifaceID,
		}

		return interfaceByKey[ifaceName], nil
	}

	vlans := make([]deviceruntime.VLAN, 0, len(swtch.Spec.VLANs))
	for _, vlan := range swtch.Spec.VLANs {
		prefix, err := netip.ParsePrefix(vlan.Prefix)
		if err != nil {
			return nil, fmt.Errorf("parsing VLAN %d prefix %s: %w", vlan.ID, vlan.Prefix, err)
		}

		members := make([]deviceruntime.VLANMember, 0, len(vlan.Members))
		for _, member := range vlan.Members {
			switch {
			case member.DeviceInterfaceRef != nil:
				iface, err := resolveInterface(member.DeviceInterfaceRef.Name)
				if err != nil {
					return nil, fmt.Errorf("resolving vlan %d device interface %s: %w", vlan.ID, member.DeviceInterfaceRef.Name, err)
				}

				members = append(members, deviceruntime.VLANMember{Interface: iface})
			default:
				return nil, fmt.Errorf("unknown vlan member %#+v", member)
			}
		}

		vlans = append(vlans, deviceruntime.VLAN{
			ID:        vlan.ID,
			Prefix:    prefix,
			DHCPRelay: vlan.DHCPRelay,
			Members:   members,
		})
	}

	var bgp *deviceruntime.BGP
	if swtchBGP := swtch.Spec.BGP; swtchBGP != nil {
		peerGroups := make([]deviceruntime.BGPPeerGroup, 0, len(swtchBGP.PeerGroups))
		for _, peerGroup := range swtchBGP.PeerGroups {
			neighbors := make([]deviceruntime.BGPNeighbor, 0, len(peerGroup.Neighbors))
			for _, neighbor := range peerGroup.Neighbors {
				switch {
				case neighbor.VLANID > 0:
					neighbors = append(neighbors, deviceruntime.BGPNeighbor{VLANID: neighbor.VLANID})
				case neighbor.DeviceInterfaceRef != nil:
					iface, err := resolveInterface(neighbor.DeviceInterfaceRef.Name)
					if err != nil {
						return nil, fmt.Errorf("resolving bgp peer group %s device interface %s: %w", peerGroup.Name, neighbor.DeviceInterfaceRef.Name, err)
					}

					neighbors = append(neighbors, deviceruntime.BGPNeighbor{Interface: iface})
				default:
					return nil, fmt.Errorf("unknown bgp peer group neighbor %#+v", neighbor)
				}
			}

			peerGroups = append(peerGroups, deviceruntime.BGPPeerGroup{
				Name:      peerGroup.Name,
				Neighbors: neighbors,
			})
		}

		bgp = &deviceruntime.BGP{
			ASN:        swtchBGP.ASN,
			RouterID:   swtchBGP.RouterID,
			PeerGroups: peerGroups,
		}
	}

	return &deviceruntime.SwitchConfig{
		Metadata: deviceruntime.SwitchMetadata{
			Namespace: swtch.Namespace,
			Name:      swtch.Name,
			UID:       string(swtch.UID),
		},
		Hostname:    swtch.Spec.Hostname,
		LoopbackIPs: ips,
		Prefixes:    prefixes,
		VLANs:       vlans,
		BGP:         bgp,
	}, nil
}

const (
	switchDeviceKey = ".spec.deviceRef.name"
)

func (r *SwitchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.Switch{}, switchDeviceKey, func(obj client.Object) []string {
		swtch := obj.(*v1alpha1.Switch)
		deviceName := swtch.Spec.DeviceRef.Name
		return []string{deviceName}
	}); err != nil {
		return fmt.Errorf("indexing switch device field: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(
			&v1alpha1.Switch{},
		).
		Watches(
			&v1alpha1.Device{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				device := obj.(*v1alpha1.Device)

				switchList := &v1alpha1.SwitchList{}
				if err := r.List(ctx, switchList, client.MatchingFields{switchDeviceKey: device.Name}); err != nil {
					return nil
				}

				requests := make([]reconcile.Request, 0, len(switchList.Items))
				for _, swtch := range switchList.Items {
					requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&swtch)})
				}
				return requests
			}),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				device := obj.(*v1alpha1.Device)
				if r.DevicePredicate != nil {
					return r.DevicePredicate(device)
				}
				return true
			})),
		).
		Complete(r)
}
