# Cluster API Provider Vmware Desktop (Workstation, Fusion)

## Description
The Cluster API Provider Vmware Desktop (CAVD) provides a way to declaratively create and manage cluster on Vmware Desktop with `vmrest`, in a Kubernetes-native way. It extends the Kubernetes API with Custom Resource Definitions (CRDs) allowing you to interact with clusters in the same fashion you interact with workload.

## Getting Started

### Prerequisites
- Understanding Cluster API [Quick Start](https://cluster-api.sigs.k8s.io/user/quick-start)
- Vmware Desktop Hypervisor (Workstation or Fusion, tested only with Fusion)
- Prepared Virtual Machine by [image-builder](https://image-builder.sigs.k8s.io/capi/providers/vsphere) with `build-node-ova-local-` or [factory-talos](https://factory.talos.dev/) `Cloud/Vmware` (only amd64 supports by Talos)
- Running `vmrest` [Doc] (https://techdocs.broadcom.com/us/en/vmware-cis/desktop-hypervisors/fusion-pro/13-0/using-vmware-fusion/guide-and-help-using-the-vmware-fusion-rest-api/guide-and-help-use-the-fusion-api-service.html)

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

