// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CellConfigInstanceConditionReady is set to True when the Cell owned by the
// instance exists and has reached phase Active.
const CellConfigInstanceConditionReady = "Ready"

// CellConfigInstanceSpec defines the desired state of CellConfigInstance.
// Every field except enabled is immutable.
// +kubebuilder:validation:XValidation:rule="self.nodeRef == oldSelf.nodeRef && self.id == oldSelf.id && (has(self.hostname) == has(oldSelf.hostname) && (!has(self.hostname) || self.hostname == oldSelf.hostname)) && (has(self.ips) == has(oldSelf.ips) && (!has(self.ips) || self.ips == oldSelf.ips)) && (has(self.prefixes) == has(oldSelf.prefixes) && (!has(self.prefixes) || self.prefixes == oldSelf.prefixes)) && (has(self.peers) == has(oldSelf.peers) && (!has(self.peers) || self.peers == oldSelf.peers))",message="only spec.enabled may be changed; all configuration fields are immutable"
type CellConfigInstanceSpec struct {
	// enabled gates whether a live Cell is materialized from this instance.
	// When true, the controller ensures a Cell exists and matches this spec.
	// When false, the controller deletes the Cell this instance created.
	// +optional
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// CellConfigSpec contains the effective CellConfig
	CellConfigSpec `json:",inline"`
}

// CellConfigInstanceStatus defines the observed state of a CellConfigInstance.
type CellConfigInstanceStatus struct {
	// observedGeneration is the generation of the instance most recently
	// reconciled by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// cellRef points at the Cell this instance owns, if any.
	// +optional
	CellRef *LocalObjectReference `json:"cellRef,omitempty"`

	// conditions represent the current state of the CellConfigInstance.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=cci
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Cell",type=string,JSONPath=`.status.cellRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CellConfigInstance is the Schema for the cellconfiginstances API
type CellConfigInstance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of the CellConfigInstance
	// +required
	Spec CellConfigInstanceSpec `json:"spec"`

	// status defines the observed state of the CellConfigInstance
	// +optional
	Status CellConfigInstanceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CellConfigInstanceList contains a list of CellConfigInstance
type CellConfigInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CellConfigInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CellConfigInstance{}, &CellConfigInstanceList{})
}
