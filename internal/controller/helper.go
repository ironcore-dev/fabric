// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strconv"

	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// cellConfigRevisionAnnotation records the per-cell revision ordinal on a
	// CellConfigInstance.
	cellConfigRevisionAnnotation = "fabric.ironcore.dev/revision"

	// cellConfigNameLabel records the name of the CellConfig a CellConfigInstance
	// was materialized from.
	cellConfigNameLabel = "fabric.ironcore.dev/cellconfig"

	// defaultRevisionHistoryLimit is a default revision history limit.
	defaultRevisionHistoryLimit = 10
)

// ownerNameForKind returns [name] of the controlling owner of the given kind in
// the fabric API group, or nil if there is none.
func ownerNameForKind(obj client.Object, kind string) []string {
	owner := metav1.GetControllerOf(obj)
	if owner == nil {
		return nil
	}
	gv, err := schema.ParseGroupVersion(owner.APIVersion)
	if err != nil || gv.Group != fabricv1alpha1.GroupVersion.Group || owner.Kind != kind {
		return nil
	}
	return []string{owner.Name}
}

// cellSpecFromConfig copies the shared configuration fields into a CellSpec.
func cellSpecFromConfig(cfg fabricv1alpha1.CellConfigSpec) fabricv1alpha1.CellSpec {
	return fabricv1alpha1.CellSpec(cfg)
}

// readinessFromCellPhase maps a Cell phase to the instance Ready condition.
func readinessFromCellPhase(phase fabricv1alpha1.CellPhase) (metav1.ConditionStatus, string, string) {
	switch phase {
	case fabricv1alpha1.CellActive:
		return metav1.ConditionTrue, "CellActive", "Cell has reached phase Active"
	case fabricv1alpha1.CellDegraded:
		return metav1.ConditionFalse, "CellDegraded", "Cell was Active but no longer satisfies its readiness checks"
	case fabricv1alpha1.CellFailed:
		return metav1.ConditionFalse, "CellFailed", "Cell has reached phase Failed"
	case fabricv1alpha1.CellExpired:
		return metav1.ConditionFalse, "CellExpired", "Cell has reached phase Expired"
	default:
		return metav1.ConditionFalse, "CellNotActive", "Cell has not yet reached phase Active"
	}
}

// resolveMaxUnavailable returns the max number of cells that may be down at once.
func resolveMaxUnavailable(rollout *fabricv1alpha1.CellConfigRollout, desired int) int {
	if rollout.Spec.Strategy.RollingUpdate == nil || rollout.Spec.Strategy.RollingUpdate.MaxUnavailable == nil {
		return 1
	}
	mu, err := intstr.GetScaledValueFromIntOrPercent(rollout.Spec.Strategy.RollingUpdate.MaxUnavailable, desired, false)
	if err != nil || mu < 0 {
		return 1
	}
	return mu
}

func resolveRevisionHistoryLimit(rollout *fabricv1alpha1.CellConfigRollout) int {
	if rollout.Spec.RevisionHistoryLimit == nil {
		return defaultRevisionHistoryLimit
	}
	if *rollout.Spec.RevisionHistoryLimit < 0 {
		return defaultRevisionHistoryLimit
	}
	return int(*rollout.Spec.RevisionHistoryLimit)
}

func configRevisionHash(cfg *fabricv1alpha1.CellConfig) string {
	data, _ := json.Marshal(cfg.Spec)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])[:10]
}

func instanceName(cfg *fabricv1alpha1.CellConfig) string {
	return fmt.Sprintf("%s-%s", cfg.Name, configRevisionHash(cfg))
}

func instanceCellConfigName(instance *fabricv1alpha1.CellConfigInstance) string {
	return instance.Labels[cellConfigNameLabel]
}

func instanceRevision(instance *fabricv1alpha1.CellConfigInstance) int64 {
	if v, ok := instance.Annotations[cellConfigRevisionAnnotation]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func prunableHistory(
	revisions []*fabricv1alpha1.CellConfigInstance,
	keepName string,
	historyLimit int,
) []*fabricv1alpha1.CellConfigInstance {
	var history []*fabricv1alpha1.CellConfigInstance
	for _, inst := range revisions {
		if inst.Name == keepName || inst.Spec.Enabled || !inst.DeletionTimestamp.IsZero() {
			continue
		}
		history = append(history, inst)
	}
	sort.Slice(history, func(i, j int) bool {
		return instanceRevision(history[i]) > instanceRevision(history[j])
	})
	if historyLimit >= len(history) {
		return nil
	}
	return history[historyLimit:]
}

func countUnavailableForCurrentRevision(
	ownedByCell map[string][]*fabricv1alpha1.CellConfigInstance,
	valid []*fabricv1alpha1.CellConfig,
) int {
	count := 0
	for _, cfg := range valid {
		revisions := ownedByCell[cfg.Name]
		if len(revisions) == 0 {
			// Never provisioned; no slot consumed yet.
			continue
		}
		if !cellHasServingRevision(revisions) {
			count++
		}
	}
	return count
}

func isAvailable(inst *fabricv1alpha1.CellConfigInstance) bool {
	return meta.IsStatusConditionTrue(inst.Status.Conditions, fabricv1alpha1.CellConfigInstanceConditionReady)
}

func cellHasServingRevision(revisions []*fabricv1alpha1.CellConfigInstance) bool {
	for _, inst := range revisions {
		if inst.Spec.Enabled && inst.DeletionTimestamp.IsZero() && isAvailable(inst) {
			return true
		}
	}
	return false
}

func isReplacementInProgress(inst *fabricv1alpha1.CellConfigInstance) bool {
	return !inst.DeletionTimestamp.IsZero()
}

func isFailed(inst *fabricv1alpha1.CellConfigInstance) bool {
	cond := meta.FindStatusCondition(inst.Status.Conditions, fabricv1alpha1.CellConfigInstanceConditionReady)
	return cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason == "CellFailed"
}

func instanceMatchesConfig(inst *fabricv1alpha1.CellConfigInstance, cfg *fabricv1alpha1.CellConfig) bool {
	return inst.Spec.Enabled && equalConfigSpec(inst.Spec.CellConfigSpec, cfg.Spec)
}

func equalConfigSpec(a, b fabricv1alpha1.CellConfigSpec) bool {
	if a.NodeRef != b.NodeRef || a.ID != b.ID || a.Hostname != b.Hostname {
		return false
	}
	if !equalStrings(a.IPs, b.IPs) || !equalStrings(a.Prefixes, b.Prefixes) {
		return false
	}
	if len(a.Peers) != len(b.Peers) {
		return false
	}
	for i := range a.Peers {
		if a.Peers[i] != b.Peers[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func copyLabels(in map[string]string) map[string]string {
	return maps.Clone(in)
}

func hashConfigs(configs []*fabricv1alpha1.CellConfig) string {
	entries := make([]string, 0, len(configs))
	for _, cfg := range configs {
		entries = append(entries, fmt.Sprintf("%s=%+v", cfg.Name, cfg.Spec))
	}
	sort.Strings(entries)
	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func progressingMessage(invalid []string) string {
	if len(invalid) == 0 {
		return "Reconciling selected CellConfigs"
	}
	return fmt.Sprintf("Skipped invalid CellConfigs: %v", invalid)
}
