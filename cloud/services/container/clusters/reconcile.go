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

package clusters

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-gcp/cloud/scope"
	"sigs.k8s.io/cluster-api-provider-gcp/cloud/services/shared"

	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/googleapis/gax-go/v2/apierror"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1exp "sigs.k8s.io/cluster-api-provider-gcp/exp/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-gcp/util/reconciler"
)

// Reconcile reconcile GKE cluster.
func (s *Service) Reconcile(ctx context.Context) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("service", "container.clusters")
	log.Info("Reconciling cluster resources")

	cluster, err := s.describeCluster(ctx, &log)
	if err != nil {
		s.scope.GCPManagedControlPlane.Status.Initialized = false
		s.scope.GCPManagedControlPlane.Status.Ready = false
		conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Cluster not found, creating")
		s.scope.GCPManagedControlPlane.Status.Initialized = false
		s.scope.GCPManagedControlPlane.Status.Ready = false

		nodePools, _, err := s.scope.GetAllNodePools(ctx)
		if err != nil {
			conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
			conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
			conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneCreatingCondition, infrav1exp.GKEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
			return ctrl.Result{}, err
		}
		if s.scope.IsAutopilotCluster() {
			if len(nodePools) > 0 {
				log.Error(ErrAutopilotClusterMachinePoolsNotAllowed, fmt.Sprintf("%d machine pools defined", len(nodePools)))
				conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneRequiresAtLeastOneNodePoolReason, clusterv1.ConditionSeverityInfo, "")
				conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneRequiresAtLeastOneNodePoolReason, clusterv1.ConditionSeverityInfo, "")
				conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneCreatingCondition, infrav1exp.GKEControlPlaneRequiresAtLeastOneNodePoolReason, clusterv1.ConditionSeverityInfo, "")
				return ctrl.Result{}, ErrAutopilotClusterMachinePoolsNotAllowed
			}
		} else {
			if len(nodePools) == 0 {
				log.Info("At least 1 node pool is required to create GKE cluster with autopilot disabled")
				conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneRequiresAtLeastOneNodePoolReason, clusterv1.ConditionSeverityInfo, "")
				conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneRequiresAtLeastOneNodePoolReason, clusterv1.ConditionSeverityInfo, "")
				conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneCreatingCondition, infrav1exp.GKEControlPlaneRequiresAtLeastOneNodePoolReason, clusterv1.ConditionSeverityInfo, "")
				return ctrl.Result{RequeueAfter: reconciler.DefaultRetryTime}, nil
			}
		}

		if err = s.createCluster(ctx, &log); err != nil {
			log.Error(err, "failed creating cluster")
			conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
			conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
			conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneCreatingCondition, infrav1exp.GKEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
			return ctrl.Result{}, err
		}
		log.Info("Cluster created provisioning in progress")
		conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneCreatingReason, clusterv1.ConditionSeverityInfo, "")
		conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneCreatingReason, clusterv1.ConditionSeverityInfo, "")
		conditions.MarkTrue(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneCreatingCondition)
		return ctrl.Result{RequeueAfter: reconciler.DefaultRetryTime}, nil
	}

	log.V(2).Info("gke cluster found", "status", cluster.Status)
	s.scope.GCPManagedControlPlane.Status.CurrentVersion = cluster.CurrentMasterVersion

	switch cluster.Status {
	case containerpb.Cluster_PROVISIONING:
		log.Info("Cluster provisioning in progress")
		conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneCreatingReason, clusterv1.ConditionSeverityInfo, "")
		conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneCreatingReason, clusterv1.ConditionSeverityInfo, "")
		conditions.MarkTrue(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneCreatingCondition)
		s.scope.GCPManagedControlPlane.Status.Initialized = false
		s.scope.GCPManagedControlPlane.Status.Ready = false
		return ctrl.Result{RequeueAfter: reconciler.DefaultRetryTime}, nil
	case containerpb.Cluster_RECONCILING:
		log.Info("Cluster reconciling in progress")
		conditions.MarkTrue(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneUpdatingCondition)
		s.scope.GCPManagedControlPlane.Status.Initialized = true
		s.scope.GCPManagedControlPlane.Status.Ready = true
		return ctrl.Result{RequeueAfter: reconciler.DefaultRetryTime}, nil
	case containerpb.Cluster_STOPPING:
		log.Info("Cluster stopping in progress")
		conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneDeletingReason, clusterv1.ConditionSeverityInfo, "")
		conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneDeletingReason, clusterv1.ConditionSeverityInfo, "")
		conditions.MarkTrue(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneDeletingCondition)
		s.scope.GCPManagedControlPlane.Status.Initialized = false
		s.scope.GCPManagedControlPlane.Status.Ready = false
		return ctrl.Result{RequeueAfter: reconciler.DefaultRetryTime}, nil
	case containerpb.Cluster_ERROR, containerpb.Cluster_DEGRADED:
		var msg string
		if len(cluster.Conditions) > 0 {
			msg = cluster.Conditions[0].GetMessage()
		}
		log.Error(errors.New("Cluster in error/degraded state"), msg, "name", s.scope.ClusterName())
		conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneErrorReason, clusterv1.ConditionSeverityError, "")
		s.scope.GCPManagedControlPlane.Status.Ready = false
		s.scope.GCPManagedControlPlane.Status.Initialized = false
		return ctrl.Result{}, nil
	case containerpb.Cluster_RUNNING:
		log.Info("Cluster running")
	default:
		statusErr := NewErrUnexpectedClusterStatus(string(cluster.Status))
		log.Error(statusErr, fmt.Sprintf("Unhandled cluster status %s", cluster.Status), "name", s.scope.ClusterName())
		return ctrl.Result{}, statusErr
	}

	needUpdate, updateClusterRequest := s.checkDiffAndPrepareUpdate(cluster, &log)
	if needUpdate {
		log.Info("Update required")
		err = s.updateCluster(ctx, updateClusterRequest, &log)
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Cluster updating in progress")
		conditions.MarkTrue(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneUpdatingCondition)
		s.scope.GCPManagedControlPlane.Status.Initialized = true
		s.scope.GCPManagedControlPlane.Status.Ready = true
		return ctrl.Result{}, nil
	}
	conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneUpdatingCondition, infrav1exp.GKEControlPlaneUpdatedReason, clusterv1.ConditionSeverityInfo, "")

	// Reconcile kubeconfig
	err = s.reconcileKubeconfig(ctx, cluster, &log)
	if err != nil {
		log.Error(err, "Failed to reconcile CAPI kubeconfig")
		return ctrl.Result{}, err
	}
	err = s.reconcileAdditionalKubeconfigs(ctx, cluster, &log)
	if err != nil {
		log.Error(err, "Failed to reconcile additional kubeconfig")
		return ctrl.Result{}, err
	}

	s.scope.SetEndpoint(cluster.Endpoint)
	conditions.MarkTrue(s.scope.ConditionSetter(), clusterv1.ReadyCondition)
	conditions.MarkTrue(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition)
	conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneCreatingCondition, infrav1exp.GKEControlPlaneCreatedReason, clusterv1.ConditionSeverityInfo, "")
	s.scope.GCPManagedControlPlane.Status.Ready = true
	s.scope.GCPManagedControlPlane.Status.Initialized = true

	log.Info("Cluster reconciled")

	return ctrl.Result{}, nil
}

