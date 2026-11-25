// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strconv"
	"testing"

	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// These are deterministic, envtest-free unit tests for the CellConfigRollout
// decision helpers. They pin the behaviors behind the bugs found during lab
// testing so they cannot regress:
//   - budget is computed from the DESIRED config set, counting missing cells;
//   - Empty / Pending / Degraded / Failed phases are NOT counted as available;
//   - only Active counts as available (advances the roll);
//   - a drifted-but-down cell is gated, never deleted ungated.

const testCellConfigName = "leaf-node-1"

func readyInstance(enabled bool, hostname string) *fabricv1alpha1.CellConfigInstance { //nolint:unparam
	return instanceWithReady("a", enabled, hostname, metav1.ConditionTrue, "CellActive")
}

func instanceWithReady(name string, enabled bool, hostname string, status metav1.ConditionStatus, reason string) *fabricv1alpha1.CellConfigInstance {
	return &fabricv1alpha1.CellConfigInstance{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec: fabricv1alpha1.CellConfigInstanceSpec{
			Enabled:        enabled,
			CellConfigSpec: fabricv1alpha1.CellConfigSpec{Hostname: hostname},
		},
		Status: fabricv1alpha1.CellConfigInstanceStatus{
			Conditions: []metav1.Condition{{
				Type:   fabricv1alpha1.CellConfigInstanceConditionReady,
				Status: status,
				Reason: reason,
			}},
		},
	}
}

func cfg(name, hostname string) *fabricv1alpha1.CellConfig { //nolint:unparam
	return &fabricv1alpha1.CellConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec:       fabricv1alpha1.CellConfigSpec{Hostname: hostname},
	}
}

// phaseToReady mirrors how the CellConfigInstance controller derives the Ready
// condition from a Cell phase, so we can assert availability per phase.
func instanceForPhase(name string, phase fabricv1alpha1.CellPhase) *fabricv1alpha1.CellConfigInstance {
	status, reason, _ := readinessFromCellPhase(phase)
	return instanceWithReady(name, true, "h", status, reason)
}

func TestReadinessFromCellPhase_OnlyActiveIsReady(t *testing.T) {
	for _, tc := range []struct {
		phase     fabricv1alpha1.CellPhase
		wantReady bool
	}{
		{fabricv1alpha1.CellActive, true},
		{fabricv1alpha1.CellPending, false},
		{fabricv1alpha1.CellDegraded, false},
		{fabricv1alpha1.CellFailed, false},
		{fabricv1alpha1.CellExpired, false},
		{fabricv1alpha1.CellPhase(""), false}, // empty/unknown phase must not be "ready"
		{fabricv1alpha1.CellPhase("Bogus"), false},
	} {
		status, _, _ := readinessFromCellPhase(tc.phase)
		gotReady := status == metav1.ConditionTrue
		if gotReady != tc.wantReady {
			t.Errorf("phase %q: ready=%v, want %v", tc.phase, gotReady, tc.wantReady)
		}
	}
}

func TestIsAvailable_OnlyReadyTrue(t *testing.T) {
	if !isAvailable(readyInstance(true, "h")) {
		t.Error("Ready=True instance should be available")
	}
	for _, phase := range []fabricv1alpha1.CellPhase{
		fabricv1alpha1.CellPending, fabricv1alpha1.CellDegraded,
		fabricv1alpha1.CellFailed, fabricv1alpha1.CellExpired, fabricv1alpha1.CellPhase(""),
	} {
		if isAvailable(instanceForPhase("a", phase)) {
			t.Errorf("phase %q instance must not be available", phase)
		}
	}
	// An instance with no conditions at all is not available.
	bare := &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{Name: "a"}}
	if isAvailable(bare) {
		t.Error("instance with no Ready condition must not be available")
	}
}

// currentRevInstance builds an instance named like the current revision of cfg
// (<cellconfig>-<revhash>) and labeled with its owning CellConfig, so it is
// grouped and recognized as cfg's current revision.
func currentRevInstance(c *fabricv1alpha1.CellConfig, enabled bool, phase fabricv1alpha1.CellPhase) *fabricv1alpha1.CellConfigInstance {
	status, reason, _ := readinessFromCellPhase(phase)
	inst := instanceWithReady(instanceName(c), enabled, c.Spec.Hostname, status, reason)
	inst.Labels = map[string]string{cellConfigNameLabel: c.Name}
	return inst
}

