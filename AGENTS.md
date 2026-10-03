# AI Agent Instructions for cluster-kube-controller-manager-operator

> Also read [ARCHITECTURE.md](ARCHITECTURE.md) for design decisions and [CONTRIBUTING.md](CONTRIBUTING.md) for workflow.

## What This Repo Is

An OpenShift operator that manages the kube-controller-manager static pod on control plane nodes. It handles configuration rendering, certificate rotation, SA token signing key management, and static pod revision tracking. Built on [library-go](https://github.com/openshift/library-go)'s static pod operator framework. Installed by the Cluster Version Operator at run-level 25.

## Repository Layout

```text
cmd/                                    # Entry points
  cluster-kube-controller-manager-operator/  # Main operator binary
  cluster-kube-controller-manager-operator-tests-ext/  # OTE test binary
pkg/
  operator/                             # Core operator logic
    starter.go                          #   Controller wiring and startup
    certrotationcontroller/             #   CSR signer + SA token key rotation
    configobservation/                  #   Config observers (network, cloud, TLS, etc.)
    gcwatchercontroller/                #   Garbage collector Prometheus watcher
    resourcesynccontroller/             #   Cross-namespace resource sync
    targetconfigcontroller/             #   Renders KCM config, kubeconfig, pod template
    operatorclient/                     #   Namespace constants and client interface
  cmd/                                  # Subcommands (operator, render, recoverycontroller, resourcegraph)
bindata/
  assets/                              # Operator-applied resources (RBAC, pod template, network policies)
  bootkube/                            # Bootstrap-phase manifests
manifests/                             # CVO-managed resources (deployment, RBAC, monitoring)
test/
  e2e/                                 # E2E tests (require OpenShift cluster)
  e2e-preferred-host/                  # Preferred-host e2e tests
```

## Build and Test Commands

```bash
make build          # Build operator and test binaries
make test-unit      # Unit tests (pkg/..., cmd/...)
make test-e2e       # E2E tests (requires running cluster)
make verify         # gofmt, govet, Go version checks
make update         # Auto-fix gofmt issues
```

## Critical Rules

1. **Never edit `vendor/` directly.** Change `go.mod`, then `go mod tidy && go mod vendor`. Always commit vendor changes separately from code changes for reviewable diffs.

2. **Static pod template changes affect all control plane nodes.** `bindata/assets/kube-controller-manager/pod.yaml` defines four containers (kube-controller-manager, cluster-policy-controller, cert-syncer, recovery-controller). Changes here trigger rolling restarts across control plane.

3. **CVO manifest ordering matters.** Files in `manifests/` are prefixed `0000_25_` for run-level 25. This operator must upgrade after kube-apiserver (run-level 20). Don't change the prefix.

4. **Cert rotation has safety gates.** The SA token signer waits for bootstrap node departure and uses a 5-minute promotion delay. Don't bypass these — they prevent token validation failures cluster-wide.

5. **This operator does not run in HyperShift.** The operator Deployment is excluded from hosted control plane topologies. HyperShift's control-plane-operator manages KCM directly.

6. **Feature gate changes cause operator exit.** The operator process calls `os.Exit(0)` when the resolved feature gate set changes (by design, via library-go). This is a restart, not a crash.

## Key Patterns

- **Config observers** go in `pkg/operator/configobservation/<name>/` — one subdirectory per observer, each with its own unit tests. Observers write to `.status.observedConfig` on the KubeControllerManager CR.
- **Static resources** go in `bindata/assets/kube-controller-manager/`. RBAC, namespaces, and network policies are applied by StaticResourceController (in `starter.go`, uses `bindata.Asset`). Config, pod template, and kubeconfig are applied by TargetConfigController (uses `bindata.MustAsset`).
- **Resource syncing** uses ResourceSyncController to copy ConfigMaps/Secrets from `openshift-config` or `openshift-config-managed` to the target namespace. Don't read global config directly in controllers.
- **Library-go factory pattern** for all controllers — use `factory.New().WithInformers().WithSync().ToController()`.
- **Dual feature gate observers** — the ConfigObserver runs two separate feature gate observers: one for the KCM container (filters out OpenShift-only gates that KCM doesn't understand) and one for the cluster-policy-controller container (unfiltered). Both write to different paths in `.status.observedConfig`.

## What NOT to Do

- **Don't read cluster config directly in TargetConfigController.** Use the ObservedConfig pattern — ConfigObserver writes to the CR status, TargetConfigController reads from there.
- **Don't add HyperShift logic.** This operator is standalone-only. HyperShift has its own KCM management.
- **Don't modify `pkg/operator/configobservation/network/` without networking team review.** It has separate OWNERS.
- **Don't skip `make verify` before submitting.** CI runs gofmt, govet, and Go version checks.

## Test Suites

- **Unit tests:** Colocated with source in `pkg/`. Run `make test-unit`.
- **E2E tests:** In `test/e2e/` and `test/e2e-preferred-host/`. Require a running OpenShift cluster. Run `make test-e2e`.
- **OTE integration:** Tests are registered with [openshift-tests-extension](https://github.com/openshift-eng/openshift-tests-extension). Suite: `openshift/cluster-kube-controller-manager-operator/operator/parallel`.
