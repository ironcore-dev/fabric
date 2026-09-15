// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"net/netip"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/cellruntime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("CellController", func() {
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

	It("should reconcile the cell", func(ctx SpecContext) {
		By("creating a cell")
		cell := &v1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    "default",
				GenerateName: "cell-",
			},
			Spec: v1alpha1.CellSpec{
				NodeRef: v1alpha1.LocalObjectReference{
					Name: node.Name,
				},
				Hostname: "host.example.org",
				IPs: []string{
					"ffee:ffee::1",
				},
				Prefixes: []string{
					"ffee:ffee::/64",
				},
				Peers: []v1alpha1.Peer{
					{
						InterfaceRef: v1alpha1.LocalObjectReference{Name: iface.Name},
						DHCPRelay:    "dhcp.example.org",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())
		cellKey := client.ObjectKeyFromObject(cell)

		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, cell))
		})

		By("waiting for the cell to be pending and present in the runtime")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, cellKey, cell)).To(Succeed())
			g.Expect(cell.Status.Phase).To(Equal(v1alpha1.CellPending))
			g.Expect(controllerutil.ContainsFinalizer(cell, NodeFinalizer(node.Name))).To(BeTrue())

			fakeCellRuntime.RLock()
			defer fakeCellRuntime.RUnlock()

			desiredCfg := &cellruntime.CellConfig{
				Metadata: cellruntime.CellMetadata{
					Namespace: cell.Namespace,
					Name:      cell.Name,
					UID:       string(cell.UID),
				},
				Hostname: "host.example.org",
				LoopbackIPs: []netip.Addr{
					netip.MustParseAddr("ffee:ffee::1"),
				},
				Prefixes: []netip.Prefix{
					netip.MustParsePrefix("ffee:ffee::/64"),
				},
				Peers: []cellruntime.Peer{
					{
						Interface: &cellruntime.Interface{
							Metadata: cellruntime.InterfaceMetadata{
								Name: iface.Name,
								UID:  string(iface.UID),
							},
							ID: iface.Name,
						},
						DHCPRelay: "dhcp.example.org",
					},
				},
			}

			g.Expect(fakeCellRuntime.Cells[node.Name]).To(HaveField("Config", desiredCfg))
		}).To(Succeed())

		By("setting the fake cell status to active")
		func() {
			fakeCellRuntime.Lock()
			defer fakeCellRuntime.Unlock()

			fakeCellRuntime.Cells[node.Name].Status.Phase = cellruntime.CellPhaseActive
		}()

		By("waiting for the cell to be active")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, cellKey, cell)).To(Succeed())
			g.Expect(cell.Status.Phase).To(Equal(v1alpha1.CellActive))
		}).To(Succeed())

		By("deleting the cell")
		Expect(k8sClient.Delete(ctx, cell)).To(Succeed())

		By("waiting for the cell to be gone and the runtime to be reset")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, cellKey, cell)).To(Satisfy(apierrors.IsNotFound))

			fakeCellRuntime.RLock()
			defer fakeCellRuntime.RUnlock()
			g.Expect(fakeCellRuntime.Cells[node.Name]).To(BeNil())
		}).Should(Succeed())
	})
})
