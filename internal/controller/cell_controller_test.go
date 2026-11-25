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

var _ = Describe("CellReconciler", func() {
	It("reports when the referenced Node does not exist", func(ctx SpecContext) {
		cell := &fabricv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: defaultNamespace, GenerateName: "missing-node-cell-"},
			Spec: fabricv1alpha1.CellSpec{
				NodeRef: fabricv1alpha1.LocalObjectReference{Name: "missing-node"},
			},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, cell))
		})

		Eventually(Object(cell)).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
			HaveField("Type", fabricv1alpha1.CellConditionTypeBound),
			HaveField("Status", metav1.ConditionFalse),
			HaveField("Reason", "NodeNotFound"),
		))))
	})

	It("leaves binding status to the Fabriclet when the Node exists", func(ctx SpecContext) {
		node := &fabricv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{GenerateName: "existing-node-"}}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(k8sClient.Delete, node)

		cell := &fabricv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: defaultNamespace, GenerateName: "existing-node-cell-"},
			Spec: fabricv1alpha1.CellSpec{
				NodeRef: fabricv1alpha1.LocalObjectReference{Name: node.Name},
			},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cell)

		Consistently(Object(cell)).Should(
			HaveField("Status.Conditions", Not(ContainElement(
				HaveField("Type", fabricv1alpha1.CellConditionTypeBound),
			))),
		)
	})
})
