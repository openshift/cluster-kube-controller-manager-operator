# Architecture: cluster-kube-controller-manager-operator

## Scope

This operator manages the lifecycle of the **kube-controller-manager** static pod on OpenShift control plane nodes. It handles configuration rendering, certificate rotation, service account token signing key management, and static pod revision tracking.

**What it manages:**
- The kube-controller-manager static pod and its configuration
- CSR signing certificate rotation
- Service account token signing key rotation
- Resource syncing from global config namespaces to the target namespace

**What it does NOT manage:**
- The kube-controller-manager binary itself (that's upstream Kubernetes)
- Cloud controller managers (separate operators per cloud provider)
- The cluster-policy-controller binary (runs as a sidecar in the same pod, but this operator generates its config — see [Design Decisions](#design-decisions))

## Namespace Map

| Namespace | Constant | Role |
|-----------|----------|------|
| `openshift-kube-controller-manager-operator` | `OperatorNamespace` | Operator pod, signing CAs, staging secrets |
| `openshift-kube-controller-manager` | `TargetNamespace` | Target — static pod, revisioned ConfigMaps/Secrets |
| `openshift-config` | `GlobalUserSpecifiedConfigNamespace` | User-specified global config (consumed, never written) |
| `openshift-config-managed` | `GlobalMachineSpecifiedConfigNamespace` | Machine-specified global config (consumed, never written) |
| `kube-system` | — | Kubernetes system namespace (watched) |
| `openshift-infra` | — | OpenShift infrastructure namespace (created and watched) |

Constants are defined in `pkg/operator/operatorclient/interfaces.go`.

## Component Overview

The operator follows the **library-go static pod operator pattern**: observe cluster config, generate target config, create revisions, install static pods. All controllers use the library-go factory pattern with informer-driven work queues.

This operator runs at CVO **run-level 25** (`0000_25_*` manifest prefix), meaning it upgrades *after* the kube-apiserver operator (run-level 20). This ordering is required because KCM must be N-1 compatible with the API server during upgrades.

The operator process **exits on feature gate changes** — library-go's `NewFeatureGateAccess` calls `os.Exit(0)` if the resolved feature gate set changes, forcing a restart with the new gates. This is by design, not a crash.

```
 Cluster Config Resources              KubeControllerManager CR
 (Infrastructure, Networks,            (operator.openshift.io/v1)
  FeatureGates, Proxies)                        │
         │                                      │
         ▼                                      │
 ┌─────────────────┐                            │
 │ ConfigObserver   │── writes ObservedConfig ──▶│
 └─────────────────┘                            │
                                                ▼
 ┌─────────────────┐    ┌──────────────────────────────┐
 │ ResourceSync    │───▶│ TargetConfigController       │
 │ (copies secrets │    │ (renders config, kubeconfig,  │
 │  & CAs across   │    │  pod template, recycler cfg) │
 │  namespaces)    │    └──────────────┬───────────────┘
 └─────────────────┘                   │
                                       ▼
                        ┌──────────────────────────────┐
                        │ Static Pod Controllers       │
                        │ (Revision, Installer, Prune, │
                        │  PDB Guard)                  │
                        └──────────────────────────────┘
```

## Controllers

| Controller | Purpose | Key Watches | Key Outputs |
|-----------|---------|-------------|-------------|
| **ConfigObserver** | Observes cluster-wide config and writes `.status.observedConfig` on the CR. Runs two separate feature gate observers: one for KCM (filters out OpenShift-only gates) and one for CPC (unfiltered). | Infrastructure, Networks, FeatureGates, Proxies, Nodes, APIServers | ObservedConfig JSON on KubeControllerManager CR |
| **TargetConfigController** | Renders KCM config, kubeconfig, pod template, and supporting ConfigMaps/Secrets | KubeControllerManager CR, Infrastructure | `config`, `controller-manager-kubeconfig`, `kube-controller-manager-pod` ConfigMaps; `csr-signer` Secret |
| **ResourceSyncController** | Copies Secrets and ConfigMaps from global config namespaces to the target namespace | ConfigMaps/Secrets in `openshift-config`, `openshift-config-managed` | Synced copies in target namespace (`client-ca`, `service-ca`, etc.) |
| **CertRotationController** | Rotates the CSR signing CA and leaf certificates | Secrets/ConfigMaps in operator and target namespaces, FeatureGates | `csr-signer-signer` CA, `csr-signer` leaf cert, `csr-controller-signer-ca` bundle |
| **SATokenSignerController** | Rotates the service account token signing key with a 5-minute promotion delay | Secrets in config namespaces, Endpoints (`default/kubernetes`), Pods in `openshift-kube-apiserver` | `service-account-private-key` Secret, `sa-token-signing-certs` history |
| **GCWatcherController** | Monitors Prometheus for garbage collector sync failures | ClusterOperator/monitoring, Prometheus/Thanos queries | Status conditions on KubeControllerManager CR |
| **Static Pod controllers** | Revision tracking, pod installation, pruning, PDB guard | ConfigMaps/Secrets in target namespace, Pods | Static pod manifest revisions, PodDisruptionBudget (HA only) |
| **ClusterOperatorStatus** | Reports operator health to the ClusterOperator API | KubeControllerManager CR conditions | ClusterOperator/kube-controller-manager status |

## CRDs and API Types

**Owned:**
- `KubeControllerManager` (`operator.openshift.io/v1`) — singleton named `cluster`. Embeds `StaticPodOperatorSpec`/`StaticPodOperatorStatus` from library-go.

**Consumed:**
- `Infrastructure`, `Network`, `FeatureGate`, `Proxy`, `APIServer` (`config.openshift.io/v1`)
- `KubeControllerManagerConfig` (`kubecontrolplane.config.openshift.io/v1`) — the operand's config schema

## Manifest and Resource Management

**CVO-managed** (`manifests/`): Operator namespace, deployment, RBAC, ServiceMonitors, PrometheusRules, ClusterOperator status object. Prefixed `0000_25_` (run-level 25).

**Operator-applied** (`bindata/assets/`): Split across two controllers:
- **StaticResourceController** applies RBAC, namespace, network policies, and service accounts (uses `bindata.Asset`). Includes conditional vSphere resources.
- **TargetConfigController** applies the pod template, config, kubeconfig, and recycler config (uses `bindata.MustAsset`).

**Revision-tracked ConfigMaps:** `kube-controller-manager-pod`, `config`, `cluster-policy-controller-config`, `controller-manager-kubeconfig`, `kube-controller-cert-syncer-kubeconfig`, `cloud-config`, `serviceaccount-ca`, `service-ca`, `recycler-config`.

**Revision-tracked Secrets:** `service-account-private-key`, `serving-cert`, `localhost-recovery-client-token`.

**Unrevisioned certs** (changes trigger rollout but don't create new revisions): `aggregator-client-ca`, `client-ca`, `trusted-ca-bundle`, `kube-controller-manager-client-cert-key`, `csr-signer`.

## Platform and Topology Behavior

- **Standalone / Self-managed:** The operator runs on self-managed OpenShift clusters (HA and SNO). It is included in the release payload with `include.release.openshift.io/self-managed-high-availability: "true"` and `include.release.openshift.io/single-node-developer: "true"`.
- **Single-node (SNO):** Detected via `IsSingleNodePlatformFn()`. Skips PodDisruptionBudget creation since there's only one control plane node.
- **HyperShift (hosted control planes):** This operator does **not** run in HyperShift topology. The operator Deployment is explicitly excluded (`exclude.release.openshift.io/internal-openshift-hosted: "true"`). HyperShift's own control-plane-operator manages the kube-controller-manager directly. Only supporting resources (namespace, RBAC, monitoring) carry `include.release.openshift.io/hypershift: "true"` so the hosted cluster has the infrastructure the KCM operand needs.
- **vSphere:** Applies additional legacy cloud provider RBAC from `bindata/assets/kube-controller-manager/vsphere/`.
- **All platforms:** Reads `Infrastructure.Status.APIServerInternalURL` to render the KCM kubeconfig. Cloud provider config is injected via the ConfigObserver based on platform type.

## Certificate Management

| Certificate | Rotation Period | Validity | Storage |
|------------|----------------|----------|---------|
| CSR signer CA (`csr-signer-signer`) | 30 days (2h with `ShortCertRotation`) | 60 days | Secret in operator namespace |
| CSR signer leaf (`csr-signer`) | Derived from CA | Derived from CA | Secret in target namespace |
| SA token signing key | On-demand, 5-min promotion delay | N/A (RSA key pair) | Secret in target namespace + history ConfigMap |

**Bootstrap transition:** The installer creates a CSR signer with only **24-hour validity** — deliberately short to limit blast radius during the least-trusted bootstrap phase. This operator's CertRotationController replaces it with a 30-day signer once the cluster is running. Target certs issued against the bootstrap signer are capped to the signer's remaining lifetime, so nodes must be operational within ~19 hours of install.

The SA token signer waits for the bootstrap node to depart (by checking `default/kubernetes` endpoints against kube-apiserver pod IPs) before performing its first rotation.

With the `ConfigurablePKI` feature gate enabled ([enhancements#1882](https://github.com/openshift/enhancements/blob/master/enhancements/api-review/1882-configurable-pki.md)), cert validity and refresh periods can be overridden via PKI profile custom resources. This operator is one of six that integrate with the configurable PKI framework (OCPSTRAT-2271).

## Dependencies

- **[library-go](https://github.com/openshift/library-go):** Static pod controllers, config observer framework, cert rotation primitives, resource sync, operator status reporting. This is the core framework dependency.
- **[openshift/api](https://github.com/openshift/api):** CRD types (`KubeControllerManager`), config types, feature gate definitions.
- **[openshift/client-go](https://github.com/openshift/client-go):** Generated clients for OpenShift API types.
- **Upstream Kubernetes** (`k8s.io/*`): Client libraries, informer framework, API machinery.

## Testing Strategy

- **Unit tests** (`pkg/...`): ConfigObserver logic, target config rendering, cert rotation edge cases. Run with `make test-unit`.
- **E2E tests** (`test/e2e/...`): Validate operator behavior on a live cluster — status reporting, static pod rollouts, network policy enforcement. Run with `make test-e2e`.
- **OTE framework**: Tests are registered with [openshift-tests-extension](https://github.com/openshift-eng/openshift-tests-extension) for CI integration. Suite: `openshift/cluster-kube-controller-manager-operator/operator/parallel`.

## Recovery Controller

`pkg/cmd/recoverycontroller/` provides a certificate recovery mechanism for when CSR signing certificates have expired. It runs the cert rotation controller in `RefreshOnlyWhenExpired` mode to regenerate expired certificates, and includes a CSR approval controller that auto-approves kubelet CSRs signed by the recovered signer. Runs as a container in the static pod via the `cert-recovery-controller` subcommand.

## Render Command

`pkg/cmd/render/` is a bootstrap manifest renderer used during cluster installation. It takes installer-provided inputs (cloud provider config, feature gates, cluster CIDRs, images) and renders the initial set of manifests needed to bootstrap the kube-controller-manager before the operator is running. Templates live in `bindata/bootkube/`.

## Design Decisions

1. **Static pod pattern over Deployment:** The kube-controller-manager runs as a static pod managed by kubelet, not as a Deployment. This avoids a circular dependency — KCM manages controllers that Deployments depend on (e.g., service account token controller). The operator writes pod manifests that kubelet picks up directly.

2. **Revision-based rollouts:** Configuration changes create new revisions (numbered copies of ConfigMaps/Secrets). The installer controller rolls out one node at a time by writing a new static pod manifest referencing the latest revision. This provides rollback capability and audit trail.

3. **Cluster-policy-controller as a sidecar:** The cluster-policy-controller ([openshift/cluster-policy-controller](https://github.com/openshift/cluster-policy-controller)) runs as a container in the KCM static pod rather than as a separate Deployment. This was a deliberate decision made for OpenShift 4.3 (PR #297, October 2019). The primary driver is a **bootstrap chicken-and-egg problem**: CPC's controllers (namespace SCC allocation, quota reconciliation, PSA label syncing) must be running before any Deployments can be scheduled — pods cannot be created without UID range and SELinux label allocation. Placing these controllers in a static pod breaks the circular dependency. The KCM pod was chosen because CPC shares the same service account (`system:kube-controller-manager`), RBAC, certificates, kubeconfig, and leader election namespace, avoiding infrastructure duplication.

4. **5-minute SA key promotion delay:** New service account signing keys are staged in `next-service-account-private-key` for 5 minutes before promotion. This gives the kube-apiserver time to observe the new public key via the `sa-token-signing-certs` bundle, preventing token validation failures during rotation.

5. **Bootstrap node departure gate:** SA token key rotation is blocked until the bootstrap node has left the cluster. The bootstrap node uses a different signing key; rotating before it departs could cause token validation failures for workloads it started.

6. **ObservedConfig indirection:** Rather than reading cluster config directly in the target config controller, a separate ConfigObserver writes a merged JSON blob to `.status.observedConfig` on the CR. This decouples config sources from config consumers and makes the effective config inspectable via `oc get kubecontrollermanager cluster -o jsonpath='{.status.observedConfig}'`.

7. **24-hour bootstrap signer with operator takeover:** The installer deliberately creates a short-lived CSR signer (24h) to minimize trust window during bootstrap. This operator's cert rotation controller takes ownership and issues a longer-lived replacement, ensuring the cluster transitions from minimal-trust bootstrap to managed cert lifecycle.

8. **UseMoreSecureServiceCA bypasses ObservedConfig (tech debt):** The TargetConfigController reads `.spec.useMoreSecureServiceCA` directly from the operator spec rather than going through the ObservedConfig pattern. This is acknowledged in code as needing migration to a config observer, but requires changes to the observedConfig format.
