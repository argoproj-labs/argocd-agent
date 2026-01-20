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

package cmdutil

import (
	"crypto/tls"
	"fmt"
	"strings"
)

// PrintAvailableCipherSuites prints all available TLS cipher suites and their
// supported TLS versions to stdout.
func PrintAvailableCipherSuites() {
	fmt.Println("Available TLS cipher suites:")
	fmt.Println()
	for _, cs := range tls.CipherSuites() {
		versions := make([]string, 0, len(cs.SupportedVersions))
		for _, v := range cs.SupportedVersions {
			switch v {
			case tls.VersionTLS10:
				versions = append(versions, "TLS 1.0")
			case tls.VersionTLS11:
				versions = append(versions, "TLS 1.1")
			case tls.VersionTLS12:
				versions = append(versions, "TLS 1.2")
			case tls.VersionTLS13:
				versions = append(versions, "TLS 1.3")
			}
		}
		fmt.Printf("  %s\n", cs.Name)
		fmt.Printf("    Supported versions: %s\n", strings.Join(versions, ", "))
	}
}
