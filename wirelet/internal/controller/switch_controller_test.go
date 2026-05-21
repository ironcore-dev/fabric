// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"net/netip"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/deviceruntime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("SwitchController", func() {
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

	It("should reconcile the switch", func(ctx SpecContext) {
		By("creating a switch")
		swtch := &v1alpha1.Switch{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    "default",
				GenerateName: "switch-",
			},
			Spec: v1alpha1.SwitchSpec{
				DeviceRef: v1alpha1.LocalObjectReference{
					Name: device.Name,
				},
				Hostname: "host.example.org",
				IPs: []string{
					"ffee:ffee::1",
				},
				Prefixes: []string{
					"ffee:ffee::/64",
				},
				VLANs: []v1alpha1.SwitchVLAN{
					{
						ID:        1000,
						Prefix:    "ffee:ffee:f::/80",
						DHCPRelay: "dhcp.example.org",
						Members: []v1alpha1.SwitchVLANMember{
							{
								DeviceInterfaceRef: &v1alpha1.LocalObjectReference{
									Name: deviceInterface.Name,
								},
							},
						},
					},
				},
				BGP: &v1alpha1.SwitchBGP{
					ASN:      20000,
					RouterID: "2.2.2.2",
					PeerGroups: []v1alpha1.SwitchBGPPeerGroup{
						{
							Name: "server",
							Neighbors: []v1alpha1.SwitchBGPNeighbor{
								{
									VLANID: 1000,
								},
							},
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, swtch)).To(Succeed())
		swtchKey := client.ObjectKeyFromObject(swtch)

		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, swtch))
		})

		By("waiting for the switch active & the runtime to be configured")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, swtchKey, swtch)).To(Succeed())
			g.Expect(swtch.Status.Phase).To(Equal(v1alpha1.SwitchActive))
			g.Expect(controllerutil.ContainsFinalizer(swtch, DeviceFinalizer(device.Name))).To(BeTrue())

			fakeDeviceRuntime.RLock()
			defer fakeDeviceRuntime.RUnlock()

			desiredCfg := &deviceruntime.SwitchConfig{
				Metadata: deviceruntime.SwitchMetadata{
					Namespace: swtch.Namespace,
					Name:      swtch.Name,
					UID:       string(swtch.UID),
				},
				Hostname: "host.example.org",
				LoopbackIPs: []netip.Addr{
					netip.MustParseAddr("ffee:ffee::1"),
				},
				Prefixes: []netip.Prefix{
					netip.MustParsePrefix("ffee:ffee::/64"),
				},
				VLANs: []deviceruntime.VLAN{
					{
						ID:        1000,
						Prefix:    netip.MustParsePrefix("ffee:ffee:f::/80"),
						DHCPRelay: "dhcp.example.org",
						Members: []deviceruntime.VLANMember{
							{
								Interface: &deviceruntime.Interface{
									Metadata: deviceruntime.InterfaceMetadata{
										Name: deviceInterface.Name,
										UID:  string(deviceInterface.UID),
									},
									ID: deviceInterface.Name,
								},
							},
						},
					},
				},
				BGP: &deviceruntime.BGP{
					ASN:      20000,
					RouterID: "2.2.2.2",
					PeerGroups: []deviceruntime.BGPPeerGroup{
						{
							Name: "server",
							Neighbors: []deviceruntime.BGPNeighbor{
								{
									VLANID: 1000,
								},
							},
						},
					},
				},
			}

			g.Expect(fakeDeviceRuntime.Switches[device.Name]).To(Equal(desiredCfg))
		}).To(Succeed())

		By("deleting the switch")
		Expect(k8sClient.Delete(ctx, swtch)).To(Succeed())

		By("waiting for the switch to be gone and the runtime to be reset")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, swtchKey, swtch)).To(Satisfy(apierrors.IsNotFound))

			fakeDeviceRuntime.RLock()
			defer fakeDeviceRuntime.RUnlock()
			g.Expect(fakeDeviceRuntime.Switches[device.Name]).To(BeNil())
		}).Should(Succeed())
	})
})
