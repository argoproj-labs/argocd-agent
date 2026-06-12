// Copyright 2026 The argocd-agent Authors
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

package main

import (
	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/spf13/cobra"
)

func addKeyGenFlags(cmd *cobra.Command, algorithm *string, size *int) {
	cmd.Flags().StringVar(algorithm, "key-algorithm", tlsutil.DefaultKeyAlgorithm,
		"Private key algorithm (rsa, ecdsa-p256, ecdsa-p384, ecdsa-p521, ed25519)")
	cmd.Flags().IntVar(size, "key-size", tlsutil.DefaultRSABits,
		"RSA key size in bits (2048, 3072, or 4096; ignored for non-RSA algorithms)")
}

func parseKeyGenFlags(algorithm string, size int) tlsutil.KeyGenOptions {
	opts, err := tlsutil.ParseKeyAlgorithm(algorithm, size)
	if err != nil {
		cmdutil.Fatal("%v", err)
	}
	return opts
}