// Delete delete GKE cluster.
func (s *Service) Delete(ctx context.Context) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("service", "container.clusters")
	log.Info("Deleting cluster resources")

	cluster, err := s.describeCluster(ctx, &log)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Cluster already deleted")
		conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneDeletingCondition, infrav1exp.GKEControlPlaneDeletedReason, clusterv1.ConditionSeverityInfo, "")
		return ctrl.Result{}, nil
	}

	switch cluster.Status {
	case containerpb.Cluster_PROVISIONING:
		log.Info("Cluster provisioning in progress")
		return ctrl.Result{}, nil
	case containerpb.Cluster_RECONCILING:
		log.Info("Cluster reconciling in progress")
		return ctrl.Result{}, nil
	case containerpb.Cluster_STOPPING:
		log.Info("Cluster stopping in progress")
		conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneDeletingReason, clusterv1.ConditionSeverityInfo, "")
		conditions.MarkTrue(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneDeletingCondition)
		return ctrl.Result{}, nil
	default:
		break
	}

	if err = s.deleteCluster(ctx, &log); err != nil {
		conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneDeletingCondition, infrav1exp.GKEControlPlaneReconciliationFailedReason, clusterv1.ConditionSeverityError, err.Error())
		return ctrl.Result{}, err
	}
	log.Info("Cluster deleting in progress")
	s.scope.GCPManagedControlPlane.Status.Initialized = false
	s.scope.GCPManagedControlPlane.Status.Ready = false
	conditions.MarkFalse(s.scope.ConditionSetter(), clusterv1.ReadyCondition, infrav1exp.GKEControlPlaneDeletingReason, clusterv1.ConditionSeverityInfo, "")
	conditions.MarkFalse(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneReadyCondition, infrav1exp.GKEControlPlaneDeletingReason, clusterv1.ConditionSeverityInfo, "")
	conditions.MarkTrue(s.scope.ConditionSetter(), infrav1exp.GKEControlPlaneDeletingCondition)

	return ctrl.Result{}, nil
}

