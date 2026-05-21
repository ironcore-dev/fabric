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

// HostSpec defines the desired state of Host
type HostSpec struct {
	ServerRef LocalObjectReference `json:"serverRef"`

	IPs      []string `json:"ips,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`

	BGP *HostBGP `json:"bgp,omitempty"`
}

type HostBGP struct {
	ASN        int32              `json:"asn"`
	RouterID   string             `json:"routerID"`
	PeerGroups []HostBGPPeerGroup `json:"peerGroups,omitempty"`
}

type HostBGPPeerGroup struct {
	Name      string            `json:"name"`
	Neighbors []HostBGPNeighbor `json:"neighbors,omitempty"`
}

type HostBGPNeighbor struct {
	ServerInterfaceRef *LocalObjectReference `json:"serverInterfaceRef,omitempty"`
}

// HostStatus defines the observed state of Host.
type HostStatus struct {
	// Conditions represent the current state of the Host resource.
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

// Host is the Schema for the hosts API
type Host struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Host
	// +required
	Spec HostSpec `json:"spec"`

	// status defines the observed state of Host
	// +optional
	Status HostStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HostList contains a list of Host
type HostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Host `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Host{}, &HostList{})
}
