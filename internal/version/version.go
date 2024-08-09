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

	"gopkg.in/yaml.v3"
)

const version = "99.9.9-unreleased"
const gitRevision = "unknown"
const gitStatus = "unknown"

type versionInformation struct {
	Name        string `json:"name"`
	Component   string `json:"component"`
	Version     string `json:"version"`
	GitRevision string `json:"gitRevision" yaml:"gitRevision"`
	GitStatus   string `json:"gitStatus" yaml:"gitStatus"`
	GoVersion   string `json:"goVersion" yaml:"goVersion"`
}

type Version struct {
	v versionInformation
}

func New(name, component string) *Version {
	v := versionInformation{
		Name:        name,
		Component:   component,
		Version:     version,
		GitRevision: gitRevision,
		GitStatus:   gitStatus,
		GoVersion:   runtime.Version(),
	}
	return &Version{v: v}
}

func (v *Version) QualifiedVersion() string {
	return v.v.Name + "-" + v.v.Version
}

func (v *Version) Version() string {
	return version
}

func (v *Version) Name() string {
	return v.v.Name
}

func (v *Version) Component() string {
	return v.v.Component
}

func (v *Version) GitRevision() string {
	return v.v.GitRevision
}

func (v *Version) GitStatus() string {
	return v.v.GitStatus
}

func (v *Version) YAML() string {
	b, err := yaml.Marshal(v.v)
	if err != nil {
		return "error: " + err.Error()
	}
	return string(b)
}

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