func (s *Service) describeCluster(ctx context.Context, log *logr.Logger) (*containerpb.Cluster, error) {
	getClusterRequest := &containerpb.GetClusterRequest{
		Name: s.scope.ClusterFullName(),
	}
	cluster, err := s.scope.ManagedControlPlaneClient().GetCluster(ctx, getClusterRequest)
	if err != nil {
		var e *apierror.APIError
		if ok := errors.As(err, &e); ok {
			if e.GRPCStatus().Code() == codes.NotFound {
				return nil, nil
			}
		}
		log.Error(err, "Error getting GKE cluster", "name", s.scope.ClusterName())
		return nil, err
	}

	return cluster, nil
}

func (s *Service) createCluster(ctx context.Context, log *logr.Logger) error {
	nodePools, machinePools, _ := s.scope.GetAllNodePools(ctx)

	log.V(2).Info("Running pre-flight checks on machine pools before cluster creation")
	if err := shared.ManagedMachinePoolsPreflightCheck(nodePools, machinePools, s.scope.Region()); err != nil {
		return fmt.Errorf("preflight checks on machine pools before cluster create: %w", err)
	}

	isRegional := shared.IsRegional(s.scope.Region())

	cluster := &containerpb.Cluster{
		Name:    s.scope.ClusterName(),
		Network: *s.scope.GCPManagedCluster.Spec.Network.Name,
		Autopilot: &containerpb.Autopilot{
			Enabled: s.scope.GCPManagedControlPlane.Spec.EnableAutopilot,
		},
		ReleaseChannel: &containerpb.ReleaseChannel{
			Channel: convertToSdkReleaseChannel(s.scope.GCPManagedControlPlane.Spec.ReleaseChannel),
		},
		WorkloadIdentityConfig: s.createWorkloadIdentityConfig(),
		NetworkConfig:          s.createNetworkConfig(),
		AddonsConfig:           s.createAddonsConfig(),
		ResourceLabels:         s.scope.GCPManagedCluster.Labels,
		MasterAuthorizedNetworksConfig: convertToSdkMasterAuthorizedNetworksConfig(s.scope.GCPManagedControlPlane.Spec.MasterAuthorizedNetworksConfig),
	}

	if s.scope.GCPManagedControlPlane.Spec.ControlPlaneVersion != nil {
		cluster.InitialClusterVersion = *s.scope.GCPManagedControlPlane.Spec.ControlPlaneVersion
	}

	if !s.scope.IsAutopilotCluster() {
		cluster.NodePools = scope.ConvertToSdkNodePools(nodePools, machinePools, isRegional)
	}

	createClusterRequest := &containerpb.CreateClusterRequest{
		Cluster: cluster,
		Parent:  s.scope.ClusterLocation(),
	}

	log.V(2).Info("Creating GKE cluster")
	_, err := s.scope.ManagedControlPlaneClient().CreateCluster(ctx, createClusterRequest)
	if err != nil {
		log.Error(err, "Error creating GKE cluster", "name", s.scope.ClusterName())
		return err
	}

	return nil
}

func (s *Service) updateCluster(ctx context.Context, updateClusterRequest *containerpb.UpdateClusterRequest, log *logr.Logger) error {
	_, err := s.scope.ManagedControlPlaneClient().UpdateCluster(ctx, updateClusterRequest)
	if err != nil {
		log.Error(err, "Error updating GKE cluster", "name", s.scope.ClusterName())
		return err
	}

	return nil
}

func (s *Service) deleteCluster(ctx context.Context, log *logr.Logger) error {
	deleteClusterRequest := &containerpb.DeleteClusterRequest{
		Name: s.scope.ClusterFullName(),
	}
	_, err := s.scope.ManagedControlPlaneClient().DeleteCluster(ctx, deleteClusterRequest)
	if err != nil {
		log.Error(err, "Error deleting GKE cluster", "name", s.scope.ClusterName())
		return err
	}

	return nil
}

