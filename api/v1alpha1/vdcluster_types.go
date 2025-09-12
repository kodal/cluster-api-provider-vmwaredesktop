/*
Copyright 2025.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// ClusterFinalizer allows ReconcileVDCluster to clean up Vmware Fusion resources associated with VDCluster before
	// removing it from the apiserver.
	ClusterFinalizer = "vdcluster.infrastructure.cluster.x-k8s.io"
)

// VDClusterSpec defines the desired state of VDCluster.
type VDClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self.port > 0 && self.port < 65536",message="port must be within 1-65535"
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`

	// +optional
	IPAMPoolRef *corev1.ObjectReference `json:"ipamPoolRef,omitempty"`
}

// VDClusterStatus defines the observed state of VDCluster.
type VDClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Ready indicates that the cluster is ready.
	// +optional
	// +kubebuilder:default=false
	Ready bool `json:"ready"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// VDCluster is the Schema for the vdclusters API.
type VDCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VDClusterSpec   `json:"spec,omitempty"`
	Status VDClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VDClusterList contains a list of VDCluster.
type VDClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VDCluster `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &VDCluster{}, &VDClusterList{})
}
