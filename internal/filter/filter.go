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

import "k8s.io/apimachinery/pkg/runtime"

type FilterType runtime.Object

// ChangeFilterFunc is a function that compares old and new and returns false if the
// agent should ignore this change.
type ChangeFilterFunc[T FilterType] func(old T, new T) bool

// AdmitFilterFunc is a function that returns true if the agent should handle a
// given Application
type AdmitFilterFunc[T FilterType] func(res T) bool

// Chain is a chain of filters to decide whether a change should be
// ignored by the agent.
type Chain[T FilterType] struct {
	changeFilters []ChangeFilterFunc[T]
	admitFilters  []AdmitFilterFunc[T]
}

// Append appends a filter function to the chain
func (fc *Chain[T]) AppendChangeFilter(f ChangeFilterFunc[T]) {
	fc.changeFilters = append(fc.changeFilters, f)
}

// AppendAdmitFilter appends an admit filter function to the chain
func (fc *Chain[T]) AppendAdmitFilter(f AdmitFilterFunc[T]) {
	fc.admitFilters = append(fc.admitFilters, f)
}

// ProcessChange runs all filters in the FilterChain and returns false if the change
// should be ignored by the agent
func (fc *Chain[T]) ProcessChange(old, new T) bool {
	for _, f := range fc.changeFilters {
		if !f(old, new) {
			return false
		}
	}
	return true
}

// Admit runs all admit filters in the FilterChain and returns true if the app
// should be admitted
func (fc *Chain[T]) Admit(app T) bool {
	for _, f := range fc.admitFilters {
		if !f(app) {
			return false
		}
	}
	return true
}

// NewFilterChain returns an instance of an empty FilterChain
func NewFilterChain[T FilterType]() *Chain[T] {
	fc := &Chain[T]{}
	return fc
}
