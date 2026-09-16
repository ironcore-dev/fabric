// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/ironcore-dev/fabric/api/v1alpha1"
	"github.com/ironcore-dev/fabric/cellruntime"
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

const nodeFinalizerPrefix = "node.fabriclet.ironcore.dev/"

func NodeFinalizer(nodeName string) string {
	return nodeFinalizerPrefix + nodeName
}

type CellReconciler struct {
	client.Client
	events.EventRecorder
	NodePredicate                    func(node *v1alpha1.Node) bool
	CellRuntime                      cellruntime.Runtime
	CellRuntimePollInterval          time.Duration
	CellRuntimePollImmediateInterval time.Duration
}

var terminalCellPhases = map[v1alpha1.CellPhase]struct{}{
	v1alpha1.CellExpired: {},
	v1alpha1.CellFailed:  {},
}

// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cells,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cells/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cells/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=nodes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=nodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=interfaces,verbs=get;list;watch

func (r *CellReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cell := &v1alpha1.Cell{}
	if err := r.Get(ctx, req.NamespacedName, cell); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, cell)
}

func (r *CellReconciler) reconcileExists(
	ctx context.Context,
	cell *v1alpha1.Cell,
) (ctrl.Result, error) {
	node := &v1alpha1.Node{}
	nodeKey := client.ObjectKey{Name: cell.Spec.NodeRef.Name}
	if err := r.Get(ctx, nodeKey, node); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("getting node %s: %w", nodeKey, err)
		}

		return ctrl.Result{}, nil
	}
	if r.NodePredicate != nil && !r.NodePredicate(node) {
		return ctrl.Result{}, nil
	}

	ctx = ctrl.LoggerInto(ctx, ctrl.LoggerFrom(ctx, "node", nodeKey))

	if !cell.DeletionTimestamp.IsZero() {
		return r.delete(ctx, node, cell)
	}
	return r.reconcile(ctx, node, cell)
}

