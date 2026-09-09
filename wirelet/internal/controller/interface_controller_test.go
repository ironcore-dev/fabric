// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/cellruntime"
	celltesting "github.com/ironcore-dev/wire/cellruntime/testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("InterfaceController", func() {
	var (
		node  *v1alpha1.Node
		iface *v1alpha1.Interface
	)
	BeforeEach(func(ctx SpecContext) {
		node = &v1alpha1.Node{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: nodeGeneratePrefix,
			},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())

		DeferCleanup(k8sClient.Delete, node)

		iface = &v1alpha1.Interface{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "interface-",
			},
			Spec: v1alpha1.InterfaceSpec{
				NodeRef: v1alpha1.LocalObjectReference{
					Name: node.Name,
				},
			},
		}
		Expect(k8sClient.Create(ctx, iface)).To(Succeed())

		DeferCleanup(k8sClient.Delete, iface)
	})

	It("should reconcile the runtime state the interface", func(ctx SpecContext) {
		iface1Key := client.ObjectKeyFromObject(iface)

		By("waiting for the interface state to be unknown and handle to be set")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, iface1Key, iface)).To(Succeed())
			g.Expect(iface.Status.OperationState).To(Equal(v1alpha1.OperationStateUnknown))
			g.Expect(iface.Spec.Handle).To(Equal(fmt.Sprintf("%s://%s", celltesting.ProviderName, iface1Key.Name)))
		}).Should(Succeed())

		By("adding the runtime state to the fake runtime")
		func() {
			fakeCellRuntime.Lock()
			defer fakeCellRuntime.Unlock()

			if fakeCellRuntime.InterfaceStates == nil {
				fakeCellRuntime.InterfaceStates = make(map[string]cellruntime.InterfaceState)
			}
			fakeCellRuntime.InterfaceStates[iface.Name] = cellruntime.InterfaceState{AdminUp: true, OperUp: true}
		}()

		By("issuing an empty patch to the interface to trigger reconcile")
		base := iface.DeepCopy()
		metav1.SetMetaDataAnnotation(&iface.ObjectMeta, "foof", "bar")
		Expect(k8sClient.Patch(ctx, iface, client.MergeFrom(base))).To(Succeed())

		By("waiting for the fake interface state to be down")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, iface1Key, iface)).To(Succeed())
			g.Expect(iface.Status.OperationState).To(Equal(v1alpha1.OperationStateDown))
		}).Should(Succeed())

		By("setting the interface admin state")
		baseIface1 := iface.DeepCopy()
		iface.Spec.AdminState = v1alpha1.AdminStateUp
		Expect(k8sClient.Patch(ctx, iface, client.MergeFrom(baseIface1))).To(Succeed())

		By("waiting for the cell runtime state to be updated")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, iface1Key, iface)).To(Succeed())
			g.Expect(iface.Status.OperationState).To(Equal(v1alpha1.OperationStateUp))

			fakeCellRuntime.RLock()
			defer fakeCellRuntime.RUnlock()
			g.Expect(fakeCellRuntime.InterfaceStates[iface.Name].AdminUp).To(BeTrueBecause("admin state has been set to true"))
		}).Should(Succeed())
	})
})
