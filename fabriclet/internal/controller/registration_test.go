// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	fabricletcontroller "github.com/ironcore-dev/fabric/fabriclet/controller"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Fabriclet registration", func() {
	It("creates the Node and Interfaces idempotently", func(ctx SpecContext) {
		nodeName := "registration-node-" + rand.String(5)
		interfaceNames := []string{
			nodeName + "-eth0",
			nodeName + "-eth1",
		}
		opts := fabricletcontroller.InitOptions{
			Interfaces: []fabricletcontroller.InitInterfaceOptions{
				{Name: interfaceNames[0], Handle: "fake://eth0"},
				{Name: interfaceNames[1], Handle: "fake://eth1"},
			},
		}

		Expect(fabricletcontroller.Init(ctx, k8sClient, nodeName, "fake://node", opts)).To(Succeed())
		Expect(fabricletcontroller.Init(ctx, k8sClient, nodeName, "fake://node", opts)).To(Succeed())

		node := &fabricv1alpha1.Node{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: nodeName}, node)).To(Succeed())
		Expect(node.Spec.ProviderID).To(Equal("fake://node"))
		DeferCleanup(k8sClient.Delete, node)

		for i, interfaceName := range interfaceNames {
			iface := &fabricv1alpha1.Interface{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: interfaceName}, iface)).To(Succeed())
			Expect(iface.Spec.NodeRef.Name).To(Equal(nodeName))
			Expect(iface.Spec.Handle).To(Equal(opts.Interfaces[i].Handle))
			DeferCleanup(k8sClient.Delete, iface)
		}
	})
})
