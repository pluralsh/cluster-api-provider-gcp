/*
Copyright 2022 The Kubernetes Authors.

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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	infrav1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

const (
	// ManagedMachinePoolFinalizer allows Reconcile to clean up GCP resources associated with the GCPManagedMachinePool before
	// removing it from the apiserver.
	ManagedMachinePoolFinalizer = "gcpmanagedmachinepool.infrastructure.cluster.x-k8s.io"
)

// GCPManagedMachinePoolSpec defines the desired state of GCPManagedMachinePool.
type GCPManagedMachinePoolSpec struct {
	// NodePoolName specifies the name of the GKE node pool corresponding to this MachinePool. If you don't specify a name
	// then a default name will be created based on the namespace and name of the managed machine pool.
	// +optional
	NodePoolName string `json:"nodePoolName,omitempty"`
	// Scaling specifies scaling for the node pool
	// +optional
	Scaling *NodePoolAutoScaling `json:"scaling,omitempty"`
	// Management configuration for this NodePool.
	// +optional
	Management *NodeManagement `json:"management,omitempty"`
	// KubernetesLabels specifies the labels to apply to the nodes of the node pool.
	// +optional
	KubernetesLabels infrav1.Labels `json:"kubernetesLabels,omitempty"`
	// KubernetesTaints specifies the taints to apply to the nodes of the node pool.
	// +optional
	KubernetesTaints Taints `json:"kubernetesTaints,omitempty"`
	// AdditionalLabels is an optional set of tags to add to GCP resources managed by the GCP provider, in addition to the
	// ones added by default.
	// +optional
	AdditionalLabels infrav1.Labels `json:"additionalLabels,omitempty"`
	// ProviderIDList are the provider IDs of instances in the
	// managed instance group corresponding to the nodegroup represented by this
	// machine pool
	// +optional
	ProviderIDList []string `json:"providerIDList,omitempty"`
	// The name of a Google Compute Engine [machine
	// type](https://cloud.google.com/compute/docs/machine-types)
	//
	// If unspecified, the default machine type is `e2-medium`.
	MachineType string `json:"machineType,omitempty"`
	// Size of the disk attached to each node, specified in GB.
	// The smallest allowed disk size is 10GB.
	//
	// If unspecified, the default disk size is 100GB.
	DiskSizeGb int32 `json:"diskSizeGb,omitempty"`
	// Type of the disk attached to each node (e.g. 'pd-standard', 'pd-ssd' or 'pd-balanced')
	//
	// If unspecified, the default disk type is 'pd-standard'
	DiskType string `json:"diskType,omitempty"`
	// ImageType is the image type to use for this node. Note that for a given image type,
	// the latest version of it will be used. Please see
	// https://cloud.google.com/kubernetes-engine/docs/concepts/node-images for
	// available image types.
	ImageType string `json:"imageType,omitempty"`
	// Whether the nodes are created as preemptible VM instances. See:
	// https://cloud.google.com/compute/docs/instances/preemptible for more
	// information about preemptible VM instances.
	Preemptible *bool `json:"preemptible,omitempty"`
	// Spot flag for enabling Spot VM, which is a rebrand of the existing preemptible flag.
	Spot *bool `json:"spot,omitempty"`
}

// GCPManagedMachinePoolStatus defines the observed state of GCPManagedMachinePool.
type GCPManagedMachinePoolStatus struct {
	Ready bool `json:"ready"`
	// Replicas is the most recently observed number of replicas.
	// +optional
	Replicas int32 `json:"replicas"`
	// Conditions specifies the cpnditions for the managed machine pool
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.mode"
// +kubebuilder:resource:path=gcpmanagedmachinepools,scope=Namespaced,categories=cluster-api,shortName=gcpmmp
// +kubebuilder:storageversion
// +kubebuilder:subresource:status

// GCPManagedMachinePool is the Schema for the gcpmanagedmachinepools API.
type GCPManagedMachinePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GCPManagedMachinePoolSpec   `json:"spec,omitempty"`
	Status GCPManagedMachinePoolStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GCPManagedMachinePoolList contains a list of GCPManagedMachinePool.
type GCPManagedMachinePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GCPManagedMachinePool `json:"items"`
}

// NodePoolAutoScaling specifies scaling options.
type NodePoolAutoScaling struct {
	// MinCount is a minimum number of nodes for one location in the NodePool. Must be >= 1 and
	// <= maxCount.
	MinCount *int32 `json:"minCount,omitempty"`
	// MaxCount is a maximum number of nodes for one location in the NodePool. Must be >=
	// maxCount. There has to be enough quota to scale up the cluster.
	MaxCount *int32 `json:"maxCount,omitempty"`
}

// NodeManagement defines the set of node management services turned on for the
// node pool.
type NodeManagement struct {
	// AutoUpgrade is a flag that specifies whether node auto-upgrade is enabled for the node
	// pool. If enabled, node auto-upgrade helps keep the nodes in your node pool
	// up to date with the latest release version of Kubernetes.
	// +optional
	AutoUpgrade *bool `json:"autoUpgrade,omitempty"`
	// AutoRepair is a flag that specifies whether the node auto-repair is enabled for the node
	// pool. If enabled, the nodes in this node pool will be monitored and, if
	// they fail health checks too many times, an automatic repair action will be
	// triggered.
	// +optional
	AutoRepair *bool `json:"autoRepair,omitempty"`
}

// GetConditions returns the machine pool conditions.
func (r *GCPManagedMachinePool) GetConditions() clusterv1.Conditions {
	return r.Status.Conditions
}

// SetConditions sets the status conditions for the GCPManagedMachinePool.
func (r *GCPManagedMachinePool) SetConditions(conditions clusterv1.Conditions) {
	r.Status.Conditions = conditions
}

func init() {
	SchemeBuilder.Register(&GCPManagedMachinePool{}, &GCPManagedMachinePoolList{})
}
