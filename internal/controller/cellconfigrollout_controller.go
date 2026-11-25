// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// CellConfigRolloutReconciler reconciles a CellConfigRollout object.
type CellConfigRolloutReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfigrollouts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfigrollouts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfigrollouts/finalizers,verbs=update
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfiginstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=cellconfiginstances/status,verbs=get
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=fabric.ironcore.dev,resources=interfaces,verbs=get;list;watch

func (r *CellConfigRolloutReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rollout := &fabricv1alpha1.CellConfigRollout{}
	if err := r.Get(ctx, req.NamespacedName, rollout); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !rollout.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	return r.reconcile(ctx, rollout)
}

func (r *CellConfigRolloutReconciler) reconcile(
	ctx context.Context,
	rollout *fabricv1alpha1.CellConfigRollout,
) (ctrl.Result, error) {
	selector, err := metav1.LabelSelectorAsSelector(rollout.Spec.ConfigSelector)
	if err != nil {
		return ctrl.Result{}, r.setProgressing(ctx, rollout, metav1.ConditionFalse, "InvalidSelector",
			fmt.Sprintf("configSelector is invalid: %v", err))
	}

	configList := &fabricv1alpha1.CellConfigList{}
	if err := r.List(ctx, configList,
		client.InNamespace(rollout.Namespace),
		client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing CellConfigs for CellConfigRollout %s: %w",
			client.ObjectKeyFromObject(rollout), err)
	}

	valid := make([]*fabricv1alpha1.CellConfig, 0, len(configList.Items))
	var invalid []string
	for i := range configList.Items {
		cfg := &configList.Items[i]
		if err := r.validateConfig(ctx, cfg); err != nil {
			invalid = append(invalid, fmt.Sprintf("%s (%s)", cfg.Name, err))
			continue
		}
		valid = append(valid, cfg)
	}

	ownedList := &fabricv1alpha1.CellConfigInstanceList{}
	if err := r.List(ctx, ownedList,
		client.InNamespace(rollout.Namespace),
		client.MatchingFields{cellConfigInstanceOwnerKey: rollout.Name}); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing CellConfigInstances for CellConfigRollout %s: %w",
			client.ObjectKeyFromObject(rollout), err)
	}

	ownedByCell := make(map[string][]*fabricv1alpha1.CellConfigInstance)
	for i := range ownedList.Items {
		inst := &ownedList.Items[i]
		cellName := instanceCellConfigName(inst)
		if cellName == "" {
			continue
		}
		ownedByCell[cellName] = append(ownedByCell[cellName], inst)
	}

	desiredNames := make(map[string]struct{}, len(valid))
	for _, cfg := range valid {
		desiredNames[cfg.Name] = struct{}{}
	}

	// Compute the rollout budget. maxUnavailable bounds how many desired cells
	// may be "down" (not Active) at once. The budget is desired-based: a cell is
	// down when its current-revision instance is missing, being deleted, or not
	// Ready, so a cell stays counted as down through its whole create -> provision
	// cycle and the next cell cannot start until this one is back.
	maxUnavailable := resolveMaxUnavailable(rollout, len(valid))
	historyLimit := resolveRevisionHistoryLimit(rollout)
	downNow := countUnavailableForCurrentRevision(ownedByCell, valid)

	requeue := false

	for _, cfg := range valid {
		rolled, requeueCell, err := r.rollCell(ctx, rollout, cfg, ownedByCell[cfg.Name], &downNow, maxUnavailable)
		if err != nil {
			return ctrl.Result{}, err
		}
		if requeueCell {
			requeue = true
		}
		if rolled {
			return r.updateStatus(ctx, rollout, valid, ownedList.Items, invalid, true)
		}
	}

	for _, cfg := range valid {
		if pruned, err := r.pruneHistory(ctx, ownedByCell[cfg.Name], instanceName(cfg), historyLimit); err != nil {
			return ctrl.Result{}, err
		} else if pruned {
			requeue = true
		}
	}

	for cellName, revisions := range ownedByCell {
		if _, keep := desiredNames[cellName]; keep {
			continue
		}
		for _, inst := range revisions {
			if !inst.DeletionTimestamp.IsZero() {
				requeue = true
				continue
			}
			if inst.Spec.Enabled {
				if downNow >= maxUnavailable {
					requeue = true
					continue
				}
				if isAvailable(inst) {
					downNow++
				}
			}
			if err := r.Delete(ctx, inst); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, fmt.Errorf("deleting CellConfigInstance %s: %w",
					client.ObjectKeyFromObject(inst), err)
			}
			requeue = true
		}
	}

	return r.updateStatus(ctx, rollout, valid, ownedList.Items, invalid, requeue)
}

