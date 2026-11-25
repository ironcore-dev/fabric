// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CellConfigSpec defines the desired state of CellConfig.
type CellConfigSpec struct {
	// nodeRef selects the Node this configuration targets.
	// +required
	NodeRef LocalObjectReference `json:"nodeRef"`

	// id is the cell identifier.
	// +required
	ID string `json:"id"`

	// hostname is the cell hostname.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// ips are the addresses assigned to the cell.
	// +optional
	IPs []string `json:"ips,omitempty"`

	// prefixes are the prefixes advertised by the cell.
	// +optional
	Prefixes []string `json:"prefixes,omitempty"`

	// peers are the peering interfaces of the cell.
	// +optional
	Peers []Peer `json:"peers,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=cc
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.spec.nodeRef.name`
// +kubebuilder:printcolumn:name="ID",type=string,JSONPath=`.spec.id`
// +kubebuilder:printcolumn:name="Hostname",type=string,JSONPath=`.spec.hostname`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CellConfig is the Schema for the cellconfigs API
type CellConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired configuration of the cell
	// +required
	Spec CellConfigSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// CellConfigList contains a list of CellConfig
type CellConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CellConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CellConfig{}, &CellConfigList{})
}
