// Copyright 2024 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kube

import (
	"crypto/tls"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"k8s.io/client-go/rest"
)

// GetClientCert extracts the client certificate and private key from a given
// Kubernetes config. It supports both, loading the data from a file, as well
// as using base64-encoded data.
func GetClientCert(conf *rest.Config) (*tls.Certificate, error) {
	var cert tls.Certificate
	var err error
	if conf.CertFile != "" && conf.KeyFile != "" {
		cert, err = tlsutil.TlsCertFromFile(conf.CertFile, conf.KeyFile, true)
	} else if len(conf.CertData) > 0 && len(conf.KeyData) > 0 {
		cert, err = tls.X509KeyPair(conf.CertData, conf.KeyData)
	} else {
		fmt.Printf("%v\n%v\n", conf.CertData, conf.KeyData)
		return nil, fmt.Errorf("invalid TLS config in configuration")
	}

	return &cert, err
}
