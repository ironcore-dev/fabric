// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("CellConfigInstanceReconciler", func() {
	newInstance := func() *fabricv1alpha1.CellConfigInstance {
		return &fabricv1alpha1.CellConfigInstance{
			ObjectMeta: metav1.ObjectMeta{Namespace: defaultNamespace, GenerateName: "instance-"},
			Spec: fabricv1alpha1.CellConfigInstanceSpec{
				Enabled: true,
				CellConfigSpec: fabricv1alpha1.CellConfigSpec{
					NodeRef:  fabricv1alpha1.LocalObjectReference{Name: "node-a"},
					ID:       "42",
					Hostname: "switch-a",
				},
			},
		}
	}

	// ownedCell returns the Cell the instance materializes (same name/namespace).
	ownedCell := func(instance *fabricv1alpha1.CellConfigInstance) *fabricv1alpha1.Cell {
		return &fabricv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: instance.Namespace, Name: instance.Name},
		}
	}

	It("creates a Cell owned by the instance when enabled", func(ctx SpecContext) {
		instance := newInstance()
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, instance))
		})

		cell := ownedCell(instance)
		Eventually(Object(cell)).Should(SatisfyAll(
			HaveField("Spec.NodeRef.Name", Equal("node-a")),
			HaveField("Spec.ID", Equal("42")),
			HaveField("ObjectMeta.OwnerReferences", ContainElement(SatisfyAll(
				HaveField("Kind", "CellConfigInstance"),
				HaveField("Name", instance.Name),
			))),
		))
	})

	It("reports Ready once the owned Cell reaches phase Active", func(ctx SpecContext) {
		instance := newInstance()
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, instance))
		})

		cell := ownedCell(instance)
		Eventually(Get(cell)).Should(Succeed())
		Eventually(UpdateStatus(cell, func() {
			cell.Status.Phase = fabricv1alpha1.CellActive
		})).Should(Succeed())

		Eventually(Object(instance)).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
			HaveField("Type", fabricv1alpha1.CellConfigInstanceConditionReady),
			HaveField("Status", metav1.ConditionTrue),
		))))
	})

	It("deletes the owned Cell but keeps the instance when disabled", func(ctx SpecContext) {
		instance := newInstance()
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, instance))
		})

		cell := ownedCell(instance)
		Eventually(Get(cell)).Should(Succeed())

		Eventually(Update(instance, func() {
			instance.Spec.Enabled = false
		})).Should(Succeed())

		Eventually(Get(cell)).Should(Satisfy(apierrors.IsNotFound))
		// The instance itself is still present.
		Consistently(Get(instance)).Should(Succeed())
	})

	It("deletes the owned Cell via the finalizer before removing the instance", func(ctx SpecContext) {
		instance := newInstance()
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())

		cell := ownedCell(instance)
		Eventually(Get(cell)).Should(Succeed())

		Expect(k8sClient.Delete(ctx, instance)).To(Succeed())

		Eventually(Get(cell)).Should(Satisfy(apierrors.IsNotFound))
		Eventually(Get(instance)).Should(Satisfy(apierrors.IsNotFound))
	})

	It("reverts external edits to the owned Cell (drift)", func(ctx SpecContext) {
		instance := newInstance()
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, instance))
		})

		cell := ownedCell(instance)
		Eventually(Object(cell)).Should(HaveField("Spec.Hostname", Equal("switch-a")))

		Eventually(Update(cell, func() {
			cell.Spec.Hostname = "tampered"
		})).Should(Succeed())

		Eventually(Object(cell)).Should(HaveField("Spec.Hostname", Equal("switch-a")))
	})

	It("rejects changes to configuration fields but allows toggling enabled", func(ctx SpecContext) {
		instance := newInstance()
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, instance))
		})
		Eventually(Get(instance)).Should(Succeed())

		// Changing an immutable configuration field is rejected by the API.
		Expect(Update(instance, func() {
			instance.Spec.NodeRef.Name = "node-b"
		})()).NotTo(Succeed())

		Expect(Get(instance)()).To(Succeed())
		Expect(Update(instance, func() {
			instance.Spec.Hostname = "renamed"
		})()).NotTo(Succeed())

		// Toggling only spec.enabled is permitted.
		Expect(Get(instance)()).To(Succeed())
		Expect(Update(instance, func() {
			instance.Spec.Enabled = false
		})()).To(Succeed())
	})
})