// rollCell processes a single desired CellConfig: ensures the current revision
// is materialized and enabled, gates new rolls by the budget, and disables
// superseded revisions. It returns (rolled=true) when it acted on this cell
// (the caller must return early to let the cache catch up), (requeue=true)
// when the cell is provisioning and needs a follow-up reconcile, or an error.
func (r *CellConfigRolloutReconciler) rollCell(
	ctx context.Context,
	rollout *fabricv1alpha1.CellConfigRollout,
	cfg *fabricv1alpha1.CellConfig,
	revisions []*fabricv1alpha1.CellConfigInstance,
	downNow *int,
	maxUnavailable int,
) (rolled bool, requeue bool, err error) {
	log := ctrl.LoggerFrom(ctx)
	want := instanceName(cfg)

	var current *fabricv1alpha1.CellConfigInstance
	for _, inst := range revisions {
		if inst.Name == want {
			current = inst
			break
		}
	}

	// Enabled revisions other than the desired one must eventually be
	// superseded (disabled). Their presence means a roll is needed.
	var staleEnabled []*fabricv1alpha1.CellConfigInstance
	for _, inst := range revisions {
		if inst.Name != want && inst.Spec.Enabled {
			staleEnabled = append(staleEnabled, inst)
		}
	}

	// A cell is "already up to date" when its current revision exists, is
	// enabled, and nothing else is still enabled.
	currentEnabled := current != nil && current.Spec.Enabled
	rollNeeded := current == nil || !currentEnabled || len(staleEnabled) > 0

	if !rollNeeded {
		if !isAvailable(current) {
			return false, true, nil // provisioning on the current revision; wait.
		}
		return false, false, nil
	}

	// A roll is needed. Gate it by the budget.
	//
	// Two distinct cases when cellServing is false:
	// 1. current CCI exists and is enabled but not yet Ready (mid-roll): already
	//    counted in downNow seed; allow it to continue without an extra slot.
	// 2. current CCI does not exist yet (never provisioned, or prior rollout left
	//    it not-serving): must gate so multiple such cells don't all roll at once.
	cellServing := cellHasServingRevision(revisions)
	currentMidRoll := current != nil && current.Spec.Enabled && !isAvailable(current)
	if cellServing || !currentMidRoll {
		if *downNow >= maxUnavailable {
			log.Info("Cell roll gated by budget", "cell", cfg.Name, "downNow", *downNow, "maxUnavailable", maxUnavailable)
			return false, true, nil
		}
		if cellServing {
			*downNow++
		}
	}

	// Within the budget: perform the roll for this cell.
	if current == nil {
		nextRev := maxRevision(revisions) + 1
		if err := r.createInstance(ctx, rollout, cfg, want, nextRev); err != nil {
			return false, false, err
		}
	} else if !current.Spec.Enabled {
		if err := r.setInstanceEnabled(ctx, current, true); err != nil {
			return false, false, err
		}
	}
	// Disable superseded revisions (frees the Node so the new Cell can bind).
	for _, inst := range staleEnabled {
		if err := r.setInstanceEnabled(ctx, inst, false); err != nil {
			return false, false, err
		}
	}
	// Return rolled=true so the caller returns immediately, allowing the cache
	// to catch up before the next reconcile decides on the next cell.
	log.Info("Rolled cell, returning to allow cache to catch up", "cell", cfg.Name)
	return true, false, nil
}