func TestCountUnavailableForCurrentRevision_FromDesiredNotActual(t *testing.T) {
	ca, cb, cc := cfg("a", "h"), cfg("b", "h"), cfg("c", "h")
	cfgs := []*fabricv1alpha1.CellConfig{ca, cb, cc}

	group := func(insts ...*fabricv1alpha1.CellConfigInstance) map[string][]*fabricv1alpha1.CellConfigInstance {
		m := map[string][]*fabricv1alpha1.CellConfigInstance{}
		for _, in := range insts {
			cell := instanceCellConfigName(in)
			m[cell] = append(m[cell], in)
		}
		return m
	}

	for _, tc := range []struct {
		name  string
		owned map[string][]*fabricv1alpha1.CellConfigInstance
		want  int
	}{
		{
			name:  "all current revisions present, enabled and Ready => 0 down",
			owned: group(currentRevInstance(ca, true, fabricv1alpha1.CellActive), currentRevInstance(cb, true, fabricv1alpha1.CellActive), currentRevInstance(cc, true, fabricv1alpha1.CellActive)),
			want:  0,
		},
		{
			name:  "a desired cell with no instances is not down (never provisioned, slot not consumed)",
			owned: group(currentRevInstance(ca, true, fabricv1alpha1.CellActive), currentRevInstance(cc, true, fabricv1alpha1.CellActive)), // b missing
			want:  0,
		},
		{
			name:  "all missing => 0 down (never provisioned, no slot consumed yet)",
			owned: map[string][]*fabricv1alpha1.CellConfigInstance{},
			want:  0,
		},
		{
			name:  "Degraded/Pending/empty current revisions count as down",
			owned: group(currentRevInstance(ca, true, fabricv1alpha1.CellDegraded), currentRevInstance(cb, true, fabricv1alpha1.CellPending), currentRevInstance(cc, true, fabricv1alpha1.CellPhase(""))),
			want:  3,
		},
		{
			name:  "a disabled current revision counts as down (only enabled+Ready is up)",
			owned: group(currentRevInstance(ca, false, fabricv1alpha1.CellActive), currentRevInstance(cb, true, fabricv1alpha1.CellActive), currentRevInstance(cc, true, fabricv1alpha1.CellActive)),
			want:  1,
		},
		{
			name: "an OLD-revision instance present but current revision missing => down",
			owned: func() map[string][]*fabricv1alpha1.CellConfigInstance {
				// cell "a" has only a stale revision instance (different hash), not its current one.
				stale := instanceWithReady("a-stalehash0", false, "OLD", metav1.ConditionFalse, "CellDegraded")
				stale.Labels = map[string]string{cellConfigNameLabel: "a"}
				return group(stale, currentRevInstance(cb, true, fabricv1alpha1.CellActive), currentRevInstance(cc, true, fabricv1alpha1.CellActive))
			}(),
			want: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := countUnavailableForCurrentRevision(tc.owned, cfgs); got != tc.want {
				t.Errorf("countUnavailableForCurrentRevision = %d, want %d", got, tc.want)
			}
		})
	}
}

func deleting(inst *fabricv1alpha1.CellConfigInstance) *fabricv1alpha1.CellConfigInstance {
	now := metav1.Now()
	inst.DeletionTimestamp = &now
	// a deleting object must carry a finalizer to be valid, but for the helper
	// only DeletionTimestamp matters.
	inst.Finalizers = []string{"test"}
	return inst
}

func TestIsReplacementInProgress(t *testing.T) {
	if isReplacementInProgress(readyInstance(true, "h")) {
		t.Error("a non-deleting instance is not mid-replacement")
	}
	if !isReplacementInProgress(deleting(readyInstance(true, "h"))) {
		t.Error("a deleting instance IS mid-replacement")
	}
}

func TestInstanceMatchesConfig(t *testing.T) {
	c := cfg("a", "h")
	if !instanceMatchesConfig(readyInstance(true, "h"), c) {
		t.Error("enabled instance with matching spec should match")
	}
	if instanceMatchesConfig(readyInstance(true, "OLD"), c) {
		t.Error("drifted hostname should not match")
	}
	if instanceMatchesConfig(readyInstance(false, "h"), c) {
		t.Error("disabled instance should not match")
	}
}

func TestResolveMaxUnavailable(t *testing.T) {
	mkRollout := func(mu *intstr.IntOrString) *fabricv1alpha1.CellConfigRollout {
		r := &fabricv1alpha1.CellConfigRollout{}
		if mu != nil {
			r.Spec.Strategy.RollingUpdate = &fabricv1alpha1.RollingUpdateCellConfig{MaxUnavailable: mu}
		}
		return r
	}
	one := intstr.FromInt32(1)
	two := intstr.FromInt32(2)
	zero := intstr.FromInt32(0)
	fifty := intstr.FromString("50%")

	if got := resolveMaxUnavailable(mkRollout(nil), 4); got != 1 {
		t.Errorf("omitted maxUnavailable should default to 1, got %d", got)
	}
	if got := resolveMaxUnavailable(mkRollout(&one), 4); got != 1 {
		t.Errorf("explicit 1 => 1, got %d", got)
	}
	if got := resolveMaxUnavailable(mkRollout(&two), 4); got != 2 {
		t.Errorf("explicit 2 => 2, got %d", got)
	}
	if got := resolveMaxUnavailable(mkRollout(&zero), 4); got != 0 {
		t.Errorf("explicit 0 must be honored (pause), got %d", got)
	}
	if got := resolveMaxUnavailable(mkRollout(&fifty), 4); got != 2 {
		t.Errorf("50%% of 4 => 2, got %d", got)
	}
}

