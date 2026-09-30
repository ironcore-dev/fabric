// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeSpec defines the desired state of Node
type NodeSpec struct {
	// ProviderID is the provider internal ID of the node.
	ProviderID string `json:"providerID,omitempty"`
	// CellRef references the active Cell configuration for this Node, if any.
	CellRef *NamespacedUIDReference `json:"cellRef,omitempty"`
}

// NodeStatus defines the observed state of Node.
type NodeStatus struct {
	// Conditions represent the current state of the Node resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.providerID`
// +kubebuilder:printcolumn:name="Cell",type=string,JSONPath=`.spec.cellRef.name`
// +kubebuilder:printcolumn:name="Cell Namespace",type=string,JSONPath=`.spec.cellRef.namespace`

// Node is the Schema for the nodes API
type Node struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Node
	// +required
	Spec NodeSpec `json:"spec"`

	// status defines the observed state of Node
	// +optional
	Status NodeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NodeList contains a list of Node
type NodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Node `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Node{}, &NodeList{})
}
