// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Device Controller", func() {
	It("should set the provider ID on the device", func(ctx SpecContext) {
		By("creating a device without provider ID")
		device := &v1alpha1.Device{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "device-",
			},
		}
		Expect(k8sClient.Create(ctx, device)).To(Succeed())
		DeferCleanup(k8sClient.Delete, device)
		deviceKey := client.ObjectKeyFromObject(device)

		By("waiting for the device to report the provider ID")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, deviceKey, device)).To(Succeed())
			g.Expect(device.Spec.ProviderID).To(Equal(fmt.Sprintf("fake://%s", device.Name)))
		}).Should(Succeed())
	})

	It("should release devices that are bound to non-existent switches", func(ctx SpecContext) {
		By("creating a device pointing at a non-existent switch")
		device := &v1alpha1.Device{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "device-",
			},
			Spec: v1alpha1.DeviceSpec{
				SwitchRef: &v1alpha1.NamespacedUIDReference{
					Namespace: "should-not-exist",
					Name:      "should-not-exist",
					UID:       "should-not-exist",
				},
			},
		}
		Expect(k8sClient.Create(ctx, device)).To(Succeed())
		DeferCleanup(k8sClient.Delete, device)
		deviceKey := client.ObjectKeyFromObject(device)

		By("waiting for the device to be released")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, deviceKey, device)).To(Succeed())
			g.Expect(device.Spec.SwitchRef).To(BeNil())
		}).Should(Succeed())
	})
})