func TestConfigRevisionHashAndInstanceName(t *testing.T) {
	c1 := cfg(testCellConfigName, "host-a")
	c1dup := cfg(testCellConfigName, "host-a")
	c2 := cfg(testCellConfigName, "host-b") // same cell, changed spec

	if configRevisionHash(c1) != configRevisionHash(c1dup) {
		t.Error("identical specs must hash identically")
	}
	if configRevisionHash(c1) == configRevisionHash(c2) {
		t.Error("a spec change must change the revision hash")
	}

	n1 := instanceName(c1)
	n2 := instanceName(c2)
	if n1 == n2 {
		t.Errorf("a spec change must yield a new instance name (got %q for both)", n1)
	}
	// Every instance name carries a hash suffix, including the first revision.
	if n1 == testCellConfigName {
		t.Error("instance name must always include a revision hash suffix")
	}

	// Grouping is by the cellConfigNameLabel, which is robust even though the
	// cell name itself contains '-'.
	labeled := &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{
		Name:   n1,
		Labels: map[string]string{cellConfigNameLabel: testCellConfigName},
	}}
	if got := instanceCellConfigName(labeled); got != testCellConfigName {
		t.Errorf("instanceCellConfigName (labeled) = %q, want leaf-node-1", got)
	}
	// Grouping is strictly label-based: no label => "" (instance is ignored).
	unlabeled := &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{Name: testCellConfigName}}
	if got := instanceCellConfigName(unlabeled); got != "" {
		t.Errorf("instanceCellConfigName (unlabeled) = %q, want \"\"", got)
	}
}

func TestInstanceRevisionAndMaxRevision(t *testing.T) {
	mk := func(name string, rev int64) *fabricv1alpha1.CellConfigInstance {
		return &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{cellConfigRevisionAnnotation: strconv.FormatInt(rev, 10)},
		}}
	}
	noAnno := &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	if instanceRevision(noAnno) != 0 {
		t.Error("missing revision annotation must default to 0")
	}
	if got := instanceRevision(mk("x", 7)); got != 7 {
		t.Errorf("instanceRevision = %d, want 7", got)
	}
	revs := []*fabricv1alpha1.CellConfigInstance{mk("a", 3), mk("b", 9), mk("c", 5), noAnno}
	if got := maxRevision(revs); got != 9 {
		t.Errorf("maxRevision = %d, want 9", got)
	}
	if got := maxRevision(nil); got != 0 {
		t.Errorf("maxRevision(nil) = %d, want 0", got)
	}
}

func TestResolveRevisionHistoryLimit(t *testing.T) {
	mk := func(v *int32) *fabricv1alpha1.CellConfigRollout {
		r := &fabricv1alpha1.CellConfigRollout{}
		r.Spec.RevisionHistoryLimit = v
		return r
	}
	if got := resolveRevisionHistoryLimit(mk(nil)); got != defaultRevisionHistoryLimit {
		t.Errorf("omitted => default %d, got %d", defaultRevisionHistoryLimit, got)
	}
	three := int32(3)
	if got := resolveRevisionHistoryLimit(mk(&three)); got != 3 {
		t.Errorf("explicit 3 => 3, got %d", got)
	}
	zero := int32(0)
	if got := resolveRevisionHistoryLimit(mk(&zero)); got != 0 {
		t.Errorf("explicit 0 => 0 (keep no history), got %d", got)
	}
	neg := int32(-1)
	if got := resolveRevisionHistoryLimit(mk(&neg)); got != defaultRevisionHistoryLimit {
		t.Errorf("negative => default, got %d", got)
	}
}

