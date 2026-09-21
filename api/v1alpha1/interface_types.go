// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	InterfaceKind = "Interface"
)

// InterfaceSpec defines the desired state of Interface.
type InterfaceSpec struct {
	// NodeRef references the node this interface belongs to.
	NodeRef LocalObjectReference `json:"nodeRef"`
	// Handle is the provider specific handle of this interface.
	Handle string `json:"handle,omitempty"`
	// AdminState is the administrative state of the interface.
	// +kubebuilder:default=Up
	AdminState AdminState `json:"adminState"`
}

type AdminState string

const (
	AdminStateUp   AdminState = "Up"
	AdminStateDown AdminState = "Down"
)

type OperationState string

const (
	OperationStateUp      OperationState = "Up"
	OperationStateDown    OperationState = "Down"
	OperationStateUnknown OperationState = "Unknown"
)

// InterfaceStatus defines the observed state of Interface.
type InterfaceStatus struct {
	// OperationState is the actual operation state the interface has.
	OperationState OperationState `json:"operationState,omitempty"`

	// Conditions represent the current state of the Interface resource.
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
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.spec.nodeRef.name`
// +kubebuilder:printcolumn:name="Handle",type=string,JSONPath=`.spec.handle`
// +kubebuilder:printcolumn:name="Admin_State",type=string,JSONPath=`.spec.adminState`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.operationState`

// Interface is the Schema for the interfaces API
type Interface struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Interface
	// +required
	Spec InterfaceSpec `json:"spec"`

	// status defines the observed state of Interface
	// +optional
	Status InterfaceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// InterfaceList contains a list of Interface
type InterfaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Interface `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Interface{}, &InterfaceList{})
}
