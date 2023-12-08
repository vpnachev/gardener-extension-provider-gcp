// Copyright (c) 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package terraformer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type mutator struct {
	logger logr.Logger
	client client.Client
}

const (
	containerName = "terraform"
	secretName    = "cloudprovider"
	volumeName    = "projected-infra-token"
	secretKey     = "token"
)

// New returns a new Infrastructure mutator that uses mutateFunc to perform the mutation.
func New(mgr manager.Manager, logger logr.Logger) extensionswebhook.Mutator {
	return &mutator{
		client: mgr.GetClient(),
		logger: logger,
	}
}

// Mutate mutates the given object on creation and adds the annotation `gcp.provider.extensions.gardener.cloud/use-flow=true`
// if the seed has the label `gcp.provider.extensions.gardener.cloud/use-flow` == `new`.
func (m *mutator) Mutate(ctx context.Context, new, old client.Object) error {
	if old != nil || new.GetDeletionTimestamp() != nil {
		return nil
	}

	terraformerPod, ok := new.(*corev1.Pod)
	if !ok {
		return errors.New("object is not of type corev1.Pod")
	}

	idx, found := containerIndex(terraformerPod, containerName)
	if !found {
		return fmt.Errorf("found no container with name %q", containerName)
	}

	fileName, dirPath, err := getTokenPath(ctx, m.client, terraformerPod.GetNamespace(), secretName)
	if err != nil {
		return err
	}

	m.logger.Info("Mounting secret", "dirPath", dirPath, "fileName", fileName)

	if err := ensureVolume(terraformerPod, fileName); err != nil {
		return err
	}

	return ensureVolumeMount(terraformerPod, idx, dirPath)

}

func containerIndex(p *corev1.Pod, constainerName string) (int, bool) {
	for idx, c := range p.Spec.Containers {
		if c.Name == constainerName {
			return idx, true
		}
	}
	return -1, false
}

func ensureVolume(p *corev1.Pod, path string) error {
	p.Spec.Volumes = append(
		p.Spec.Volumes,
		corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Items: []corev1.KeyToPath{
						{
							Key:  secretKey,
							Path: path,
						},
					},
				},
			},
		},
	)
	return nil
}

func ensureVolumeMount(p *corev1.Pod, idx int, mountPath string) error {
	p.Spec.Containers[idx].VolumeMounts = append(
		p.Spec.Containers[idx].VolumeMounts,
		corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		},
	)
	return nil
}

func getTokenPath(ctx context.Context, c client.Reader, namespace, secretName string) (string, string, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      secretName,
		},
	}

	if err := c.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		return "", "", err
	}

	key := "serviceaccount.json"
	saRaw, ok := secret.Data[key]
	if !ok {
		return "", "", fmt.Errorf("secret %q/%q has no key %q", namespace, secretName, key)
	}

	type credentialsSource struct {
		File string `json:"file"`
	}
	type serviceAccount struct {
		Type   string            `json:"type"`
		Source credentialsSource `json:"credential_source"`
	}

	sa := &serviceAccount{}
	if err := json.Unmarshal(saRaw, sa); err != nil {
		return "", "", err
	}

	if sa.Type != "external_account" {
		return "", "", fmt.Errorf("service account is not of type \"external_account\"")
	}

	if sa.Source.File == "" {
		return "", "", fmt.Errorf("service account has empty path")
	}

	tree := strings.Split(sa.Source.File, "/")
	fileName := tree[len(tree)-1]
	dirPath := strings.Join(tree[:len(tree)-1], "/")
	return fileName, dirPath, nil
}
