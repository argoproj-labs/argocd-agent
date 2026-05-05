#!/bin/bash
# Copyright 2026 The argocd-agent Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Centralized namespace definitions for E2E testing
# Each component uses a different namespace. 
export ARGOCD_PRINCIPAL_NAMESPACE="argocd-principal"
export ARGOCD_MANAGED_NAMESPACE="argocd-managed"
export ARGOCD_AUTONOMOUS_NAMESPACE="argocd-autonomous"
