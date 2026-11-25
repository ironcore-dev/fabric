// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"net/netip"

	"github.com/ironcore-dev/fabric/api/v1alpha1"
	"github.com/ironcore-dev/fabric/cellruntime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const defaultNamespace = "default"
const testCellHostname = "host.example.org"
const testDHCPRelay = "dhcp.example.org"

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
				Namespace:    defaultNamespace,
				GenerateName: "cell-",
			},
			Spec: v1alpha1.CellSpec{
				NodeRef: v1alpha1.LocalObjectReference{
					Name: node.Name,
				},
				Hostname: testCellHostname,
				IPs: []string{
					"ffee:ffee::1",
				},
				Prefixes: []string{
					"ffee:ffee::/64",
				},
				Peers: []v1alpha1.Peer{
					{
						InterfaceRef: v1alpha1.LocalObjectReference{Name: iface.Name},
						DHCPRelay:    testDHCPRelay,
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())

		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, cell))
		})

		By("waiting for the Fabriclet to bind and apply the cell")
		Eventually(Object(cell)).Should(SatisfyAll(
			HaveField("Status.Phase", Equal(v1alpha1.CellPending)),
			HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", v1alpha1.CellConditionTypeBound),
				HaveField("Status", metav1.ConditionTrue),
				HaveField("Reason", "Bound"),
			))),
			HaveField("Finalizers", ContainElement(NodeFinalizer(node.Name))),
		))
		Eventually(Object(node)).Should(
			HaveField("Spec.CellRef", Equal(&v1alpha1.NamespacedUIDReference{
				Namespace: cell.Namespace,
				Name:      cell.Name,
				UID:       cell.UID,
			})),
		)

		desiredCfg := &cellruntime.CellConfig{
			Metadata: cellruntime.CellMetadata{
				Namespace: cell.Namespace,
				Name:      cell.Name,
				UID:       string(cell.UID),
			},
			Hostname: testCellHostname,
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
					DHCPRelay: testDHCPRelay,
				},
			},
		}
		Eventually(func(g Gomega) {
			fakeCellRuntime.RLock()
			defer fakeCellRuntime.RUnlock()
			g.Expect(fakeCellRuntime.Cells[node.Name]).To(HaveField("Config", desiredCfg))
		}).Should(Succeed())

		By("setting the fake cell status to active")
		func() {
			fakeCellRuntime.Lock()
			defer fakeCellRuntime.Unlock()

			fakeCellRuntime.Cells[node.Name].Status.Phase = cellruntime.CellPhaseActive
		}()

		By("waiting for the cell to be active")
		Eventually(Object(cell)).Should(HaveField("Status.Phase", Equal(v1alpha1.CellActive)))

		By("deleting the cell")
		Expect(k8sClient.Delete(ctx, cell)).To(Succeed())

		By("waiting for the cell to be gone and the runtime to be reset")
		Eventually(Get(cell)).Should(Satisfy(apierrors.IsNotFound))
		Eventually(func(g Gomega) {
			fakeCellRuntime.RLock()
			defer fakeCellRuntime.RUnlock()
			g.Expect(fakeCellRuntime.Cells[node.Name]).To(BeNil())
		}).Should(Succeed())
	})

	It("marks a previously-Active cell Degraded when readiness regresses", func(ctx SpecContext) {
		By("creating a cell")
		cell := &v1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: defaultNamespace, GenerateName: "cell-degraded-"},
			Spec: v1alpha1.CellSpec{
				NodeRef:  v1alpha1.LocalObjectReference{Name: node.Name},
				Hostname: testCellHostname,
				IPs:      []string{"ffee:ffee::1"},
				Prefixes: []string{"ffee:ffee::/64"},
				Peers: []v1alpha1.Peer{
					{InterfaceRef: v1alpha1.LocalObjectReference{Name: iface.Name}, DHCPRelay: "dhcp.example.org"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, cell))
		})

		By("driving the cell to Active")
		Eventually(func(g Gomega) {
			fakeCellRuntime.Lock()
			defer fakeCellRuntime.Unlock()
			g.Expect(fakeCellRuntime.Cells[node.Name]).NotTo(BeNil())
			fakeCellRuntime.Cells[node.Name].Status.Phase = cellruntime.CellPhaseActive
		}).Should(Succeed())
		Eventually(Object(cell)).Should(HaveField("Status.Phase", Equal(v1alpha1.CellActive)))

		By("regressing readiness: runtime reports Created again")
		func() {
			fakeCellRuntime.Lock()
			defer fakeCellRuntime.Unlock()
			fakeCellRuntime.Cells[node.Name].Status.Phase = cellruntime.CellPhaseCreated
		}()

		By("expecting the cell to become Degraded, not Pending")
		Eventually(Object(cell)).Should(HaveField("Status.Phase", Equal(v1alpha1.CellDegraded)))
		// And never silently empty.
		Expect(cell.Status.Phase).NotTo(BeEmpty())
	})

	It("reports when the requested Node is bound to another Cell", func(ctx SpecContext) {
		existingCell := &v1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: defaultNamespace, GenerateName: "existing-cell-"},
			Spec:       v1alpha1.CellSpec{NodeRef: v1alpha1.LocalObjectReference{Name: node.Name}},
		}
		Expect(k8sClient.Create(ctx, existingCell)).To(Succeed())
		DeferCleanup(k8sClient.Delete, existingCell)

		Eventually(Object(node)).Should(
			HaveField("Spec.CellRef", Equal(&v1alpha1.NamespacedUIDReference{
				Namespace: existingCell.Namespace,
				Name:      existingCell.Name,
				UID:       existingCell.UID,
			})),
		)

		cell := &v1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: defaultNamespace, GenerateName: "waiting-cell-"},
			Spec:       v1alpha1.CellSpec{NodeRef: v1alpha1.LocalObjectReference{Name: node.Name}},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cell)

		Eventually(Object(cell)).Should(SatisfyAll(
			HaveField("Status.Phase", Equal(v1alpha1.CellPending)),
			HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", v1alpha1.CellConditionTypeBound),
				HaveField("Status", metav1.ConditionFalse),
				HaveField("Reason", "NodeAlreadyBound"),
			))),
		))
	})

	It("should reject changing the cell node reference", func(ctx SpecContext) {
		cell := &v1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Namespace: defaultNamespace, GenerateName: "cell-"},
			Spec:       v1alpha1.CellSpec{NodeRef: v1alpha1.LocalObjectReference{Name: node.Name}},
		}
		Expect(k8sClient.Create(ctx, cell)).To(Succeed())
		DeferCleanup(k8sClient.Delete, cell)

		cell.Spec.NodeRef.Name = "another-node"
		Expect(k8sClient.Update(ctx, cell)).NotTo(Succeed())
	})
})
