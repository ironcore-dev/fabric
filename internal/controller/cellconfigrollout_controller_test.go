// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("CellConfigRolloutReconciler", func() {
	const rolloutNS = defaultNamespace

	newConfig := func(name, nodeName string, labels map[string]string) *fabricv1alpha1.CellConfig {
		return &fabricv1alpha1.CellConfig{
			ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: name, Labels: labels},
			Spec: fabricv1alpha1.CellConfigSpec{
				NodeRef:  fabricv1alpha1.LocalObjectReference{Name: nodeName},
				ID:       "1",
				Hostname: name,
			},
		}
	}

	newRollout := func(name string, selector map[string]string) *fabricv1alpha1.CellConfigRollout {
		return &fabricv1alpha1.CellConfigRollout{
			ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: name},
			Spec: fabricv1alpha1.CellConfigRolloutSpec{
				ConfigSelector: &metav1.LabelSelector{MatchLabels: selector},
			},
		}
	}

	currentInstance := func(cfg *fabricv1alpha1.CellConfig) *fabricv1alpha1.CellConfigInstance {
		return &fabricv1alpha1.CellConfigInstance{
			ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: instanceName(cfg)},
		}
	}

	ensureNode := func(ctx SpecContext, name string) {
		node := &fabricv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, node))).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error { return client.IgnoreNotFound(k8sClient.Delete(ctx, node)) })
	}

	driveCellActive := func(cellName string) {
		cell := &fabricv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: cellName}}
		Eventually(Get(cell), "30s").Should(Succeed())
		Eventually(UpdateStatus(cell, func() { cell.Status.Phase = fabricv1alpha1.CellActive }), "30s").Should(Succeed())
	}

	It("materializes one current-revision CellConfigInstance per selected CellConfig", func(ctx SpecContext) {
		By("creating two CellConfigs and a rollout")
		ensureNode(ctx, "node-rollout-1")
		sel := map[string]string{groupLabelKey: "rollout-1"}

		cfg1 := newConfig("cfg-r1-a", "node-rollout-1", sel)
		cfg2 := newConfig("cfg-r1-b", "node-rollout-1", sel)
		Expect(k8sClient.Create(ctx, cfg1)).To(Succeed())
		Expect(k8sClient.Create(ctx, cfg2)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cfg1)
		DeferCleanup(k8sClient.Delete, cfg2)

		rollout := newRollout("rollout-1", sel)
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		By("driving both cells Active (order-independent) and asserting CCIs are correct")
		cfgs := []*fabricv1alpha1.CellConfig{cfg1, cfg2}
		Eventually(func(g Gomega) {
			for _, cfg := range cfgs {
				cell := &fabricv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: instanceName(cfg)}}
				if err := Get(cell)(); err == nil && cell.Status.Phase != fabricv1alpha1.CellActive {
					_ = UpdateStatus(cell, func() { cell.Status.Phase = fabricv1alpha1.CellActive })()
				}
			}
			for _, cfg := range cfgs {
				g.Expect(Object(currentInstance(cfg))()).To(SatisfyAll(
					HaveField("Spec.Enabled", BeTrue()),
					HaveField("Spec.CellConfigSpec.Hostname", Equal(cfg.Name)),
					HaveField("ObjectMeta.Annotations", HaveKey(cellConfigRevisionAnnotation)),
					HaveField("ObjectMeta.OwnerReferences", ContainElement(SatisfyAll(
						HaveField("Kind", "CellConfigRollout"),
						HaveField("Name", rollout.Name),
					))),
				))
			}
		}, "30s", "100ms").Should(Succeed())

		Eventually(Object(rollout)).Should(HaveField("Status.DesiredCells", BeEquivalentTo(2)))
	})

	It("deletes all revisions when its CellConfig stops matching the selector", func(ctx SpecContext) {
		By("creating a CellConfig and a rollout")
		ensureNode(ctx, "node-rollout-2")
		sel := map[string]string{groupLabelKey: "rollout-2"}

		cfg := newConfig("cfg-r2-a", "node-rollout-2", sel)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error { return client.IgnoreNotFound(k8sClient.Delete(ctx, cfg)) })

		rollout := newRollout("rollout-2", sel)
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		Eventually(Get(currentInstance(cfg))).Should(Succeed())

		By("relabeling the config out of the selector")
		Eventually(Update(cfg, func() {
			cfg.Labels = map[string]string{groupLabelKey: "other"}
		})).Should(Succeed())

		Eventually(ObjectList(&fabricv1alpha1.CellConfigInstanceList{},
			client.InNamespace(rolloutNS), client.MatchingLabels(sel)),
		).Should(HaveField("Items", BeEmpty()))
	})

	It("skips invalid CellConfigs whose Node does not exist", func(ctx SpecContext) {
		By("creating a CellConfig with a missing Node reference")
		sel := map[string]string{groupLabelKey: "rollout-3"}
		cfg := newConfig("cfg-r3-a", "nonexistent-node", sel)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cfg)

		rollout := newRollout("rollout-3", sel)
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		Eventually(Object(rollout)).Should(SatisfyAll(
			HaveField("Status.DesiredCells", BeEquivalentTo(0)),
			HaveField("Status.Conditions", ContainElement(
				HaveField("Type", fabricv1alpha1.CellConfigRolloutConditionProgressing),
			)),
		))
		Consistently(ObjectList(&fabricv1alpha1.CellConfigInstanceList{},
			client.InNamespace(rolloutNS), client.MatchingLabels(sel)),
			"2s", "100ms",
		).Should(HaveField("Items", BeEmpty()))
	})

	It("counts available cells as instances become Ready", func(ctx SpecContext) {
		By("creating a CellConfig and a rollout, then driving the cell Active")
		ensureNode(ctx, "node-rollout-4")
		sel := map[string]string{groupLabelKey: "rollout-4"}

		cfg := newConfig("cfg-r4-a", "node-rollout-4", sel)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cfg)

		rollout := newRollout("rollout-4", sel)
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		driveCellActive(instanceName(cfg))

		Eventually(Object(rollout)).Should(HaveField("Status.AvailableCells", BeEquivalentTo(1)))
	})

	It("creates a NEW revision instance on config change and retains the old one disabled", func(ctx SpecContext) {
		By("creating a CellConfig and a rollout, driving the first revision Active")
		ensureNode(ctx, "node-rollout-6")
		sel := map[string]string{groupLabelKey: "rollout-6"}

		cfg := newConfig("cfg-r6-a", "node-rollout-6", sel)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cfg)

		rollout := newRollout("rollout-6", sel)
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		rev1Name := instanceName(cfg)
		Eventually(Get(currentInstance(cfg))).Should(Succeed())
		driveCellActive(rev1Name)
		Eventually(Object(rollout)).Should(HaveField("Status.AvailableCells", BeEquivalentTo(1)))

		By("changing the config spec, expecting a new revision CCI and the old one disabled")
		Eventually(Update(cfg, func() { cfg.Spec.Hostname = "cfg-r6-a-v2" })).Should(Succeed())
		rev2Name := instanceName(cfg)
		Expect(rev2Name).NotTo(Equal(rev1Name))

		Eventually(Object(currentInstance(cfg))).Should(HaveField("Spec.Enabled", BeTrue()))

		rev1 := &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: rev1Name}}
		Eventually(Object(rev1)).Should(HaveField("Spec.Enabled", BeFalse()))

		Eventually(ObjectList(&fabricv1alpha1.CellConfigInstanceList{},
			client.InNamespace(rolloutNS), client.MatchingLabels(sel)),
		).Should(HaveField("Items", HaveLen(2)))
	})

	It("prunes revision history down to revisionHistoryLimit", func(ctx SpecContext) {
		By("creating a CellConfig with revisionHistoryLimit=2 and driving the initial revision Active")
		ensureNode(ctx, "node-rollout-7")
		sel := map[string]string{groupLabelKey: "rollout-7"}

		cfg := newConfig("cfg-r7-a", "node-rollout-7", sel)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cfg)

		rollout := newRollout("rollout-7", sel)
		limit := int32(2)
		rollout.Spec.RevisionHistoryLimit = &limit
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		Eventually(Get(currentInstance(cfg))).Should(Succeed())
		driveCellActive(instanceName(cfg))
		Eventually(Object(currentInstance(cfg))).Should(HaveField("Spec.Enabled", BeTrue()))

		By("rolling through 5 more revisions, each driven Active before the next change")
		for _, host := range []string{"h0", "h1", "h2", "h3", "h4"} {
			Eventually(Update(cfg, func() { cfg.Spec.Hostname = host })).Should(Succeed())
			Eventually(Get(currentInstance(cfg))).Should(Succeed())
			driveCellActive(instanceName(cfg))
			Eventually(Object(currentInstance(cfg))).Should(SatisfyAll(
				HaveField("Spec.Enabled", BeTrue()),
				HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", fabricv1alpha1.CellConfigInstanceConditionReady),
					HaveField("Status", metav1.ConditionTrue),
				))),
			))
		}

		By("asserting at most limit disabled history instances remain")
		Eventually(func(g Gomega) {
			list := &fabricv1alpha1.CellConfigInstanceList{}
			g.Expect(List(list, client.InNamespace(rolloutNS), client.MatchingLabels(sel))()).To(Succeed())
			enabled, disabled := 0, 0
			for i := range list.Items {
				if list.Items[i].Spec.Enabled {
					enabled++
				} else {
					disabled++
				}
			}
			g.Expect(enabled).To(Equal(1))
			g.Expect(disabled).To(BeNumerically("<=", int(limit)))
		}).Should(Succeed())
	})

	It("rolls a config change one cell at a time, honoring maxUnavailable=1", func(ctx SpecContext) {
		By("creating two CellConfigs, a rollout, and bringing both cells to Active")
		ensureNode(ctx, "node-rollout-5a")
		ensureNode(ctx, "node-rollout-5b")
		sel := map[string]string{groupLabelKey: "rollout-5"}

		cfgA := newConfig("cfg-r5-a", "node-rollout-5a", sel)
		cfgB := newConfig("cfg-r5-b", "node-rollout-5b", sel)
		Expect(k8sClient.Create(ctx, cfgA)).To(Succeed())
		Expect(k8sClient.Create(ctx, cfgB)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cfgA)
		DeferCleanup(k8sClient.Delete, cfgB)

		rollout := newRollout("rollout-5", sel)
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		driveCellActive(instanceName(cfgA))
		driveCellActive(instanceName(cfgB))
		Eventually(Object(rollout)).Should(HaveField("Status.AvailableCells", BeEquivalentTo(2)))

		By("changing both configs simultaneously and asserting never more than 1 cell is down")
		Eventually(Update(cfgA, func() { cfgA.Spec.Hostname = "cfg-r5-a-v2" })).Should(Succeed())
		Eventually(Update(cfgB, func() { cfgB.Spec.Hostname = "cfg-r5-b-v2" })).Should(Succeed())

		Consistently(func(g Gomega) {
			a := &fabricv1alpha1.CellConfig{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: cfgA.Name}}
			b := &fabricv1alpha1.CellConfig{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: cfgB.Name}}
			g.Expect(Get(a)()).To(Succeed())
			g.Expect(Get(b)()).To(Succeed())
			list := &fabricv1alpha1.CellConfigInstanceList{}
			g.Expect(List(list, client.InNamespace(rolloutNS), client.MatchingLabels(sel))()).To(Succeed())
			ownedByCell := map[string][]*fabricv1alpha1.CellConfigInstance{}
			for i := range list.Items {
				cell := instanceCellConfigName(&list.Items[i])
				ownedByCell[cell] = append(ownedByCell[cell], &list.Items[i])
			}
			g.Expect(countUnavailableForCurrentRevision(ownedByCell, []*fabricv1alpha1.CellConfig{a, b})).
				To(BeNumerically("<=", 1))
		}, "3s", "100ms").Should(Succeed())
	})

	It("supports rollback by re-enabling a prior revision", func(ctx SpecContext) {
		By("creating a CellConfig and rollout, driving revision 1 Active")
		ensureNode(ctx, "node-rollout-9")
		sel := map[string]string{groupLabelKey: "rollout-9"}

		cfg := newConfig("cfg-r9-a", "node-rollout-9", sel)
		Expect(k8sClient.Create(ctx, cfg)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cfg)

		rollout := newRollout("rollout-9", sel)
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		rev1Name := instanceName(cfg)
		Eventually(Get(currentInstance(cfg))).Should(Succeed())
		driveCellActive(rev1Name)
		Eventually(Object(currentInstance(cfg))).Should(HaveField("Spec.Enabled", BeTrue()))

		By("rolling to revision 2")
		Eventually(Update(cfg, func() { cfg.Spec.Hostname = "cfg-r9-a-v2" })).Should(Succeed())
		Eventually(Get(currentInstance(cfg))).Should(Succeed())
		driveCellActive(instanceName(cfg))
		Eventually(Object(currentInstance(cfg))).Should(HaveField("Spec.Enabled", BeTrue()))
		rev1 := &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: rev1Name}}
		Eventually(Object(rev1)).Should(HaveField("Spec.Enabled", BeFalse()))

		By("reverting the config to revision 1's spec; the rollout re-enables the retained instance")
		Eventually(Update(cfg, func() { cfg.Spec.Hostname = "cfg-r9-a" })).Should(Succeed())
		Expect(instanceName(cfg)).To(Equal(rev1Name))
		Eventually(Object(rev1)).Should(HaveField("Spec.Enabled", BeTrue()))
	})

	It("rolls 4 cells one at a time AND retains history when all configs change", func(ctx SpecContext) {
		By("creating 4 nodes, 4 CellConfigs, and a rollout with revisionHistoryLimit=2")
		nodes := []string{"node-r10-1", "node-r10-2", "node-r10-3", "node-r10-4"}
		for _, n := range nodes {
			ensureNode(ctx, n)
		}
		sel := map[string]string{groupLabelKey: "rollout-10"}

		cfgs := make([]*fabricv1alpha1.CellConfig, 4)
		for i, n := range nodes {
			cfgs[i] = newConfig("cfg-r10-"+n, n, sel)
			Expect(k8sClient.Create(ctx, cfgs[i])).To(Succeed())
			DeferCleanup(k8sClient.Delete, cfgs[i])
		}

		rollout := newRollout("rollout-10", sel)
		limit := int32(2)
		rollout.Spec.RevisionHistoryLimit = &limit
		Expect(k8sClient.Create(ctx, rollout)).To(Succeed())
		DeferCleanup(k8sClient.Delete, rollout)

		By("driving all 4 initial CCIs to Active (order-independent)")
		Eventually(func(g Gomega) {
			for _, cfg := range cfgs {
				cell := &fabricv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: instanceName(cfg)}}
				if err := Get(cell)(); err == nil && cell.Status.Phase != fabricv1alpha1.CellActive {
					_ = UpdateStatus(cell, func() { cell.Status.Phase = fabricv1alpha1.CellActive })()
				}
			}
			for _, cfg := range cfgs {
				g.Expect(Object(currentInstance(cfg))()).To(HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", fabricv1alpha1.CellConfigInstanceConditionReady),
					HaveField("Status", metav1.ConditionTrue),
				))))
			}
		}, "60s", "100ms").Should(Succeed())
		Eventually(Object(rollout)).Should(HaveField("Status.AvailableCells", BeEquivalentTo(4)))

		By("changing all 4 config hostnames at once")
		for _, cfg := range cfgs {
			Eventually(Update(cfg, func() { cfg.Spec.Hostname = cfg.Spec.Hostname + "-v2" })).Should(Succeed())
		}

		downNow := func(g Gomega) int {
			latest := make([]*fabricv1alpha1.CellConfig, 0, len(cfgs))
			for _, c := range cfgs {
				cur := &fabricv1alpha1.CellConfig{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: c.Name}}
				g.Expect(Get(cur)()).To(Succeed())
				latest = append(latest, cur)
			}
			list := &fabricv1alpha1.CellConfigInstanceList{}
			g.Expect(List(list, client.InNamespace(rolloutNS), client.MatchingLabels(sel))()).To(Succeed())
			ownedByCell := map[string][]*fabricv1alpha1.CellConfigInstance{}
			for i := range list.Items {
				if cell := instanceCellConfigName(&list.Items[i]); cell != "" {
					ownedByCell[cell] = append(ownedByCell[cell], &list.Items[i])
				}
			}
			return countUnavailableForCurrentRevision(ownedByCell, latest)
		}

		allV2Active := func(g Gomega) bool {
			for _, c := range cfgs {
				cur := &fabricv1alpha1.CellConfig{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: c.Name}}
				g.Expect(Get(cur)()).To(Succeed())
				inst := &fabricv1alpha1.CellConfigInstance{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: instanceName(cur)}}
				if err := Get(inst)(); err != nil {
					return false
				}
				if !inst.Spec.Enabled || !isAvailable(inst) {
					return false
				}
			}
			return true
		}

		By("driving the rolling update forward while asserting maxUnavailable=1 is never violated")
		Eventually(func(g Gomega) {
			g.Expect(downNow(g)).To(BeNumerically("<=", 1))
			for _, c := range cfgs {
				cur := &fabricv1alpha1.CellConfig{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: c.Name}}
				g.Expect(Get(cur)()).To(Succeed())
				cell := &fabricv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Namespace: rolloutNS, Name: instanceName(cur)}}
				if err := Get(cell)(); err == nil && cell.Status.Phase != fabricv1alpha1.CellActive {
					_ = UpdateStatus(cell, func() { cell.Status.Phase = fabricv1alpha1.CellActive })()
				}
			}
			g.Expect(allV2Active(g)).To(BeTrue())
		}, "60s", "100ms").Should(Succeed())

		Consistently(func(g Gomega) {
			g.Expect(downNow(g)).To(BeNumerically("<=", 1))
		}, "1s", "100ms").Should(Succeed())

		By("asserting each cell retains its old revision as history up to the limit")
		Eventually(func(g Gomega) {
			list := &fabricv1alpha1.CellConfigInstanceList{}
			g.Expect(List(list, client.InNamespace(rolloutNS), client.MatchingLabels(sel))()).To(Succeed())
			perCellEnabled := map[string]int{}
			perCellTotal := map[string]int{}
			for i := range list.Items {
				cell := instanceCellConfigName(&list.Items[i])
				perCellTotal[cell]++
				if list.Items[i].Spec.Enabled {
					perCellEnabled[cell]++
				}
			}
			g.Expect(perCellTotal).To(HaveLen(4))
			for cell, n := range perCellEnabled {
				g.Expect(n).To(Equal(1), "cell %s must have exactly one enabled revision", cell)
			}
			for cell, n := range perCellTotal {
				g.Expect(n).To(BeNumerically(">=", 2), "cell %s should retain its prior revision as history", cell)
				g.Expect(n).To(BeNumerically("<=", 1+int(limit)))
			}
		}).Should(Succeed())
	})
})
