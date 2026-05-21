/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ServerInterfaceKind = "ServerInterface"
)

// ServerInterfaceSpec defines the desired state of ServerInterface
type ServerInterfaceSpec struct {
	Handle    string               `json:"handle,omitempty"`
	ServerRef LocalObjectReference `json:"serverRef"`
}

// ServerInterfaceStatus defines the observed state of ServerInterface.
type ServerInterfaceStatus struct {
	// Conditions represent the current state of the ServerInterface resource.
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

// ServerInterface is the Schema for the serverinterfaces API
type ServerInterface struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ServerInterface
	// +required
	Spec ServerInterfaceSpec `json:"spec"`

	// status defines the observed state of ServerInterface
	// +optional
	Status ServerInterfaceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ServerInterfaceList contains a list of ServerInterface
type ServerInterfaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ServerInterface `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServerInterface{}, &ServerInterfaceList{})
}
