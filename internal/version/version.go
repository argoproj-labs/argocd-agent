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
