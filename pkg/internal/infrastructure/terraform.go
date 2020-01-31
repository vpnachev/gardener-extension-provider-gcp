// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package infrastructure

import (
	"path/filepath"

	api "github.com/gardener/gardener-extension-provider-gcp/pkg/apis/gcp"
	apiv1alpha1 "github.com/gardener/gardener-extension-provider-gcp/pkg/apis/gcp/v1alpha1"
	"github.com/gardener/gardener-extension-provider-gcp/pkg/internal"
	extensionscontroller "github.com/gardener/gardener-extensions/pkg/controller"
	"github.com/gardener/gardener-extensions/pkg/terraformer"

	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/chartrenderer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DefaultVPCName is the default VPC terraform name.
	DefaultVPCName = "${google_compute_network.network.name}"

	// TerraformerPurpose is the terraformer infrastructure purpose.
	TerraformerPurpose = "infra"

	// TerraformerOutputKeyVPCName is the name of the vpc_name terraform output variable.
	TerraformerOutputKeyVPCName = "vpc_name"
	// TerraformerOutputKeyServiceAccountEmail is the name of the service_account_email terraform output variable.
	TerraformerOutputKeyServiceAccountEmail = "service_account_email"
	// TerraformerOutputKeySubnetNodes is the name of the subnet_nodes terraform output variable.
	TerraformerOutputKeySubnetNodes = "subnet_nodes"
	// TerraformerOutputKeySubnetInternal is the name of the subnet_internal terraform output variable.
	TerraformerOutputKeySubnetInternal = "subnet_internal"
	// TerraformOutputKeyCloudNAT is the name of the cloud_nat terraform output variable.
	TerraformOutputKeyCloudNAT = "cloud_nat"
	// TerraformOutputKeyCloudRouter is the name of the cloud_router terraform output variable.
	TerraformOutputKeyCloudRouter = "cloud_router"
)

var (
	// StatusTypeMeta is the TypeMeta of the GCP InfrastructureStatus
	StatusTypeMeta = metav1.TypeMeta{
		APIVersion: apiv1alpha1.SchemeGroupVersion.String(),
		Kind:       "InfrastructureStatus",
	}
)

// ComputeTerraformerChartValues computes the values for the GCP Terraformer chart.
func ComputeTerraformerChartValues(
	infra *extensionsv1alpha1.Infrastructure,
	account *internal.ServiceAccount,
	config *api.InfrastructureConfig,
	cluster *extensionscontroller.Cluster,
) map[string]interface{} {
	var (
		vpcName           = DefaultVPCName
		createVPC         = true
		createCloudRouter = true
		cloudRouterName   string
		minPortsPerVM     = int32(2048)
	)

	if config.Networks.VPC != nil {
		vpcName = config.Networks.VPC.Name
		createVPC = false
		createCloudRouter = false

		if config.Networks.VPC.CloudRouter != nil && len(config.Networks.VPC.CloudRouter.Name) > 0 {
			cloudRouterName = config.Networks.VPC.CloudRouter.Name
		}
	}

	if config.Networks.CloudNAT != nil {
		if config.Networks.CloudNAT.MinPortsPerVM != nil {
			minPortsPerVM = *config.Networks.CloudNAT.MinPortsPerVM
		}
	}

	vpc := map[string]interface{}{
		"name": vpcName,
	}

	if len(cloudRouterName) > 0 {
		vpc["cloudRouter"] = map[string]interface{}{
			"name": cloudRouterName,
		}
	}

	workersCIDR := config.Networks.Workers
	// Backwards compatibility - remove this code in a future version.
	if workersCIDR == "" {
		workersCIDR = config.Networks.Worker
	}

	values := map[string]interface{}{
		"google": map[string]interface{}{
			"region":  infra.Spec.Region,
			"project": account.ProjectID,
		},
		"create": map[string]interface{}{
			"vpc":         createVPC,
			"cloudRouter": createCloudRouter,
		},
		"vpc":         vpc,
		"clusterName": infra.Namespace,
		"networks": map[string]interface{}{
			"pods":     extensionscontroller.GetPodNetwork(cluster),
			"services": extensionscontroller.GetServiceNetwork(cluster),
			"workers":  workersCIDR,
			"internal": config.Networks.Internal,
			"cloudNAT": map[string]interface{}{
				"minPortsPerVM": minPortsPerVM,
			},
		},
		"outputKeys": map[string]interface{}{
			"vpcName":             TerraformerOutputKeyVPCName,
			"cloudNAT":            TerraformOutputKeyCloudNAT,
			"cloudRouter":         TerraformOutputKeyCloudRouter,
			"serviceAccountEmail": TerraformerOutputKeyServiceAccountEmail,
			"subnetNodes":         TerraformerOutputKeySubnetNodes,
			"subnetInternal":      TerraformerOutputKeySubnetInternal,
		},
	}

	if config.Networks.FlowLogs != nil {
		fl := make(map[string]interface{})

		if config.Networks.FlowLogs.AggregationInterval != nil {
			fl["aggregationInterval"] = *config.Networks.FlowLogs.AggregationInterval
		}

		if config.Networks.FlowLogs.FlowSampling != nil {
			fl["flowSampling"] = *config.Networks.FlowLogs.FlowSampling
		}

		if config.Networks.FlowLogs.Metadata != nil {
			fl["metadata"] = *config.Networks.FlowLogs.Metadata
		}

		values["networks"].(map[string]interface{})["flowLogs"] = fl
	}

	return values
}

