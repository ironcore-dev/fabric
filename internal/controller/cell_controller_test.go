// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"testing"

	fabricv1alpha1 "github.com/ironcore-dev/fabric/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testNamespace = "default"
	testCellName  = "cell-a"
	testCellUID   = types.UID("cell-a-uid")
	testNodeName  = "node-a"
)

func TestCellReconcilerReportsUnboundWithoutClaimingNode(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := fabricv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Fabric scheme: %v", err)
	}

	cell := &fabricv1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testCellName, UID: testCellUID},
		Spec:       fabricv1alpha1.CellSpec{NodeRef: fabricv1alpha1.LocalObjectReference{Name: testNodeName}},
	}
	node := &fabricv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{Name: testNodeName}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&fabricv1alpha1.Cell{}).WithObjects(cell, node).Build()
	r := &CellReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cell)}); err != nil {
		t.Fatalf("reconciling Cell: %v", err)
	}

	actual := &fabricv1alpha1.Node{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(node), actual); err != nil {
		t.Fatalf("getting Node: %v", err)
	}
	if actual.Spec.CellRef != nil {
		t.Fatalf("controller manager claimed Node: %#v", actual.Spec.CellRef)
	}
	actualCell := &fabricv1alpha1.Cell{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cell), actualCell); err != nil {
		t.Fatalf("getting Cell: %v", err)
	}
	condition := meta.FindStatusCondition(actualCell.Status.Conditions, "Bound")
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "Unbound" {
		t.Fatalf("unexpected Bound condition: %#v", condition)
	}
}

func TestCellReconcilerDoesNotReplaceBinding(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := fabricv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Fabric scheme: %v", err)
	}

	existingRef := &fabricv1alpha1.NamespacedUIDReference{Namespace: testNamespace, Name: testCellName, UID: testCellUID}
	existingCell := &fabricv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{
		Namespace: existingRef.Namespace,
		Name:      existingRef.Name,
		UID:       existingRef.UID,
	}}
	cell := &fabricv1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "cell-b", UID: types.UID("cell-b-uid")},
		Spec:       fabricv1alpha1.CellSpec{NodeRef: fabricv1alpha1.LocalObjectReference{Name: testNodeName}},
	}
	node := &fabricv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: testNodeName},
		Spec:       fabricv1alpha1.NodeSpec{CellRef: existingRef},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&fabricv1alpha1.Cell{}).
		WithObjects(existingCell, cell, node).Build()
	r := &CellReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cell)}); err != nil {
		t.Fatalf("reconciling Cell: %v", err)
	}

	actual := &fabricv1alpha1.Node{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(node), actual); err != nil {
		t.Fatalf("getting Node: %v", err)
	}
	if actual.Spec.CellRef == nil || *actual.Spec.CellRef != *existingRef {
		t.Fatalf("binding was replaced: got %#v, want %#v", actual.Spec.CellRef, existingRef)
	}
}

func TestCellReconcilerWaitsForNode(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := fabricv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Fabric scheme: %v", err)
	}

	cell := &fabricv1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testCellName, UID: testCellUID},
		Spec:       fabricv1alpha1.CellSpec{NodeRef: fabricv1alpha1.LocalObjectReference{Name: testNodeName}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&fabricv1alpha1.Cell{}).WithObjects(cell).Build()
	r := &CellReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cell)}); err != nil {
		t.Fatalf("reconciling Cell without Node: %v", err)
	}
	actual := &fabricv1alpha1.Cell{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cell), actual); err != nil {
		t.Fatalf("getting Cell: %v", err)
	}
	condition := meta.FindStatusCondition(actual.Status.Conditions, "Bound")
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "NodeNotFound" {
		t.Fatalf("unexpected Bound condition: %#v", condition)
	}
}

