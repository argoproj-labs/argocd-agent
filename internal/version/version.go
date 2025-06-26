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

package version

import (
	"encoding/json"
	"runtime"
	"runtime/debug"
	"time"

	"gopkg.in/yaml.v3"
)

var version = "99.9.9-unreleased"
var gitRevision = "unknown"
var gitStatus = "unknown"
var buildDate = time.Now().Format(time.RFC3339)

// versionInformation represents the version information of the argocd-agent.
type versionInformation struct {
	// Name is the name of the argocd-agent.
	Name string `json:"name"`
	// Version is the version of the argocd-agent.
	Version string `json:"version"`
	// GitRevision is the git revision of the argocd-agent.
	GitRevision string `json:"gitRevision" yaml:"gitRevision"`
	// GitStatus is the git status of the argocd-agent.
	GitStatus string `json:"gitStatus" yaml:"gitStatus"`
	// GoVersion is the go version of the argocd-agent.
	GoVersion string `json:"goVersion" yaml:"goVersion"`
	// BuildDate is the build date of the argocd-agent.
	BuildDate string `json:"buildDate" yaml:"buildDate"`
	// ArgoCDVersion is the version of the argocd-agent's dependency on argocd.
	ArgoCDVersion string `json:"argocdVersion" yaml:"argocdVersion"`
}

// Version represents the version of the argocd-agent.
type Version struct {
	v versionInformation
}

// New returns a new Version object.
func New(name string) *Version {
	v := versionInformation{
		Name:          name,
		Version:       version,
		GitRevision:   gitRevision,
		GitStatus:     gitStatus,
		GoVersion:     runtime.Version(),
		BuildDate:     buildDate,
		ArgoCDVersion: getArgoCDVersion(),
	}
	return &Version{v: v}
}

// QualifiedVersion returns the qualified version of the argocd-agent.
func (v *Version) QualifiedVersion() string {
	return v.v.Name + "-" + v.v.Version
}

// Version returns the version of the argocd-agent.
func (v *Version) Version() string {
	return version
}

// Name returns the name of the argocd-agent.
func (v *Version) Name() string {
	return v.v.Name
}

// GitRevision returns the git revision of the argocd-agent.
func (v *Version) GitRevision() string {
	return v.v.GitRevision
}

// GitStatus returns the git status of the argocd-agent.

func (v *Version) GitStatus() string {
	return v.v.GitStatus
}

// getArgoCDVersion returns the version of the argocd-agent's dependency on argocd.
func getArgoCDVersion() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range buildInfo.Deps {
		if dep.Path == "github.com/argoproj/argo-cd/v2" {
			return dep.Version
		}
	}
	return "unknown"
}

// YAML returns the version information of the argocd-agent in YAML format.
func (v *Version) YAML() string {
	b, err := yaml.Marshal(v.v)
	if err != nil {
		return "error: " + err.Error()
	}
	return string(b)
}

// JSON returns the version information of the argocd-agent in JSON format.
func (v *Version) JSON(indent bool) string {
	var b []byte
	var err error
	if indent {
		b, err = json.MarshalIndent(v.v, "", "  ")
	} else {
		b, err = json.Marshal(v.v)

	}
	if err != nil {
		return "{}"
	}
	return string(b)
}
