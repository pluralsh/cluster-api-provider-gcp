/*
Copyright 2023 The Kubernetes Authors.

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

package scope

import (
	"context"
	"fmt"

	"sigs.k8s.io/cluster-api-provider-gcp/cloud"
	"sigs.k8s.io/cluster-api-provider-gcp/util/location"

	"sigs.k8s.io/cluster-api/util/conditions"

	compute "cloud.google.com/go/compute/apiv1"
	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/pkg/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterv1exp "sigs.k8s.io/cluster-api/exp/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1exp "sigs.k8s.io/cluster-api-provider-gcp/exp/api/v1beta1"
)

// ManagedMachinePoolScopeParams defines the input parameters used to create a new Scope.
type ManagedMachinePoolScopeParams struct {
	ManagedClusterClient        *container.ClusterManagerClient
	InstanceGroupManagersClient *compute.InstanceGroupManagersClient
	Client                      client.Client
	Cluster                     *clusterv1.Cluster
	MachinePool                 *clusterv1exp.MachinePool
	GCPManagedCluster           *infrav1exp.GCPManagedCluster
	GCPManagedControlPlane      *infrav1exp.GCPManagedControlPlane
	GCPManagedMachinePool       *infrav1exp.GCPManagedMachinePool
}

// NewManagedMachinePoolScope creates a new Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewManagedMachinePoolScope(ctx context.Context, params ManagedMachinePoolScopeParams) (*ManagedMachinePoolScope, error) {
	if params.Cluster == nil {
		return nil, errors.New("failed to generate new scope from nil Cluster")
	}
	if params.MachinePool == nil {
		return nil, errors.New("failed to generate new scope from nil MachinePool")
	}
	if params.GCPManagedCluster == nil {
		return nil, errors.New("failed to generate new scope from nil GCPManagedCluster")
	}
	if params.GCPManagedControlPlane == nil {
		return nil, errors.New("failed to generate new scope from nil GCPManagedControlPlane")
	}
	if params.GCPManagedMachinePool == nil {
		return nil, errors.New("failed to generate new scope from nil GCPManagedMachinePool")
	}

	if params.ManagedClusterClient == nil {
		managedClusterClient, err := newClusterManagerClient(ctx, params.GCPManagedCluster.Spec.CredentialsRef, params.Client)
		if err != nil {
			return nil, errors.Errorf("failed to create gcp managed cluster client: %v", err)
		}
		params.ManagedClusterClient = managedClusterClient
	}
	if params.InstanceGroupManagersClient == nil {
		instanceGroupManagersClient, err := newInstanceGroupManagerClient(ctx, params.GCPManagedCluster.Spec.CredentialsRef, params.Client)
		if err != nil {
			return nil, errors.Errorf("failed to create gcp instance group manager client: %v", err)
		}
		params.InstanceGroupManagersClient = instanceGroupManagersClient
	}

	helper, err := patch.NewHelper(params.GCPManagedMachinePool, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init patch helper")
	}

	return &ManagedMachinePoolScope{
		client:                 params.Client,
		Cluster:                params.Cluster,
		MachinePool:            params.MachinePool,
		GCPManagedControlPlane: params.GCPManagedControlPlane,
		GCPManagedMachinePool:  params.GCPManagedMachinePool,
		mcClient:               params.ManagedClusterClient,
		migClient:              params.InstanceGroupManagersClient,
		patchHelper:            helper,
	}, nil
}

// ManagedMachinePoolScope defines the basic context for an actuator to operate upon.
type ManagedMachinePoolScope struct {
	client      client.Client
	patchHelper *patch.Helper

	Cluster                *clusterv1.Cluster
	MachinePool            *clusterv1exp.MachinePool
	GCPManagedCluster      *infrav1exp.GCPManagedCluster
	GCPManagedControlPlane *infrav1exp.GCPManagedControlPlane
	GCPManagedMachinePool  *infrav1exp.GCPManagedMachinePool
	mcClient               *container.ClusterManagerClient
	migClient              *compute.InstanceGroupManagersClient
}

// PatchObject persists the managed control plane configuration and status.
func (s *ManagedMachinePoolScope) PatchObject() error {
	return s.patchHelper.Patch(
		context.TODO(),
		s.GCPManagedMachinePool,
		patch.WithOwnedConditions{Conditions: []clusterv1.ConditionType{
			infrav1exp.GKEMachinePoolReadyCondition,
			infrav1exp.GKEMachinePoolCreatingCondition,
			infrav1exp.GKEMachinePoolUpdatingCondition,
			infrav1exp.GKEMachinePoolDeletingCondition,
		}})
}

// Close closes the current scope persisting the managed control plane configuration and status.
func (s *ManagedMachinePoolScope) Close() error {
	s.mcClient.Close()
	s.migClient.Close()
	return s.PatchObject()
}

// ConditionSetter return a condition setter (which is GCPManagedMachinePool itself).
func (s *ManagedMachinePoolScope) ConditionSetter() conditions.Setter {
	return s.GCPManagedMachinePool
}

// ManagedMachinePoolClient returns a client used to interact with GKE.
func (s *ManagedMachinePoolScope) ManagedMachinePoolClient() *container.ClusterManagerClient {
	return s.mcClient
}

// InstanceGroupManagersClient returns a client used to interact with GCP MIG.
func (s *ManagedMachinePoolScope) InstanceGroupManagersClient() *compute.InstanceGroupManagersClient {
	return s.migClient
}

// NodePoolVersion returns the k8s version of the node pool.
func (s *ManagedMachinePoolScope) NodePoolVersion() *string {
	return infrav1exp.NormalizeMachineVersion(s.MachinePool.Spec.Template.Spec.Version)
}

// ConvertToSdkNodePool converts a node pool to format that is used by GCP SDK.
func ConvertToSdkNodePool(nodePool infrav1exp.GCPManagedMachinePool, machinePool clusterv1exp.MachinePool, regional bool) *containerpb.NodePool {
	replicas := *machinePool.Spec.Replicas
	if regional {
		replicas /= cloud.DefaultNumRegionsPerZone
	}
	nodePoolName := nodePool.Spec.NodePoolName
	if len(nodePoolName) == 0 {
		nodePoolName = nodePool.Name
	}

	sdkNodePool := containerpb.NodePool{
		Name:             nodePoolName,
		InitialNodeCount: replicas,
		Autoscaling:      convertToSdkNodePoolAutoscaling(nodePool.Spec.Scaling),
		Management:       convertToSdkNodeManagement(nodePool.Spec.Management),
		Config: &containerpb.NodeConfig{
			MachineType: nodePool.Spec.MachineType,
			DiskSizeGb:  nodePool.Spec.DiskSizeGb,
			DiskType:    nodePool.Spec.DiskType,
			Labels:      nodePool.Spec.KubernetesLabels,
			Taints:      infrav1exp.ConvertToSdkTaint(nodePool.Spec.KubernetesTaints),
			Metadata:    nodePool.Spec.AdditionalLabels,
			ImageType:   nodePool.Spec.ImageType,
			Preemptible: nodePool.Spec.Preemptible != nil && *nodePool.Spec.Preemptible,
			Spot:        nodePool.Spec.Spot != nil && *nodePool.Spec.Spot,
		},
	}

	if machinePool.Spec.Template.Spec.Version != nil {
		sdkNodePool.Version = *infrav1exp.NormalizeMachineVersion(machinePool.Spec.Template.Spec.Version)
	}

	return &sdkNodePool
}

// ConvertToSdkNodePools converts node pools to format that is used by GCP SDK.
func ConvertToSdkNodePools(nodePools []infrav1exp.GCPManagedMachinePool, machinePools []clusterv1exp.MachinePool, regional bool) []*containerpb.NodePool {
	res := make([]*containerpb.NodePool, 0)
	for i := range nodePools {
		res = append(res, ConvertToSdkNodePool(nodePools[i], machinePools[i], regional))
	}
	return res
}

// convertToSdkNodePoolAutoscaling converts node pool autoscaling to format that is used by GCP SDK.
func convertToSdkNodePoolAutoscaling(scaling *infrav1exp.NodePoolAutoScaling) *containerpb.NodePoolAutoscaling {
	if scaling == nil {
		return nil
	}

	result := &containerpb.NodePoolAutoscaling{Enabled: true}

	if scaling.MinCount != nil {
		result.MinNodeCount = *scaling.MinCount
	}

	if scaling.MaxCount != nil {
		result.MaxNodeCount = *scaling.MaxCount
	}

	return result
}

// convertToSdkNodeManagement converts node management to format that is used by GCP SDK.
func convertToSdkNodeManagement(management *infrav1exp.NodeManagement) *containerpb.NodeManagement {
	if management == nil {
		return nil
	}

	result := &containerpb.NodeManagement{}

	if management.AutoUpgrade != nil {
		result.AutoUpgrade = *management.AutoUpgrade
	}

	if management.AutoRepair != nil {
		result.AutoRepair = *management.AutoRepair
	}

	return result
}

// SetReplicas sets the replicas count in status.
func (s *ManagedMachinePoolScope) SetReplicas(replicas int32) {
	s.GCPManagedMachinePool.Status.Replicas = replicas
}

// NodePoolName returns the node pool name.
func (s *ManagedMachinePoolScope) NodePoolName() string {
	if len(s.GCPManagedMachinePool.Spec.NodePoolName) > 0 {
		return s.GCPManagedMachinePool.Spec.NodePoolName
	}
	return s.GCPManagedMachinePool.Name
}

// Region returns the region of the GKE node pool.
func (s *ManagedMachinePoolScope) Region() string {
	loc, _ := location.Parse(s.GCPManagedControlPlane.Spec.Location)
	return loc.Region
}

// NodePoolLocation returns the location of the node pool.
func (s *ManagedMachinePoolScope) NodePoolLocation() string {
	return fmt.Sprintf("projects/%s/locations/%s/clusters/%s", s.GCPManagedControlPlane.Spec.Project, s.Region(), s.GCPManagedControlPlane.Spec.ClusterName)
}

// NodePoolFullName returns the full name of the node pool.
func (s *ManagedMachinePoolScope) NodePoolFullName() string {
	return fmt.Sprintf("%s/nodePools/%s", s.NodePoolLocation(), s.NodePoolName())
}
