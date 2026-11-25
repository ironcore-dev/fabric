// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// CellConfigRolloutConditionProgressing is True while the rollout is still
	// converging selected CellConfigs onto CellConfigInstances.
	CellConfigRolloutConditionProgressing = "Progressing"
	// CellConfigRolloutConditionHealthy is False once failed cells exceed the
	// rollout's tolerance.
	CellConfigRolloutConditionHealthy = "Healthy"
)

// RollingUpdateCellConfig controls the rate at which a rollout updates cells.
type RollingUpdateCellConfig struct {
	// maxUnavailable is the maximum number of cells that may be unavailable
	// during the update, as an absolute number or a percentage of desired
	// cells. A cell is available once its Cell has reached phase Active.
	// TODO: Check if this type is correct
	// +optional
	// +kubebuilder:default=1
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

// CellConfigRolloutStrategy describes how a rollout replaces cells.
type CellConfigRolloutStrategy struct {
	// rollingUpdate configures the rolling update strategy.
	// +optional
	RollingUpdate *RollingUpdateCellConfig `json:"rollingUpdate,omitempty"`
}

// CellConfigRolloutSpec defines the desired state of a CellConfigRollout.
type CellConfigRolloutSpec struct {
	// configSelector selects the CellConfigs managed by this rollout.
	// +required
	ConfigSelector *metav1.LabelSelector `json:"configSelector"`

	// strategy is the strategy used to roll out configuration changes.
	// +optional
	Strategy CellConfigRolloutStrategy `json:"strategy,omitempty"`

	// revisionHistoryLimit is the number of old revision
	// CellConfigInstances to retain per cell for rollback.
	// +optional
	// +kubebuilder:default=10
	RevisionHistoryLimit *int32 `json:"revisionHistoryLimit,omitempty"`
}

// CellConfigRolloutStatus defines the observed state of a CellConfigRollout.
type CellConfigRolloutStatus struct {
	// observedGeneration is the generation of the rollout most recently
	// reconciled by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// currentRevision is a monotonically increasing counter bumped whenever the
	// set of materialized CellConfigs changes.
	// +optional
	CurrentRevision int64 `json:"currentRevision,omitempty"`

	// currentRevisionHash is the hash of the materialized CellConfig set that
	// produced currentRevision.
	// +optional
	CurrentRevisionHash string `json:"currentRevisionHash,omitempty"`

	// desiredCells is the number of valid selected CellConfigs.
	// +optional
	DesiredCells int32 `json:"desiredCells"`

	// updatedCells is the number of instances whose spec matches the desired
	// configuration.
	// +optional
	UpdatedCells int32 `json:"updatedCells"`

	// availableCells is the number of instances whose Cell has reached phase
	// Active.
	// +optional
	AvailableCells int32 `json:"availableCells"`

	// failedCells is the number of instances whose Cell has reached phase
	// Failed.
	// +optional
	FailedCells int32 `json:"failedCells"`

	// conditions represent the current state of the CellConfigRollout.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=ccr
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.status.desiredCells`
// +kubebuilder:printcolumn:name="Updated",type=integer,JSONPath=`.status.updatedCells`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.availableCells`
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=`.status.failedCells`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CellConfigRollout is the Schema for the cellconfigrollouts API
type CellConfigRollout struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of the CellConfigRollout
	// +required
	Spec CellConfigRolloutSpec `json:"spec"`

	// status defines the observed state of the CellConfigRollout
	// +optional
	Status CellConfigRolloutStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CellConfigRolloutList contains a list of CellConfigRollout
type CellConfigRolloutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CellConfigRollout `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CellConfigRollout{}, &CellConfigRolloutList{})
}
