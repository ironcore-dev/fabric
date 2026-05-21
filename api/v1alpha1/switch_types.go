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

// SwitchSpec defines the desired state of Switch
type SwitchSpec struct {
	DeviceRef LocalObjectReference `json:"deviceRef"`

	Hostname string   `json:"hostname,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`

	VLANs []SwitchVLAN `json:"vlans,omitempty"`

	BGP *SwitchBGP `json:"bgp,omitempty"`
}

type SwitchBGP struct {
	ASN        int32                `json:"asn"`
	RouterID   string               `json:"routerID"`
	PeerGroups []SwitchBGPPeerGroup `json:"peerGroups,omitempty"`
}

type SwitchBGPPeerGroup struct {
	Name      string              `json:"name"`
	Neighbors []SwitchBGPNeighbor `json:"neighbors,omitempty"`
}

type SwitchBGPNeighbor struct {
	VLANID             int32                 `json:"vlanID,omitempty"`
	DeviceInterfaceRef *LocalObjectReference `json:"deviceInterfaceRef,omitempty"`
}

type SwitchVLAN struct {
	ID        int32              `json:"id"`
	Prefix    string             `json:"prefix"`
	DHCPRelay string             `json:"dhcpRelay,omitempty"`
	Members   []SwitchVLANMember `json:"members"`
}

type SwitchVLANMember struct {
	DeviceInterfaceRef *LocalObjectReference `json:"deviceInterfaceRef,omitempty"`
}

type SwitchPhase string

const (
	SwitchPending SwitchPhase = "Pending"
	SwitchActive  SwitchPhase = "Active"
	SwitchExpired SwitchPhase = "Expired"
	SwitchFailed  SwitchPhase = "Failed"
)

// SwitchStatus defines the observed state of Switch.
type SwitchStatus struct {
	// Phase is the phase a Switch is in.
	Phase SwitchPhase `json:"phase,omitempty"`
	// Conditions represent the current state of the Switch resource.
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
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.spec.phase`

// Switch is the Schema for the switches API
type Switch struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Switch
	// +required
	Spec SwitchSpec `json:"spec"`

	// status defines the observed state of Switch
	// +optional
	Status SwitchStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SwitchList contains a list of Switch
type SwitchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Switch `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Switch{}, &SwitchList{})
}
