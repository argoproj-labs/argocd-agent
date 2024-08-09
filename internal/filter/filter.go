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

package filter

import (
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
)

// ChangeFilterFunc is a function that compares old and new and returns false if the
// agent should ignore this change.
type ChangeFilterFunc func(old *v1alpha1.Application, new *v1alpha1.Application) bool

// AdmitFilterFunc is a function that returns true if the agent should handle a
// given Application
type AdmitFilterFunc func(app *v1alpha1.Application) bool

// Chain is a chain of filters to decide whether a change should be
// ignored by the agent.
type Chain struct {
	changeFilters []ChangeFilterFunc
	admitFilters  []AdmitFilterFunc
}

// Append appends a filter function to the chain
func (fc *Chain) AppendChangeFilter(f ChangeFilterFunc) {
	fc.changeFilters = append(fc.changeFilters, f)
}

// AppendAdmitFilter appends an admit filter function to the chain
func (fc *Chain) AppendAdmitFilter(f AdmitFilterFunc) {
	fc.admitFilters = append(fc.admitFilters, f)
}

// ProcessChange runs all filters in the FilterChain and returns false if the change
// should be ignored by the agent
func (fc *Chain) ProcessChange(old, new *v1alpha1.Application) bool {
	for _, f := range fc.changeFilters {
		if !f(old, new) {
			log().WithField("application", new.QualifiedName()).Tracef("Process filter negative")
			return false
		}
	}
	return true
}

// Admit runs all admit filters in the FilterChain and returns true if the app
// should be admitted
func (fc *Chain) Admit(app *v1alpha1.Application) bool {
	for _, f := range fc.admitFilters {
		if !f(app) {
			log().WithField("application", app.QualifiedName()).Tracef("Admit filter negative")
			return false
		}
	}
	return true
}

// NewFilterChain returns an instance of an empty FilterChain
func NewFilterChain() *Chain {
	fc := &Chain{}
	return fc
}

func log() *logrus.Entry {
	return logrus.WithField("module", "filter")
}
