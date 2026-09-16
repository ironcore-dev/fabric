// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CellSpec defines the desired state of Cell.
// +kubebuilder:validation:XValidation:rule="self.nodeRef == oldSelf.nodeRef",message="nodeRef is immutable"
type CellSpec struct {
	NodeRef LocalObjectReference `json:"nodeRef"`

	ID       string   `json:"id"`
	Hostname string   `json:"hostname,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`

	Peers []Peer `json:"peers,omitempty"`
}

type Peer struct {
	InterfaceRef LocalObjectReference `json:"interfaceRef"`
	DHCPRelay    string               `json:"dhcpRelay,omitempty"`
}

type CellPhase string

const (
	CellPending CellPhase = "Pending"
	CellActive  CellPhase = "Active"
	CellExpired CellPhase = "Expired"
	CellFailed  CellPhase = "Failed"
)

// CellStatus defines the observed state of Cell.
type CellStatus struct {
	// Phase is the phase a Cell is in.
	Phase CellPhase `json:"phase,omitempty"`
	// Conditions represent the current state of the Cell resource.
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
// +kubebuilder:resource
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.spec.nodeRef.name`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.spec.phase`

// Cell is the Schema for the cells API
type Cell struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Cell
	// +required
	Spec CellSpec `json:"spec"`

	// status defines the observed state of Cell
	// +optional
	Status CellStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CellList contains a list of Cell
type CellList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Cell `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Cell{}, &CellList{})
}