func (r *CellConfigRolloutReconciler) validateConfig(
	ctx context.Context,
	cfg *fabricv1alpha1.CellConfig,
) error {
	if cfg.Spec.NodeRef.Name == "" {
		return fmt.Errorf("nodeRef is empty")
	}
	node := &fabricv1alpha1.Node{}
	if err := r.Get(ctx, client.ObjectKey{Name: cfg.Spec.NodeRef.Name}, node); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("node %q not found", cfg.Spec.NodeRef.Name)
		}
		return fmt.Errorf("getting Node %q: %w", cfg.Spec.NodeRef.Name, err)
	}
	for _, peer := range cfg.Spec.Peers {
		if peer.InterfaceRef.Name == "" {
			return fmt.Errorf("peer interfaceRef is empty")
		}
		iface := &fabricv1alpha1.Interface{}
		if err := r.Get(ctx, client.ObjectKey{Name: peer.InterfaceRef.Name}, iface); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("interface %q not found", peer.InterfaceRef.Name)
			}
			return fmt.Errorf("getting Interface %q: %w", peer.InterfaceRef.Name, err)
		}
	}
	return nil
}

func (r *CellConfigRolloutReconciler) createInstance(
	ctx context.Context,
	rollout *fabricv1alpha1.CellConfigRollout,
	cfg *fabricv1alpha1.CellConfig,
	name string,
	revision int64,
) error {
	instanceLabels := copyLabels(cfg.Labels)
	if instanceLabels == nil {
		instanceLabels = map[string]string{}
	}

	instanceLabels[cellConfigNameLabel] = cfg.Name
	instance := &fabricv1alpha1.CellConfigInstance{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   rollout.Namespace,
			Name:        name,
			Labels:      instanceLabels,
			Annotations: map[string]string{cellConfigRevisionAnnotation: strconv.FormatInt(revision, 10)},
		},
		Spec: fabricv1alpha1.CellConfigInstanceSpec{
			Enabled:        true,
			CellConfigSpec: cfg.Spec,
		},
	}
	if err := controllerutil.SetControllerReference(rollout, instance, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on CellConfigInstance %s: %w", name, err)
	}
	if err := r.Create(ctx, instance); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating CellConfigInstance %s: %w", client.ObjectKeyFromObject(instance), err)
	}
	return nil
}

func (r *CellConfigRolloutReconciler) setInstanceEnabled(
	ctx context.Context,
	instance *fabricv1alpha1.CellConfigInstance,
	enabled bool,
) error {
	if instance.Spec.Enabled == enabled {
		return nil
	}
	base := instance.DeepCopy()
	instance.Spec.Enabled = enabled
	if err := r.Patch(ctx, instance, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("setting enabled=%t on CellConfigInstance %s: %w",
			enabled, client.ObjectKeyFromObject(instance), err)
	}
	return nil
}

func (r *CellConfigRolloutReconciler) pruneHistory(
	ctx context.Context,
	revisions []*fabricv1alpha1.CellConfigInstance,
	keepName string,
	historyLimit int,
) (bool, error) {
	pruned := false
	for _, inst := range prunableHistory(revisions, keepName, historyLimit) {
		if err := r.Delete(ctx, inst); client.IgnoreNotFound(err) != nil {
			return pruned, fmt.Errorf("pruning CellConfigInstance %s: %w",
				client.ObjectKeyFromObject(inst), err)
		}
		pruned = true
	}
	return pruned, nil
}

func maxRevision(revisions []*fabricv1alpha1.CellConfigInstance) int64 {
	var max int64
	for _, inst := range revisions {
		if rev := instanceRevision(inst); rev > max {
			max = rev
		}
	}
	return max
}

