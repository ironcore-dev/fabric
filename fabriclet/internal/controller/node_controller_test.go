// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"

	"github.com/ironcore-dev/fabric/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Node Controller", func() {
	It("should set the provider ID on the node", func(ctx SpecContext) {
		By("creating a node without provider ID")
		node := &v1alpha1.Node{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: nodeGeneratePrefix,
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(k8sClient.Delete, node)
		nodeKey := client.ObjectKeyFromObject(node)

		By("waiting for the node to report the provider ID")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, nodeKey, node)).To(Succeed())
			g.Expect(node.Spec.ProviderID).To(Equal(fmt.Sprintf("fake://%s", node.Name)))
		}).Should(Succeed())
	})

	It("should release nodes that are bound to non-existent cells", func(ctx SpecContext) {
		By("creating a node pointing at a non-existent cell")
		node := &v1alpha1.Node{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: nodeGeneratePrefix,
			},
			Spec: v1alpha1.NodeSpec{
				CellRef: &v1alpha1.NamespacedUIDReference{
					Namespace: nonExistentRef,
					Name:      nonExistentRef,
					UID:       nonExistentRef,
				},
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(k8sClient.Delete, node)
		nodeKey := client.ObjectKeyFromObject(node)

		By("waiting for the node to be released")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, nodeKey, node)).To(Succeed())
			g.Expect(node.Spec.CellRef).To(BeNil())
		}).Should(Succeed())
	})
})
