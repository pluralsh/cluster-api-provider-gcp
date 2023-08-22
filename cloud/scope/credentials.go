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

	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	infrav1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ConfigFileEnvVar is the name of the environment variable
	// that contains the path to the credentials file.
	ConfigFileEnvVar = "GOOGLE_APPLICATION_CREDENTIALS"
)

var (
	gcpScopes = []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
	}
)

// Credential is a struct to hold GCP credential data.
type Credential struct {
	token oauth2.TokenSource
}

// GetToken returns the access token of the loaded GCP credentials.
func (c *Credential) GetToken(ctx context.Context) (string, error) {
	token, err := c.token.Token()
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func getCredentials(ctx context.Context, credentialsRef *infrav1.ObjectReference, crClient client.Client) (*Credential, error) {
	var credential *google.Credentials
	var err error

	if credentialsRef != nil {
		credential, err = getCredentialDataFromRef(ctx, credentialsRef, crClient)
	} else {
		credential, err = getCredentialDataUsingADC(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("getting credential data: %w", err)
	}

	token := credential.TokenSource
	if token == nil {
		return nil, errors.New("failed retrieving token from credentials")
	}

	credentials := &Credential{
		token: token,
	}

	return credentials, nil
}

func getCredentialDataFromRef(ctx context.Context, credentialsRef *infrav1.ObjectReference, crClient client.Client) (*google.Credentials, error) {
	secretRefName := types.NamespacedName{
		Name:      credentialsRef.Name,
		Namespace: credentialsRef.Namespace,
	}

	credSecret := &corev1.Secret{}
	if err := crClient.Get(ctx, secretRefName, credSecret); err != nil {
		return nil, fmt.Errorf("getting credentials secret %s\\%s: %w", secretRefName.Namespace, secretRefName.Name, err)
	}

	rawData, ok := credSecret.Data["credentials"]
	if !ok {
		return nil, errors.New("no credentials key in secret")
	}

	creds, err := google.CredentialsFromJSON(ctx, rawData, gcpScopes...)
	if err != nil {
		return nil, fmt.Errorf("getting credentials from json: %w", err)
	}
	if creds == nil {
		return nil, errors.New("failed finding default credentials, cred is nil")
	}

	return creds, nil
}

func getCredentialDataUsingADC(ctx context.Context) (*google.Credentials, error) {
	creds, err := google.FindDefaultCredentials(ctx, gcpScopes...)
	if err != nil {
		return nil, fmt.Errorf("getting credentials from json: %w", err)
	}
	if creds == nil {
		return nil, errors.New("failed finding default credentials, cred is nil")
	}

	return creds, nil
}