func (r *CellConfigRolloutReconciler) updateStatus(
	ctx context.Context,
	rollout *fabricv1alpha1.CellConfigRollout,
	valid []*fabricv1alpha1.CellConfig,
	instances []fabricv1alpha1.CellConfigInstance,
	invalid []string,
	requeue bool,
) (ctrl.Result, error) {
	base := rollout.DeepCopy()

	byName := make(map[string]*fabricv1alpha1.CellConfigInstance, len(instances))
	for i := range instances {
		byName[instances[i].Name] = &instances[i]
	}
	var updated, available, failed int32
	for _, cfg := range valid {
		current := byName[instanceName(cfg)]
		if current == nil {
			continue
		}
		if current.Spec.Enabled {
			updated++
		}
		if isAvailable(current) {
			available++
		}
		if isFailed(current) {
			failed++
		}
	}

	rollout.Status.ObservedGeneration = rollout.Generation
	rollout.Status.DesiredCells = int32(len(valid))
	rollout.Status.UpdatedCells = updated
	rollout.Status.AvailableCells = available
	rollout.Status.FailedCells = failed

	hash := hashConfigs(valid)
	if rollout.Status.CurrentRevisionHash != hash {
		rollout.Status.CurrentRevision++
		rollout.Status.CurrentRevisionHash = hash
	}

	progressing := updated < rollout.Status.DesiredCells || requeue
	if progressing {
		meta.SetStatusCondition(&rollout.Status.Conditions, metav1.Condition{
			Type:               fabricv1alpha1.CellConfigRolloutConditionProgressing,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: rollout.Generation,
			Reason:             "RollingUpdateInProgress",
			Message:            progressingMessage(invalid),
		})
	} else {
		meta.SetStatusCondition(&rollout.Status.Conditions, metav1.Condition{
			Type:               fabricv1alpha1.CellConfigRolloutConditionProgressing,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: rollout.Generation,
			Reason:             "RolloutComplete",
			Message:            progressingMessage(invalid),
		})
	}

	healthyStatus := metav1.ConditionTrue
	healthyReason := "WithinFailureBudget"
	healthyMessage := "No failed cells"
	if failed > 0 {
		healthyStatus = metav1.ConditionFalse
		healthyReason = "CellsFailed"
		healthyMessage = fmt.Sprintf("%d cell(s) reported phase Failed", failed)
	}
	meta.SetStatusCondition(&rollout.Status.Conditions, metav1.Condition{
		Type:               fabricv1alpha1.CellConfigRolloutConditionHealthy,
		Status:             healthyStatus,
		ObservedGeneration: rollout.Generation,
		Reason:             healthyReason,
		Message:            healthyMessage,
	})

	if err := r.Status().Patch(ctx, rollout, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status of CellConfigRollout %s: %w",
			client.ObjectKeyFromObject(rollout), err)
	}

	if requeue {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func (r *CellConfigRolloutReconciler) setProgressing(
	ctx context.Context,
	rollout *fabricv1alpha1.CellConfigRollout,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	base := rollout.DeepCopy()
	if !meta.SetStatusCondition(&rollout.Status.Conditions, metav1.Condition{
		Type:               fabricv1alpha1.CellConfigRolloutConditionProgressing,
		Status:             status,
		ObservedGeneration: rollout.Generation,
		Reason:             reason,
		Message:            message,
	}) {
		return nil
	}
	if err := r.Status().Patch(ctx, rollout, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("updating progressing condition of CellConfigRollout %s: %w",
			client.ObjectKeyFromObject(rollout), err)
	}
	return nil
}

func (r *CellConfigRolloutReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cellconfigrollout").
		For(&fabricv1alpha1.CellConfigRollout{}).
		Owns(&fabricv1alpha1.CellConfigInstance{}).
		Watches(&fabricv1alpha1.CellConfig{}, handler.EnqueueRequestsFromMapFunc(r.rolloutsForCellConfig)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		Complete(r)
}

func (r *CellConfigRolloutReconciler) rolloutsForCellConfig(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	cfg := obj.(*fabricv1alpha1.CellConfig)

	rollouts := &fabricv1alpha1.CellConfigRolloutList{}
	if err := r.List(ctx, rollouts, client.InNamespace(cfg.Namespace)); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Could not list CellConfigRollouts for CellConfig", "cellConfig", cfg.Name)
		return nil
	}

	var requests []reconcile.Request
	for i := range rollouts.Items {
		rollout := &rollouts.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(rollout.Spec.ConfigSelector)
		if err != nil {
			continue
		}
		if selector.Matches(labels.Set(cfg.Labels)) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rollout)})
		}
	}
	return requests
}
