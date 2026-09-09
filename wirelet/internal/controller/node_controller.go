// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/cellruntime"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/lru"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type NodeReconciler struct {
	client.Client
	APIReader                        client.Reader
	CellRuntime                      cellruntime.Runtime
	NodePredicate                    func(*v1alpha1.Node) bool
	AbsenceCache                     *lru.Cache
	CellRuntimePollImmediateInterval time.Duration
}

// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=nodes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=nodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=interfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=wire.ironcore.dev,resources=cells,verbs=get;list;watch

func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	node := &v1alpha1.Node{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.NodePredicate != nil && !r.NodePredicate(node) {
		return ctrl.Result{}, nil
	}
	if !node.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if node.Spec.ProviderID == "" {
		log.Info("Determining and setting provider ID on node")
		nodeID, err := r.CellRuntime.NodeID(ctx, node.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("getting node %s handle: %w", node.Name, err)
		}

		return ctrl.Result{}, r.setNodeProviderID(ctx, node, fmt.Sprintf("%s://%s", r.CellRuntime.ProviderName(), nodeID))
	}

	cellRef := node.Spec.CellRef
	if cellRef == nil {
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Checking if cell exists")
	ok, err := r.nodeCellExists(ctx, node)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("checking if cell exists: %w", err)
	}
	if ok {
		log.V(1).Info("Cell is present")
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Cell does not exist, deleting runtime cell (if any)")
	if err := r.CellRuntime.DeleteCell(ctx, node.Name); err != nil {
		if !errors.Is(err, cellruntime.ErrNotFound) {
			return ctrl.Result{}, fmt.Errorf("deleting runtime cell %s: %w", node.Name, err)
		}

		log.V(1).Info("No runtime cell exists, releasing node")
		return ctrl.Result{}, r.releaseNode(ctx, node)
	}

	log.V(1).Info("Issued runtime cell deletion")
	return ctrl.Result{RequeueAfter: r.CellRuntimePollImmediateInterval}, nil
}

func (r *NodeReconciler) releaseNode(ctx context.Context, node *v1alpha1.Node) error {
	base := node.DeepCopy()
	node.Spec.CellRef = nil
	return r.Patch(ctx, node, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *NodeReconciler) nodeCellExists(ctx context.Context, node *v1alpha1.Node) (bool, error) {
	cellRef := node.Spec.CellRef
	if _, ok := r.AbsenceCache.Get(cellRef.UID); ok {
		return false, nil
	}

	cell := &v1alpha1.Cell{}
	cellKey := client.ObjectKey{Namespace: cellRef.Namespace, Name: cellRef.Name}
	if err := r.APIReader.Get(ctx, cellKey, cell); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error getting cell %s: %w", cellKey, err)
		}

		r.AbsenceCache.Add(cellRef.UID, nil)
		return false, nil
	}
	return true, nil
}

func (r *NodeReconciler) setNodeProviderID(
	ctx context.Context,
	node *v1alpha1.Node,
	providerID string,
) error {
	base := node.DeepCopy()
	node.Spec.ProviderID = providerID
	if err := r.Patch(ctx, node, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("setting node provider id: %w", err)
	}
	return nil
}

func (r *NodeReconciler) enqueueByCell() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrl.LoggerFrom(ctx)

		cell := obj.(*v1alpha1.Cell)

		nodeList := &v1alpha1.NodeList{}
		if err := r.List(ctx, nodeList, client.MatchingFields{nodeCellKey: client.ObjectKeyFromObject(cell).String()}); err != nil {
			log.Error(err, "Error listing nodes for cell")
			return nil
		}

		var reqs []reconcile.Request
		for _, node := range nodeList.Items {
			cellRef := node.Spec.CellRef
			if cellRef == nil {
				continue
			}

			if cellRef.UID != cell.UID {
				continue
			}

			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&node)})
		}
		return reqs
	})
}

const nodeCellKey = ".spec.cellRef.{namespace,name}"

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1alpha1.Node{}, nodeCellKey, func(obj client.Object) []string {
		node := obj.(*v1alpha1.Node)
		cellRef := node.Spec.CellRef
		if cellRef == nil {
			return nil
		}
		return []string{(client.ObjectKey{Namespace: cellRef.Namespace, Name: cellRef.Name}).String()}
	}); err != nil {
		return fmt.Errorf("indexing node %s: %w", nodeCellKey, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(
			&v1alpha1.Node{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				node := obj.(*v1alpha1.Node)
				if r.NodePredicate != nil {
					return r.NodePredicate(node)
				}
				return true
			})),
		).
		Watches(
			&v1alpha1.Cell{},
			r.enqueueByCell(),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				cell := obj.(*v1alpha1.Cell)
				return !cell.DeletionTimestamp.IsZero()
			})),
		).
		Complete(r)
}