// RenderTerraformerChart renders the gcp-infra chart with the given values.
func RenderTerraformerChart(
	renderer chartrenderer.Interface,
	infra *extensionsv1alpha1.Infrastructure,
	account *internal.ServiceAccount,
	config *api.InfrastructureConfig,
	cluster *extensionscontroller.Cluster,
) (*TerraformFiles, error) {

	values := ComputeTerraformerChartValues(infra, account, config, cluster)

	release, err := renderer.Render(filepath.Join(internal.InternalChartsPath, "gcp-infra"), "gcp-infra", infra.Namespace, values)
	if err != nil {
		return nil, err
	}

	return &TerraformFiles{
		Main:      release.FileContent("main.tf"),
		Variables: release.FileContent("variables.tf"),
		TFVars:    []byte(release.FileContent("terraform.tfvars")),
	}, nil
}

// TerraformFiles are the files that have been rendered from the infrastructure chart.
type TerraformFiles struct {
	Main      string
	Variables string
	TFVars    []byte
}

// TerraformState is the Terraform state for an infrastructure.
type TerraformState struct {
	// VPCName is the name of the VPC created for an infrastructure.
	VPCName string
	// CloudRouterName is the name of the created / existing cloud router
	CloudRouterName string
	// CloudNATName is the name of the created Cloud NAT
	CloudNATName string
	// ServiceAccountEmail is the service account email for a network.
	ServiceAccountEmail string
	// SubnetNodes is the CIDR of the nodes subnet of an infrastructure.
	SubnetNodes string
	// SubnetInternal is the CIDR of the internal subnet of an infrastructure.
	SubnetInternal *string
}

// ExtractTerraformState extracts the TerraformState from the given Terraformer.
func ExtractTerraformState(tf terraformer.Terraformer, config *api.InfrastructureConfig) (*TerraformState, error) {
	var (
		outputKeys = []string{
			TerraformerOutputKeyVPCName,
			TerraformerOutputKeySubnetNodes,
			TerraformerOutputKeyServiceAccountEmail,
		}

		vpcSpecifiedWithoutCloudRouter = config.Networks.VPC != nil && config.Networks.VPC.CloudRouter == nil
	)

	if !vpcSpecifiedWithoutCloudRouter {
		outputKeys = append(outputKeys, TerraformOutputKeyCloudRouter, TerraformOutputKeyCloudNAT)
	}

	hasInternal := config.Networks.Internal != nil
	if hasInternal {
		outputKeys = append(outputKeys, TerraformerOutputKeySubnetInternal)
	}

	vars, err := tf.GetStateOutputVariables(outputKeys...)
	if err != nil {
		return nil, err
	}

	state := &TerraformState{
		VPCName:             vars[TerraformerOutputKeyVPCName],
		SubnetNodes:         vars[TerraformerOutputKeySubnetNodes],
		ServiceAccountEmail: vars[TerraformerOutputKeyServiceAccountEmail],
	}

	if !vpcSpecifiedWithoutCloudRouter {
		state.CloudRouterName = vars[TerraformOutputKeyCloudRouter]
		state.CloudNATName = vars[TerraformOutputKeyCloudNAT]
	}

	if hasInternal {
		subnetInternal := vars[TerraformerOutputKeySubnetInternal]
		state.SubnetInternal = &subnetInternal
	}
	return state, nil
}

// StatusFromTerraformState computes an InfrastructureStatus from the given
// Terraform variables.
func StatusFromTerraformState(state *TerraformState) *apiv1alpha1.InfrastructureStatus {
	var (
		status = &apiv1alpha1.InfrastructureStatus{
			TypeMeta: StatusTypeMeta,
			Networks: apiv1alpha1.NetworkStatus{
				VPC: apiv1alpha1.VPC{
					Name: state.VPCName,
				},
				Subnets: []apiv1alpha1.Subnet{
					{
						Purpose: apiv1alpha1.PurposeNodes,
						Name:    state.SubnetNodes,
					},
				},
			},
			ServiceAccountEmail: state.ServiceAccountEmail,
		}
	)

	if len(state.CloudRouterName) > 0 {
		status.Networks.VPC.CloudRouter = &apiv1alpha1.CloudRouter{
			Name: state.CloudRouterName,
		}
	}

	if state.SubnetInternal != nil {
		status.Networks.Subnets = append(status.Networks.Subnets, apiv1alpha1.Subnet{
			Purpose: apiv1alpha1.PurposeInternal,
			Name:    *state.SubnetInternal,
		})
	}

	return status
}

// ComputeStatus computes the status based on the Terraformer and the given InfrastructureConfig.
func ComputeStatus(tf terraformer.Terraformer, config *api.InfrastructureConfig) (*apiv1alpha1.InfrastructureStatus, error) {
	state, err := ExtractTerraformState(tf, config)
	if err != nil {
		return nil, err
	}

	return StatusFromTerraformState(state), nil
}