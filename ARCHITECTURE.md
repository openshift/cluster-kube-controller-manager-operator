# Architecture

## Overview

The cluster-kube-controller-manager-operator is a static pod operator that manages the `kube-controller-manager` on OpenShift control plane nodes. It is deployed by the Cluster Version Operator (CVO) and uses the [library-go](https://github.com/openshift/library-go) static pod operator framework.

The operator's primary responsibilities:
- Observe cluster configuration from multiple sources and synthesize kube-controller-manager config
- Manage kube-controller-manager static pods across control plane nodes (install, revision, prune)
- Rotate CSR signing certificates
- Manage service account token signing keys
- Run the cluster-policy-controller as a sidecar in the same static pod
- Report status via the `ClusterOperator/kube-controller-manager` resource

## Data Flow

```text
 config.openshift.io resources          Secrets/ConfigMaps
 (Infrastructure, Network, Node,        (service-account-private-key,
  FeatureGate, Proxy, ...)               service-ca, cloud-config, ...)
              |                                    |
              v                                    v
   +--------------------------------------------------+
   |              Config Observer Controllers           |
   |  (observe external state, produce observedConfig)  |
   +------------------------+--------------------------+
                            | observedConfig (sparse JSON)
                            v
   +--------------------------------------------------+
   |           Target Config Controller                 |
   |  (merge defaults + observedConfig + overrides      |
   |   -> render ConfigMaps/Secrets in target ns)       |
   +------------------------+--------------------------+
                            | ConfigMaps, Secrets
                            v
   +--------------------------------------------------+
   |          Static Pod Controllers (library-go)       |
   |  Installer -> Revision Controller -> Pruner        |
   |  (roll out new revisions to each control plane     |
   |   node as static pod manifests)                    |
   +------------------------+--------------------------+
                            |
                            v
              kube-controller-manager static pods
              (one per control plane node)
```

## Operator Startup

Entry point: `cmd/cluster-kube-controller-manager-operator/main.go` -> `pkg/cmd/operator/cmd.go` -> `pkg/operator/starter.go:RunOperator()`.

Startup sequence:
1. Create clients (Kubernetes, config, operator)
2. Create informers for watched namespaces (see [Namespaces](#namespaces))
3. Initialize feature gates via `FeatureGateAccessor` and wait for observation (1-minute timeout)
4. Create and start all controllers concurrently
5. Block until context cancellation

## Namespaces

| Namespace | Constant | Purpose |
|-----------|----------|---------|
| `openshift-config` | `GlobalUserSpecifiedConfigNamespace` | User-provided configuration (certs, CAs, cloud config) |
| `openshift-config-managed` | `GlobalMachineSpecifiedConfigNamespace` | Platform-managed configuration (generated CAs, signing certs) |
| `openshift-kube-controller-manager-operator` | `OperatorNamespace` | Operator deployment and its resources (CSR signer secrets) |
| `openshift-kube-controller-manager` | `TargetNamespace` | Operand: kube-controller-manager pods, config, certs |
| `kube-system` | — | Additional watched namespace |
| `openshift-infra` | — | Created by static resource controller |

The `ResourceSyncController` copies ConfigMaps and Secrets between these namespaces as needed.

## Static Pod Management

The operator uses library-go's `staticpod.NewBuilder()` to manage kube-controller-manager static pods. This framework provides:

- **Installer controller** — creates new static pod revisions on each control plane node. Uses a custom installer command (`cluster-kube-controller-manager-operator installer`).
- **Revision controller** — tracks revisions of ConfigMaps and Secrets. When any revisioned resource changes, a new revision is created. The first ConfigMap in the list (`kube-controller-manager-pod`) contains the static pod manifest template.
- **Pruner** — removes old static pod revisions to free disk space.
- **PDB guard** — ensures availability during upgrades (only on multi-node clusters; disabled for single-node).

Resources are split into two categories (see the `deploymentConfigMaps`, `deploymentSecrets`, `CertConfigMaps`, and `CertSecrets` variables in `pkg/operator/starter.go` for the authoritative list):
- **Revisioned** — ConfigMaps and Secrets passed to `WithRevisionedResources`. A change to any of these triggers a new static pod revision.
- **Unrevisioned certs** — ConfigMaps and Secrets passed to `WithUnrevisionedCerts`. These are updated in-place without triggering a revision.

## Configuration Observers

Configuration observers watch external cluster resources and produce a sparse JSON config (`observedConfig`) that gets merged into the kube-controller-manager configuration. Each observer function receives the existing config and returns `(observedConfig, errors)`.

Observers are registered in `pkg/operator/configobservation/configobservercontroller/observe_config_controller.go`:

| Observer | Watches | Config paths set |
|----------|---------|-----------------|
| `CloudProviderObserver` | `Infrastructure` CR | Cloud provider config for the target namespace |
| `FeatureGatesObserver` (KCM) | `FeatureGate` CR | `extendedArguments.feature-gates` (excludes OpenShift-only gates) |
| `FeatureGatesObserver` (cluster-policy-controller) | `FeatureGate` CR | `featureGates` |
| `ObserveClusterCIDRs` | `Network` CR | `extendedArguments.cluster-cidr` |
| `ObserveServiceClusterIPRanges` | `Network` CR | `extendedArguments.service-cluster-ip-range` |
| `LatencyProfileObserver` | `Node` CR | `extendedArguments.node-monitor-grace-period` (Default: 40s, Medium: 2m, Low: 5m) |
| `ProxyObserver` | `Proxy` CR | `targetconfigcontroller.proxy` |
| `ObserveServiceCA` | Service CA ConfigMap | `serviceServingCert.certFile` |
| `ObserveInfraID` | `Infrastructure` CR | `extendedArguments.cluster-name` |
| `ObserveTLSSecurityProfile` | `APIServer` CR | TLS cipher suites and min version |

Several observers include latency profile suppression logic to prevent config updates during extreme profile transitions.

## Target Config Controller

`pkg/operator/targetconfigcontroller/` takes the merged configuration (defaults + observedConfig + unsupportedConfigOverrides) and renders it into concrete resources in the target namespace:

- `config` ConfigMap — the main kube-controller-manager configuration
- `kube-controller-manager-pod` ConfigMap — the static pod manifest template (kube-controller-manager + cluster-policy-controller containers)
- `cluster-policy-controller-config` ConfigMap — configuration for the cluster-policy-controller sidecar
- `controller-manager-kubeconfig` and `kube-controller-cert-syncer-kubeconfig` ConfigMaps
- `recycler-config` ConfigMap — persistent volume recycler configuration
- `serviceaccount-ca` ConfigMap — CA bundle for service account token verification

The default kube-controller-manager configuration lives in `bindata/assets/config/defaultconfig.yaml`. It enables leader election, dynamic provisioning, and all controllers except `ttl`, `bootstrapsigner`, and `tokencleaner`.

## Certificate Rotation

`pkg/operator/certrotationcontroller/` manages CSR signing certificate rotation:

- **CSR signer signer** (`csr-signer-signer`) — the CA that signs the CSR signer, stored in the operator namespace. Validity: 2x refresh period, refresh: 30 days (or 2 hours with `ShortCertRotation` feature gate).
- **CSR controller signer CA** (`csr-controller-signer-ca`) — CA bundle ConfigMap in the operator namespace.
- **CSR signer** (`csr-signer`) — the active CSR signing certificate, stored in the operator namespace and synced to the target namespace. Validity: 1x refresh period, refresh: half the refresh period. Signs kubelet CSRs.

## SA Token Signer

`pkg/operator/certrotationcontroller/satokensigner_controller.go` manages the `next-service-account-private-key` secret. When the current `service-account-private-key` is about to expire (or is missing), it generates a new key pair and stores it as the "next" key for rotation.

## Recovery Controller

`pkg/cmd/recoverycontroller/` provides a certificate recovery mechanism for when CSR signing certificates have expired:

- Runs the cert rotation controller in `RefreshOnlyWhenExpired` mode to regenerate expired certificates
- Includes a CSR approval controller (`pkg/cmd/recoverycontroller/csrcontroller.go`) that auto-approves kubelet CSRs signed by the recovered signer
- Invoked via the `cert-recovery-controller` subcommand

## Render Command

`pkg/cmd/render/` is a bootstrap manifest renderer used during cluster installation. It takes installer-provided inputs (cloud provider config, feature gates, cluster CIDRs, images) and renders the initial set of manifests needed to bootstrap kube-controller-manager before the operator is running. The templates live in `bindata/bootkube/`.

## Other Controllers

| Controller | Purpose |
|-----------|---------|
| `StaticResourceController` | Applies static manifests from `bindata/` (namespace, service, RBAC, network policies). Conditionally deploys vSphere legacy cloud provider resources. |
| `ClusterOperatorStatusController` | Reports operator status, versions, and related objects to `ClusterOperator/kube-controller-manager` |
| `GarbageCollectorWatcherController` | Monitors garbage collector sync failures via Prometheus metrics on the kube-controller-manager |
| `LatencyProfileController` | Manages latency profile configuration, coordinates with the installer to reject extreme profiles during transitions |
