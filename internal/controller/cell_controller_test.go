// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("CellReconciler", func() {
	It("reports when the referenced Node does not exist", func(ctx SpecContext) {
		cell := &fabricv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", GenerateName: "missing-node-cell-"},
			Spec: fabricv1alpha1.CellSpec{
				NodeRef: fabricv1alpha1.LocalObjectReference{Name: "missing-node"},
			},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, cell))
		})

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cell), cell)).To(Succeed())
			g.Expect(meta.FindStatusCondition(cell.Status.Conditions, fabricv1alpha1.CellConditionTypeBound)).To(And(
				HaveField("Status", metav1.ConditionFalse),
				HaveField("Reason", "NodeNotFound"),
			))
		}).Should(Succeed())
	})

	It("leaves binding status to the Fabriclet when the Node exists", func(ctx SpecContext) {
		node := &fabricv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{GenerateName: "existing-node-"}}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(k8sClient.Delete, node)

		cell := &fabricv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", GenerateName: "existing-node-cell-"},
			Spec: fabricv1alpha1.CellSpec{
				NodeRef: fabricv1alpha1.LocalObjectReference{Name: node.Name},
			},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cell)

		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cell), cell)).To(Succeed())
			g.Expect(meta.FindStatusCondition(cell.Status.Conditions, fabricv1alpha1.CellConditionTypeBound)).To(BeNil())
		}).Should(Succeed())
	})
})
