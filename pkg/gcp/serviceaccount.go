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

package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ServiceAccount represents a GCP service account.
type ServiceAccount struct {
	// Raw is the raw representation of the GCP service account.
	Raw []byte
	// Token is ID token for federated authentication
	Token []byte
	// TokenFilePath is the path to the file where the token is stored.
	TokenFilePath string
	// ProjectID is the project id the service account is associated to.
	ProjectID string
	// Email is the email associated with the service account.
	Email string
	// Type is the type of credentials.
	Type string
}

// GetServiceAccountFromSecretReference retrieves the ServiceAccount from the secret with the given secret reference.
func GetServiceAccountFromSecretReference(ctx context.Context, c client.Client, secretRef corev1.SecretReference) (*ServiceAccount, error) {
	secret, err := extensionscontroller.GetSecretByReference(ctx, c, &secretRef)
	if err != nil {
		return nil, err
	}

	return GetServiceAccountFromSecret(secret)
}

// GetServiceAccountFromSecret retrieves the ServiceAccount from the secret.
func GetServiceAccountFromSecret(secret *corev1.Secret) (*ServiceAccount, error) {
	data, ok := secret.Data[ServiceAccountJSONField]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s doesn't have a service account json (expected field: %q)", secret.Namespace, secret.Name, ServiceAccountJSONField)
	}

	token, ok := secret.Data["token"]
	if !ok {
		log.Log.Info("secret %s/%s have no token", secret.Namespace, secret.Name)
	}

	return GetServiceAccountFromJSON(data, token)
}

// GetServiceAccountFromJSON returns a ServiceAccount from the given
func GetServiceAccountFromJSON(data, token []byte) (*ServiceAccount, error) {
	type credentialSource struct {
		File string `json:"file"`
	}
	var serviceAccount struct {
		ProjectID         string           `json:"project_id"`
		Email             string           `json:"client_email"`
		Type              string           `json:"type"`
		ImpersonationURL  string           `json:"service_account_impersonation_url"`
		CredentialsSource credentialSource `json:"credential_source"`
	}

	if err := json.Unmarshal(data, &serviceAccount); err != nil {
		return nil, err
	}
	var (
		projectID = serviceAccount.ProjectID
		email     = serviceAccount.Email
	)
	if projectID == "" {
		u, err := url.Parse(serviceAccount.ImpersonationURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse impersonation URL, %+w", err)
		}

		paths := strings.Split(u.Path, "/")
		projectURL := paths[len(paths)-1]
		projectURLParts := strings.FieldsFunc(
			projectURL,
			func(c rune) bool { return c == rune('@') || c == rune(':') },
		)

		projectHost := projectURLParts[1]
		projectID = strings.Split(projectHost, ".")[0]

		if projectID == "" {
			return nil, fmt.Errorf("no service account specified")
		}
		if email == "" {
			email = projectURLParts[0] + "@" + projectURLParts[1]
		}
	}

	sa := &ServiceAccount{
		Raw:       data,
		ProjectID: projectID,
		Email:     email,
		Type:      serviceAccount.Type,
		Token:     token,
	}

	if serviceAccount.Type == "external_account" {
		sa.TokenFilePath = serviceAccount.CredentialsSource.File
	}

	return sa, nil
}

// readServiceAccountSecret reads the ServiceAccount from the given secret.
func readServiceAccountSecret(secret *corev1.Secret) ([]byte, error) {
	data, ok := secret.Data[ServiceAccountJSONField]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s doesn't have a service account json (expected field: %q)", secret.Namespace, secret.Name, ServiceAccountJSONField)
	}

	return data, nil
}

// ExtractServiceAccountProjectID extracts the project id from the given service account JSON.
func ExtractServiceAccountProjectID(serviceAccountJSON []byte) (string, error) {
	sa, err := GetServiceAccountFromJSON(serviceAccountJSON, nil)
	if err != nil {
		return "", err
	}
	return sa.ProjectID, nil
}