// TestInstanceCellConfigName_GroupingRobustness pins the fix for the grouping
// bug: cell names contain '-', so parsing the instance name is ambiguous.
// Grouping is strictly label-based — the instance name shape is irrelevant, and
// an unlabeled instance returns "" (and is ignored by the rollout).
func TestInstanceCellConfigName_GroupingRobustness(t *testing.T) {
	for _, tc := range []struct {
		name     string
		instName string
		label    string
		want     string
	}{
		{"labeled hashed instance", "leaf-node-1-07e93eb1a0", testCellConfigName, testCellConfigName},
		{"labeled multi-hyphen cell", "a-b-c-d-deadbeef12", "a-b-c-d", "a-b-c-d"},
		{"unlabeled instance is not grouped", testCellConfigName, "", ""},
		{"unlabeled single token is not grouped", "spine", "", ""},
		{"label is authoritative regardless of name", "whatever-xyz", "real-cell", "real-cell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{Name: tc.instName}}
			if tc.label != "" {
				in.Labels = map[string]string{cellConfigNameLabel: tc.label}
			}
			if got := instanceCellConfigName(in); got != tc.want {
				t.Errorf("instanceCellConfigName = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCountUnavailable_MixedRevisions ensures the budget treats a cell
// correctly when an OLD revision still serves while a NEW revision is being
// rolled: the cell is up while any revision is enabled+Ready, and down only
// when none is serving.
func TestCountUnavailable_MixedRevisions(t *testing.T) {
	caOld := cfg(testCellConfigName, "h-old")
	ca := cfg(testCellConfigName, "h-new")
	cfgs := []*fabricv1alpha1.CellConfig{ca}

	// Old revision enabled+Active (serving); new (current) revision not yet
	// created. The cell is still served, so NOT down.
	oldRev := currentRevInstance(caOld, true, fabricv1alpha1.CellActive)
	owned := map[string][]*fabricv1alpha1.CellConfigInstance{
		testCellConfigName: {oldRev},
	}
	if got := countUnavailableForCurrentRevision(owned, cfgs); got != 0 {
		t.Errorf("old revision still serving: down=%d, want 0", got)
	}

	// Old revision disabled (superseded); new revision exists but not Ready =>
	// cell is down.
	oldRev.Spec.Enabled = false
	newRev := currentRevInstance(ca, true, fabricv1alpha1.CellPending)
	owned = map[string][]*fabricv1alpha1.CellConfigInstance{
		testCellConfigName: {oldRev, newRev},
	}
	if got := countUnavailableForCurrentRevision(owned, cfgs); got != 1 {
		t.Errorf("superseded old + not-ready new revision: down=%d, want 1", got)
	}

	// New revision becomes Active => cell served again.
	newRev.Status.Conditions[0].Status = metav1.ConditionTrue
	newRev.Status.Conditions[0].Reason = "CellActive"
	if got := countUnavailableForCurrentRevision(owned, cfgs); got != 0 {
		t.Errorf("new revision Active: down=%d, want 0", got)
	}
}

// TestPrunableHistory validates which instances pruneHistory would remove:
// newest-first by revision, excluding the current/enabled/terminating ones, and
// only those beyond the limit.
func TestPrunableHistory(t *testing.T) {
	mkRev := func(hash string, rev int64, enabled bool) *fabricv1alpha1.CellConfigInstance {
		return &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{
			Name:        "cell-" + hash,
			Labels:      map[string]string{cellConfigNameLabel: "cell"},
			Annotations: map[string]string{cellConfigRevisionAnnotation: strconv.FormatInt(rev, 10)},
		}, Spec: fabricv1alpha1.CellConfigInstanceSpec{Enabled: enabled}}
	}
	keep := "cell-h5"
	revs := []*fabricv1alpha1.CellConfigInstance{
		mkRev("h5", 5, true), // current, enabled -> never pruned
		mkRev("h4", 4, false),
		mkRev("h3", 3, false),
		mkRev("h2", 2, false),
		mkRev("h1", 1, false),
	}
	names := func(in []*fabricv1alpha1.CellConfigInstance) []string {
		out := make([]string, 0, len(in))
		for _, i := range in {
			out = append(out, i.Name)
		}
		return out
	}

	// limit 2 => keep newest disabled rev4,rev3; prune rev2,rev1.
	got := names(prunableHistory(revs, keep, 2))
	want := []string{"cell-h2", "cell-h1"}
	if len(got) != len(want) {
		t.Fatalf("limit 2: pruned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("limit 2: pruned[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// limit 0 => prune all 4 disabled.
	if got := prunableHistory(revs, keep, 0); len(got) != 4 {
		t.Errorf("limit 0: pruned %v, want all 4", names(got))
	}
	// limit >= history => prune nothing.
	if got := prunableHistory(revs, keep, 10); got != nil {
		t.Errorf("limit 10: pruned %v, want none", names(got))
	}
	// the enabled current revision is never selected.
	for _, in := range prunableHistory(revs, keep, 0) {
		if in.Name == keep {
			t.Error("enabled current revision must never be pruned")
		}
	}
	// a terminating disabled revision is excluded from pruning.
	term := mkRev("h0", 0, false)
	now := metav1.Now()
	term.DeletionTimestamp = &now
	term.Finalizers = []string{"x"}
	withTerm := append([]*fabricv1alpha1.CellConfigInstance{term}, revs...)
	for _, in := range prunableHistory(withTerm, keep, 0) {
		if in.Name == "cell-h0" {
			t.Error("already-terminating revision must not be re-selected for pruning")
		}
	}
}