func (s *Service) createAddonsConfig() *containerpb.AddonsConfig {
	if s.scope.GCPManagedCluster.Spec.AddonsConfig == nil {
		return nil
	}

	config := new(containerpb.AddonsConfig)

	if s.scope.GCPManagedCluster.Spec.AddonsConfig.GcpFilestoreCsiDriverEnabled != nil {
		config.GcpFilestoreCsiDriverConfig = &containerpb.GcpFilestoreCsiDriverConfig{
			Enabled: *s.scope.GCPManagedCluster.Spec.AddonsConfig.GcpFilestoreCsiDriverEnabled,
		}
	}

	if s.scope.GCPManagedCluster.Spec.AddonsConfig.NetworkPolicyEnabled != nil {
		config.NetworkPolicyConfig = &containerpb.NetworkPolicyConfig{
			Disabled: !*s.scope.GCPManagedCluster.Spec.AddonsConfig.NetworkPolicyEnabled,
		}
	}

	if s.scope.GCPManagedCluster.Spec.AddonsConfig.HorizontalPodAutoscalingEnabled != nil {
		config.HorizontalPodAutoscaling = &containerpb.HorizontalPodAutoscaling{
			Disabled: !*s.scope.GCPManagedCluster.Spec.AddonsConfig.HorizontalPodAutoscalingEnabled,
		}
	}

	if s.scope.GCPManagedCluster.Spec.AddonsConfig.HTTPLoadBalancingEnabled != nil {
		config.HttpLoadBalancing = &containerpb.HttpLoadBalancing{
			Disabled: !*s.scope.GCPManagedCluster.Spec.AddonsConfig.HTTPLoadBalancingEnabled,
		}
	}

	return config
}

func (s *Service) createNetworkConfig() *containerpb.NetworkConfig {
	if s.scope.GCPManagedCluster.Spec.Network.DatapathProvider == nil {
		return nil
	}

	return &containerpb.NetworkConfig{
		DatapathProvider: convertToSdkDatapathProvider(s.scope.GCPManagedCluster.Spec.Network.DatapathProvider),
	}
}

func convertToSdkDatapathProvider(datapath *v1beta1.DatapathProvider) containerpb.DatapathProvider {
	if datapath == nil {
		return containerpb.DatapathProvider_DATAPATH_PROVIDER_UNSPECIFIED
	}

	switch *datapath {
	case v1beta1.DatapathProviderUnspecified:
		return containerpb.DatapathProvider_DATAPATH_PROVIDER_UNSPECIFIED
	case v1beta1.DatapathProviderLegacyDatapath:
		return containerpb.DatapathProvider_LEGACY_DATAPATH
	case v1beta1.DatapathProviderAdvancedDatapath:
		return containerpb.DatapathProvider_ADVANCED_DATAPATH
	}

	return containerpb.DatapathProvider_DATAPATH_PROVIDER_UNSPECIFIED
}

func (s *Service) createWorkloadIdentityConfig() *containerpb.WorkloadIdentityConfig {
	// Autopilot clusters enable Workload Identity by default.
	if s.scope.IsAutopilotCluster() || !s.scope.GCPManagedControlPlane.Spec.EnableWorkloadIdentity {
		return nil
	}

	return &containerpb.WorkloadIdentityConfig{
		WorkloadPool: fmt.Sprintf("%s.svc.id.goog", s.scope.GCPManagedControlPlane.Spec.Project),
	}
}

func convertToSdkReleaseChannel(channel *infrav1exp.ReleaseChannel) containerpb.ReleaseChannel_Channel {
	if channel == nil {
		return containerpb.ReleaseChannel_UNSPECIFIED
	}
	switch *channel {
	case infrav1exp.Rapid:
		return containerpb.ReleaseChannel_RAPID
	case infrav1exp.Regular:
		return containerpb.ReleaseChannel_REGULAR
	case infrav1exp.Stable:
		return containerpb.ReleaseChannel_STABLE
	default:
		return containerpb.ReleaseChannel_UNSPECIFIED
	}
}

// convertToSdkMasterAuthorizedNetworksConfig converts the MasterAuthorizedNetworksConfig defined in CRs to the SDK version.
func convertToSdkMasterAuthorizedNetworksConfig(config *infrav1exp.MasterAuthorizedNetworksConfig) *containerpb.MasterAuthorizedNetworksConfig {
	// if config is nil, it means that the user wants to disable the feature.
	if config == nil {
		return &containerpb.MasterAuthorizedNetworksConfig{
			Enabled:                     false,
			CidrBlocks:                  []*containerpb.MasterAuthorizedNetworksConfig_CidrBlock{},
			GcpPublicCidrsAccessEnabled: new(bool),
		}
	}

	// Convert the CidrBlocks slice.
	cidrBlocks := make([]*containerpb.MasterAuthorizedNetworksConfig_CidrBlock, len(config.CidrBlocks))
	for i, cidrBlock := range config.CidrBlocks {
		cidrBlocks[i] = &containerpb.MasterAuthorizedNetworksConfig_CidrBlock{
			CidrBlock:   cidrBlock.CidrBlock,
			DisplayName: cidrBlock.DisplayName,
		}
	}

	return &containerpb.MasterAuthorizedNetworksConfig{
		Enabled:                     true,
		CidrBlocks:                  cidrBlocks,
		GcpPublicCidrsAccessEnabled: config.GcpPublicCidrsAccessEnabled,
	}
}

