// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/deviceruntime/testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("DeviceInterfaceController", func() {
	var (
		device          *v1alpha1.Device
		deviceInterface *v1alpha1.DeviceInterface
	)
	BeforeEach(func(ctx SpecContext) {
		device = &v1alpha1.Device{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "device-",
			},
		}
		Expect(k8sClient.Create(ctx, device)).To(Succeed())

		DeferCleanup(k8sClient.Delete, device)

		deviceInterface = &v1alpha1.DeviceInterface{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "device-interface-",
			},
			Spec: v1alpha1.DeviceInterfaceSpec{
				DeviceRef: v1alpha1.LocalObjectReference{
					Name: device.Name,
				},
			},
		}
		Expect(k8sClient.Create(ctx, deviceInterface)).To(Succeed())

		DeferCleanup(k8sClient.Delete, deviceInterface)
	})

	It("should reconcile the runtime state the device interface", func(ctx SpecContext) {
		deviceInterface1Key := client.ObjectKeyFromObject(deviceInterface)

		By("waiting for the device interface state to be unknown and handle to be set")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, deviceInterface1Key, deviceInterface)).To(Succeed())
			g.Expect(deviceInterface.Status.OperationState).To(Equal(v1alpha1.OperationStateUnknown))
			g.Expect(deviceInterface.Spec.Handle).To(Equal(fmt.Sprintf("%s://%s", testing.ProviderName, deviceInterface1Key.Name)))
		}).Should(Succeed())

		By("adding the runtime state to the fake runtime")
		func() {
			fakeDeviceRuntime.Lock()
			defer fakeDeviceRuntime.Unlock()

			if fakeDeviceRuntime.InterfaceStates == nil {
				fakeDeviceRuntime.InterfaceStates = make(map[string]bool)
			}
			fakeDeviceRuntime.InterfaceStates[deviceInterface.Name] = true
		}()

		By("issuing an empty patch to the device interface to trigger reconcile")
		base := deviceInterface.DeepCopy()
		metav1.SetMetaDataAnnotation(&deviceInterface.ObjectMeta, "foof", "bar")
		Expect(k8sClient.Patch(ctx, deviceInterface, client.MergeFrom(base))).To(Succeed())

		By("waiting for the fake device interface state to be down")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, deviceInterface1Key, deviceInterface)).To(Succeed())
			g.Expect(deviceInterface.Status.OperationState).To(Equal(v1alpha1.OperationStateDown))
		}).Should(Succeed())

		By("setting the device interface admin state")
		baseDeviceInterface1 := deviceInterface.DeepCopy()
		deviceInterface.Spec.AdminState = v1alpha1.AdminStateUp
		Expect(k8sClient.Patch(ctx, deviceInterface, client.MergeFrom(baseDeviceInterface1))).To(Succeed())

		By("waiting for the device runtime state to be updated")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, deviceInterface1Key, deviceInterface)).To(Succeed())
			g.Expect(deviceInterface.Status.OperationState).To(Equal(v1alpha1.OperationStateUp))

			fakeDeviceRuntime.RLock()
			defer fakeDeviceRuntime.RUnlock()
			g.Expect(fakeDeviceRuntime.InterfaceStates[deviceInterface.Name]).To(BeTrueBecause("admin state has been set to true"))
		}).Should(Succeed())
	})
})