func TestCellReconcilerDoesNotModifyNodeBindings(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := fabricv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Fabric scheme: %v", err)
	}

	cell := &fabricv1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testCellName, UID: testCellUID},
		Spec:       fabricv1alpha1.CellSpec{NodeRef: fabricv1alpha1.LocalObjectReference{Name: testNodeName}},
	}
	cellRef := &fabricv1alpha1.NamespacedUIDReference{Namespace: cell.Namespace, Name: cell.Name, UID: cell.UID}
	desiredNode := &fabricv1alpha1.Node{ObjectMeta: metav1.ObjectMeta{Name: testNodeName}}
	staleNode := &fabricv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-b"},
		Spec:       fabricv1alpha1.NodeSpec{CellRef: cellRef},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&fabricv1alpha1.Cell{}).
		WithObjects(cell, desiredNode, staleNode).Build()
	r := &CellReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cell)}); err != nil {
		t.Fatalf("reconciling Cell: %v", err)
	}

	actualDesiredNode := &fabricv1alpha1.Node{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(desiredNode), actualDesiredNode); err != nil {
		t.Fatalf("getting desired Node: %v", err)
	}
	if actualDesiredNode.Spec.CellRef != nil {
		t.Fatalf("controller manager claimed desired Node: %#v", actualDesiredNode.Spec.CellRef)
	}

	actualStaleNode := &fabricv1alpha1.Node{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(staleNode), actualStaleNode); err != nil {
		t.Fatalf("getting stale Node: %v", err)
	}
	if actualStaleNode.Spec.CellRef == nil || *actualStaleNode.Spec.CellRef != *cellRef {
		t.Fatalf("controller manager modified stale binding: %#v", actualStaleNode.Spec.CellRef)
	}
}

func TestCellReconcilerDoesNotReleaseNodeWhenCellIsGone(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := fabricv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Fabric scheme: %v", err)
	}

	cellKey := client.ObjectKey{Namespace: testNamespace, Name: testCellName}
	node := &fabricv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: testNodeName},
		Spec: fabricv1alpha1.NodeSpec{CellRef: &fabricv1alpha1.NamespacedUIDReference{
			Namespace: cellKey.Namespace,
			Name:      cellKey.Name,
			UID:       testCellUID,
		}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	r := &CellReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: cellKey}); err != nil {
		t.Fatalf("reconciling deleted Cell: %v", err)
	}

	actual := &fabricv1alpha1.Node{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(node), actual); err != nil {
		t.Fatalf("getting Node: %v", err)
	}
	if actual.Spec.CellRef == nil {
		t.Fatal("controller manager released Node binding")
	}
}

func TestCellReconcilerReportsNodeAlreadyBound(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := fabricv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Fabric scheme: %v", err)
	}

	cell := &fabricv1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "cell-b", UID: types.UID("cell-b-uid")},
		Spec:       fabricv1alpha1.CellSpec{NodeRef: fabricv1alpha1.LocalObjectReference{Name: testNodeName}},
	}
	node := &fabricv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: testNodeName},
		Spec: fabricv1alpha1.NodeSpec{CellRef: &fabricv1alpha1.NamespacedUIDReference{
			Namespace: testNamespace,
			Name:      testCellName,
			UID:       testCellUID,
		}},
	}
	existingCell := &fabricv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{
		Namespace: testNamespace,
		Name:      testCellName,
		UID:       testCellUID,
	}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&fabricv1alpha1.Cell{}).
		WithObjects(existingCell, cell, node).Build()
	r := &CellReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cell)}); err != nil {
		t.Fatalf("reconciling Cell: %v", err)
	}

	actual := &fabricv1alpha1.Cell{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cell), actual); err != nil {
		t.Fatalf("getting Cell: %v", err)
	}
	condition := meta.FindStatusCondition(actual.Status.Conditions, "Bound")
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "NodeAlreadyBound" {
		t.Fatalf("unexpected Bound condition: %#v", condition)
	}
}

func TestCellReconcilerDoesNotReclaimStaleDesiredNodeReference(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := fabricv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Fabric scheme: %v", err)
	}

	cell := &fabricv1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testCellName, UID: testCellUID},
		Spec:       fabricv1alpha1.CellSpec{NodeRef: fabricv1alpha1.LocalObjectReference{Name: testNodeName}},
	}
	node := &fabricv1alpha1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: testNodeName},
		Spec: fabricv1alpha1.NodeSpec{CellRef: &fabricv1alpha1.NamespacedUIDReference{
			Namespace: testNamespace,
			Name:      "deleted-cell",
			UID:       types.UID("deleted-cell-uid"),
		}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&fabricv1alpha1.Cell{}).
		WithObjects(cell, node).Build()
	r := &CellReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cell)}); err != nil {
		t.Fatalf("reconciling Cell: %v", err)
	}

	actual := &fabricv1alpha1.Node{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(node), actual); err != nil {
		t.Fatalf("getting Node: %v", err)
	}
	want := &fabricv1alpha1.NamespacedUIDReference{Namespace: testNamespace, Name: "deleted-cell", UID: types.UID("deleted-cell-uid")}
	if actual.Spec.CellRef == nil || *actual.Spec.CellRef != *want {
		t.Fatalf("controller manager replaced stale reference: got %#v, want %#v", actual.Spec.CellRef, want)
	}
}