func (r *CellReconciler) delete(
	ctx context.Context,
	node *v1alpha1.Node,
	cell *v1alpha1.Cell,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	nodeCellRef := node.Spec.CellRef
	if nodeCellRef == nil {
		log.V(1).Info("Not bound to cell, nothing to do")
		return ctrl.Result{}, nil
	}

	if nodeCellRef.Namespace != cell.Namespace || nodeCellRef.Name != cell.Name || nodeCellRef.UID != cell.UID {
		log.V(1).Info("Node is bound to different cell")
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(cell, NodeFinalizer(node.Name)) {
		log.V(1).Info("Cell does not contain finalizer, nothing to do")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Deleting cell from runtime")
	if err := r.CellRuntime.DeleteCell(ctx, node.Name); err != nil {
		if !errors.Is(err, cellruntime.ErrNotFound) {
			return ctrl.Result{}, fmt.Errorf("deleting cell from runtime: %w", err)
		}

		if cell.Status.Phase != v1alpha1.CellExpired {
			log.V(1).Info("Expiring cell")
			return ctrl.Result{}, r.applyCellPhase(ctx, cell, v1alpha1.CellExpired)
		}

		log.V(1).Info("Cell expired & deleted from runtime, removing finalizer")
		base := cell.DeepCopy()
		controllerutil.RemoveFinalizer(cell, NodeFinalizer(node.Name))
		if err := r.Patch(ctx, cell, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
		}

		log.V(1).Info("Removed finalizer")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Issued cell deletion from runtime")
	return ctrl.Result{RequeueAfter: r.CellRuntimePollImmediateInterval}, nil
}

func (r *CellReconciler) patchClaimNode(ctx context.Context, node *v1alpha1.Node, cell *v1alpha1.Cell) error {
	base := node.DeepCopy()
	node.Spec.CellRef = &v1alpha1.NamespacedUIDReference{
		Namespace: cell.Namespace,
		Name:      cell.Name,
		UID:       cell.UID,
	}
	if err := r.Patch(ctx, node, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patching node: %w", err)
	}
	return nil
}

func (r *CellReconciler) applyCellPhase(ctx context.Context, cell *v1alpha1.Cell, phase v1alpha1.CellPhase) error {
	if phase == cell.Status.Phase {
		return nil
	}

	base := cell.DeepCopy()
	cell.Status.Phase = phase
	if err := r.Status().Patch(ctx, cell, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patching cell: %w", err)
	}
	return nil
}

func (r *CellReconciler) reconcile(
	ctx context.Context,
	node *v1alpha1.Node,
	cell *v1alpha1.Cell,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	nodeCellRef := node.Spec.CellRef
	if nodeCellRef == nil {
		if _, ok := terminalCellPhases[cell.Status.Phase]; ok {
			log.V(1).Info("Cell is in terminal phase, won't bind")
			return ctrl.Result{}, nil
		}

		log.V(1).Info("Claiming node")
		return ctrl.Result{}, r.patchClaimNode(ctx, node, cell)
	}

	if nodeCellRef.Namespace != cell.Namespace || nodeCellRef.Name != cell.Name || nodeCellRef.UID != cell.UID {
		log.V(1).Info("Node is bound to different cell")
		return ctrl.Result{}, r.applyCellPhase(ctx, cell, v1alpha1.CellPending)
	}

	if _, ok := terminalCellPhases[cell.Status.Phase]; ok {
		log.V(1).Info("Cell is in terminal phase, won't reconcile")
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(cell, NodeFinalizer(node.Name)) {
		log.V(1).Info("Adding finalizer to cell")

		base := cell.DeepCopy()
		controllerutil.AddFinalizer(cell, NodeFinalizer(node.Name))
		if err := r.Patch(ctx, cell, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("patching cell %s: %w", client.ObjectKeyFromObject(cell), err)
		}
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Getting cell status")
	status, err := r.CellRuntime.CellStatus(ctx, node.Name)
	if err != nil {
		if !errors.Is(err, cellruntime.ErrNotFound) {
			return ctrl.Result{}, fmt.Errorf("getting cell status: %w", err)
		}

		log.V(1).Info("Cell not found, resolving cell config")
		cfg, err := r.resolveCellConfig(ctx, cell)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("resolving cell config: %w", err)
		}

		log.V(1).Info("Applying cell")
		if err := r.CellRuntime.ApplyCell(ctx, node.Name, cfg); err != nil {
			if !errors.Is(err, cellruntime.TerminalError(nil)) {
				r.Eventf(cell, node, v1.EventTypeWarning, "ApplyCellError", "ApplyCell", "Error applying cell: %v", err)
				return ctrl.Result{}, fmt.Errorf("applying cell %s: %w", client.ObjectKeyFromObject(cell), err)
			}

			log.Error(err, "Encountered terminal error, setting cell to failed")
			r.Eventf(cell, node, v1.EventTypeWarning, "ApplyCellTerminalError", "ApplyCell", "Terminal error applying cell: %v", err)
			return ctrl.Result{}, r.applyCellPhase(ctx, cell, v1alpha1.CellFailed)
		}

		log.V(1).Info("Setting cell to pending")
		return ctrl.Result{RequeueAfter: r.CellRuntimePollImmediateInterval}, r.applyCellPhase(ctx, cell, v1alpha1.CellPending)
	}

	log.V(1).Info("Getting cell status")
	var (
		cellPhase    v1alpha1.CellPhase
		requeueAfter time.Duration
	)
	switch status.Phase {
	case cellruntime.CellPhaseCreated:
		cellPhase = v1alpha1.CellPending
		requeueAfter = r.CellRuntimePollImmediateInterval
	case cellruntime.CellPhaseActive:
		cellPhase = v1alpha1.CellActive
		requeueAfter = r.CellRuntimePollInterval
	case cellruntime.CellPhaseError:
		cellPhase = v1alpha1.CellFailed
		requeueAfter = r.CellRuntimePollInterval
	}

	log.V(1).Info("Applied cell, applying cell phase", "Phase", cellPhase, "RequeueAfter", requeueAfter)
	return ctrl.Result{RequeueAfter: requeueAfter}, r.applyCellPhase(ctx, cell, cellPhase)
}

func (r *CellReconciler) resolveCellConfig(
	ctx context.Context,
	cell *v1alpha1.Cell,
) (*cellruntime.CellConfig, error) {
	ips := make([]netip.Addr, 0, len(cell.Spec.IPs))
	for _, ip := range cell.Spec.IPs {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return nil, fmt.Errorf("parsing IP address %s: %w", ip, err)
		}

		ips = append(ips, addr)
	}

	prefixes := make([]netip.Prefix, 0, len(cell.Spec.Prefixes))
	for _, prefix := range cell.Spec.Prefixes {
		pfx, err := netip.ParsePrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("parsing prefix %s: %w", prefix, err)
		}

		prefixes = append(prefixes, pfx)
	}

	peers := make([]cellruntime.Peer, 0, len(cell.Spec.Peers))
	for _, peer := range cell.Spec.Peers {
		iface := &v1alpha1.Interface{}
		ifaceName := peer.InterfaceRef.Name
		if err := r.Get(ctx, client.ObjectKey{Name: ifaceName}, iface); err != nil {
			return nil, fmt.Errorf("getting interface %s: %w", ifaceName, err)
		}

		ifaceID, ok := strings.CutPrefix(iface.Spec.Handle, fmt.Sprintf("%s://", r.CellRuntime.ProviderName()))
		if !ok {
			return nil, fmt.Errorf("interface %s handle %q is invalid", ifaceName, iface.Spec.Handle)
		}

		if iface.Spec.NodeRef != cell.Spec.NodeRef {
			return nil, fmt.Errorf("interface %s references different node %s", ifaceName, iface.Spec.NodeRef)
		}

		peers = append(peers, cellruntime.Peer{
			Interface: &cellruntime.Interface{
				Metadata: cellruntime.InterfaceMetadata{
					Name: ifaceName,
					UID:  string(iface.UID),
				},
				ID: ifaceID,
			},
			DHCPRelay: peer.DHCPRelay,
		})
	}

	return &cellruntime.CellConfig{
		Metadata: cellruntime.CellMetadata{
			Namespace: cell.Namespace,
			Name:      cell.Name,
			UID:       string(cell.UID),
		},
		ID:          cell.Spec.ID,
		Hostname:    cell.Spec.Hostname,
		LoopbackIPs: ips,
		Prefixes:    prefixes,
		Peers:       peers,
	}, nil
}

const (
	cellNodeKey = ".spec.nodeRef.name"
)

func (r *CellReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.Cell{}, cellNodeKey, func(obj client.Object) []string {
		cell := obj.(*v1alpha1.Cell)
		nodeName := cell.Spec.NodeRef.Name
		return []string{nodeName}
	}); err != nil {
		return fmt.Errorf("indexing cell node field: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(
			&v1alpha1.Cell{},
		).
		Watches(
			&v1alpha1.Node{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				node := obj.(*v1alpha1.Node)

				cellList := &v1alpha1.CellList{}
				if err := r.List(ctx, cellList, client.MatchingFields{cellNodeKey: node.Name}); err != nil {
					return nil
				}

				requests := make([]reconcile.Request, 0, len(cellList.Items))
				for _, cell := range cellList.Items {
					requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&cell)})
				}
				return requests
			}),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				node := obj.(*v1alpha1.Node)
				if r.NodePredicate != nil {
					return r.NodePredicate(node)
				}
				return true
			})),
		).
		Complete(r)
}
