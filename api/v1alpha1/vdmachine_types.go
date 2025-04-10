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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// MachineFinalizer allows cleaning up resources associated with
	// DockerMachine before removing it from the API Server.
	MachineFinalizer = "vdmachine.infrastructure.cluster.x-k8s.io"
)

// VDMachineSpec defines the desired state of VDMachine.
type VDMachineSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	ProviderID *string `json:"providerID,omitempty"`

	// ID of the template VM to clone.
	TemplateID string `json:"templateID"`

	// Cpu is the number of CPUs for the VM.
	// +kubebuilder:validation:Minimum=1
	// +optional
	Cpu *int32 `json:"cpu,omitempty"`

	// Memory is the amount of memory for the VM.
	// +kubebuilder:validation:MultipleOf=8
	// +optional
	Memory *int32 `json:"memory,omitempty"`

	// Cloud Init network_config
	// +optional
	NetworkConfig *string `json:"networkConfig,omitempty"`
}

// InitializationStatus represents the initialization state of the resource.
type InitializationStatus struct {
	// Provisioned is set to true when the resource has been provisioned.
	// +optional
	Provisioned bool `json:"provisioned,omitempty"`

	// +optional
	BootstrapDataProvided bool `json:"bootstrapDataProvided,omitempty"`
}

// VDMachineStatus defines the observed state of VDMachine.
type VDMachineStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	Ready bool `json:"ready"`

	// Addresses contains the associated addresses for the vmware virtual machine.
	// +optional
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`

	// Conditions defines current service state of the VDMachine.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Initialization represents the initialization state of the VDMachine.
	// +optional
	Initialization InitializationStatus `json:"initialization,omitempty"`

	// Hardware represents the hardware configuration of the VM.
	// +optional
	Hardware VDHardware `json:"hardware,omitempty"`

	// VM power state.
	// +optional
	State *string `json:"state,omitempty"`
}

type VDHardware struct {
	// Cpu is the number of CPUs for the VM.
	// +optional
	Cpu int32 `json:"cpu,omitempty"`
	// Memory is the amount of memory for the VM.
	// +optional
	Memory int32 `json:"memory,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// VDMachine is the Schema for the vdmachines API.
type VDMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VDMachineSpec   `json:"spec,omitempty"`
	Status VDMachineStatus `json:"status,omitempty"`
}

// GetV1Beta2Conditions implements v1beta2.Setter.
func (m *VDMachine) GetV1Beta2Conditions() []metav1.Condition {
	return m.Status.Conditions
}

// SetV1Beta2Conditions implements v1beta2.Setter.
func (m *VDMachine) SetV1Beta2Conditions(conditions []metav1.Condition) {
	m.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// VDMachineList contains a list of VDMachine.
type VDMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VDMachine `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &VDMachine{}, &VDMachineList{})
}