func (s *Service) checkDiffAndPrepareUpdate(existingCluster *containerpb.Cluster, log *logr.Logger) (bool, *containerpb.UpdateClusterRequest) {
	log.V(4).Info("Checking diff and preparing update.")

	needUpdate := false
	clusterUpdate := containerpb.ClusterUpdate{}
	// Release channel
	desiredReleaseChannel := convertToSdkReleaseChannel(s.scope.GCPManagedControlPlane.Spec.ReleaseChannel)
	if desiredReleaseChannel != existingCluster.ReleaseChannel.Channel {
		log.V(2).Info("Release channel update required", "current", existingCluster.ReleaseChannel.Channel, "desired", desiredReleaseChannel)
		needUpdate = true
		clusterUpdate.DesiredReleaseChannel = &containerpb.ReleaseChannel{
			Channel: desiredReleaseChannel,
		}
	}

	// Master version
	if s.hasDesiredVersion(s.scope.GCPManagedControlPlane.Spec.ControlPlaneVersion, existingCluster.CurrentMasterVersion) {
		needUpdate = true
		clusterUpdate.DesiredMasterVersion = *s.scope.GCPManagedControlPlane.Spec.ControlPlaneVersion
	}

	// DesiredMasterAuthorizedNetworksConfig
	// When desiredMasterAuthorizedNetworksConfig is nil, it means that the user wants to disable the feature.
	desiredMasterAuthorizedNetworksConfig := convertToSdkMasterAuthorizedNetworksConfig(s.scope.GCPManagedControlPlane.Spec.MasterAuthorizedNetworksConfig)
	if !compareMasterAuthorizedNetworksConfig(desiredMasterAuthorizedNetworksConfig, existingCluster.MasterAuthorizedNetworksConfig) {
		needUpdate = true
		clusterUpdate.DesiredMasterAuthorizedNetworksConfig = desiredMasterAuthorizedNetworksConfig
		log.V(2).Info("Master authorized networks config update required", "current", existingCluster.MasterAuthorizedNetworksConfig, "desired", desiredMasterAuthorizedNetworksConfig)
	}
	log.V(4).Info("Master authorized networks config update check", "current", existingCluster.MasterAuthorizedNetworksConfig)
	if desiredMasterAuthorizedNetworksConfig != nil {
		log.V(4).Info("Master authorized networks config update check", "desired", desiredMasterAuthorizedNetworksConfig)
	}

	log.V(4).Info("Update cluster request. ", "needUpdate", needUpdate, "updateClusterRequest", &updateClusterRequest)
	return needUpdate, &containerpb.UpdateClusterRequest{
		Name:   s.scope.ClusterFullName(),
		Update: &clusterUpdate,
	}
}

func (s *Service) hasDesiredVersion(controlPlaneVersion *string, clusterVersion string) bool {
	if controlPlaneVersion == nil {
		return true
	}

	// Allow partial version matching i.e. '1.24' and '1.24.14' should be a desired version match
	// for cluster version '1.24.14-gke.2700'
	if strings.HasPrefix(clusterVersion, *controlPlaneVersion) {
		return true
	}

	return *controlPlaneVersion == clusterVersion
}

// compare if two MasterAuthorizedNetworksConfig are equal.
func compareMasterAuthorizedNetworksConfig(a, b *containerpb.MasterAuthorizedNetworksConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	if a.Enabled != b.Enabled {
		return false
	}
	if (a.GcpPublicCidrsAccessEnabled == nil && b.GcpPublicCidrsAccessEnabled != nil) || (a.GcpPublicCidrsAccessEnabled != nil && b.GcpPublicCidrsAccessEnabled == nil) {
		return false
	}
	if a.GcpPublicCidrsAccessEnabled != nil && b.GcpPublicCidrsAccessEnabled != nil && *a.GcpPublicCidrsAccessEnabled != *b.GcpPublicCidrsAccessEnabled {
		return false
	}
	// if one cidrBlocks is nil, but the other is empty, they are equal.
	if (a.CidrBlocks == nil && b.CidrBlocks != nil && len(b.CidrBlocks) == 0) || (b.CidrBlocks == nil && a.CidrBlocks != nil && len(a.CidrBlocks) == 0) {
		return true
	}
	if !cmp.Equal(a.CidrBlocks, b.CidrBlocks) {
		return false
	}
	return true
}
