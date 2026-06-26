# Cluster Kube Controller Manager Operator

A static pod operator that manages the lifecycle of `kube-controller-manager` on OpenShift control plane nodes. Built on the [library-go](https://github.com/openshift/library-go) static pod operator framework, it observes cluster configuration, rotates CSR signing certificates, manages the cluster-policy-controller sidecar, and reconciles the target kube-controller-manager config into static pod manifests. Installed by the [Cluster Version Operator](https://github.com/openshift/cluster-version-operator) (CVO).

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full design and data flow.

## Build and Test

```bash
make build                       # Build all binaries (operator + OTE test runner)
make test-unit                   # Unit tests (./pkg/... ./cmd/...)
make verify                      # Formatting, vetting, golang version checks
make test-e2e                    # E2E operator tests (30m timeout)
make test-e2e-preferred-host     # Preferred host e2e tests (1h timeout)
```

Go version: see `go.mod`.

## Project Structure

| Directory | Purpose |
|-----------|---------|
| `cmd/cluster-kube-controller-manager-operator/` | Operator binary entry point (operator, render, installer, pruner, resource-graph, cert-sync, recovery-controller) |
| `cmd/cluster-kube-controller-manager-operator-tests-ext/` | OTE test runner entry point |
| `pkg/operator/starter.go` | Operator initialization — creates clients, informers, and starts all controllers |
| `pkg/operator/targetconfigcontroller/` | Renders observed config + defaults into kube-controller-manager ConfigMaps/Secrets |
| `pkg/operator/configobservation/` | Configuration observers — each subdirectory watches a cluster resource type |
| `pkg/operator/certrotationcontroller/` | CSR signer certificate rotation and SA token signer controller |
| `pkg/operator/resourcesynccontroller/` | Syncs ConfigMaps/Secrets between namespaces |
| `pkg/operator/operatorclient/` | Namespace constants and operator client interfaces |
| `pkg/operator/gcwatchercontroller/` | Monitors garbage collector metrics via Prometheus |
| `pkg/cmd/operator/` | Operator subcommand — wires `RunOperator()` into the binary's command tree |
| `pkg/cmd/render/` | Bootstrap manifest renderer for cluster installation |
| `pkg/cmd/recoverycontroller/` | Certificate recovery controller (CSR signer + CSR approval) |
| `pkg/cmd/resourcegraph/` | Resource dependency chain visualization |
| `bindata/` | Embedded assets: default config, static pod template, RBAC, bootstrap manifests, vSphere resources |
| `manifests/` | CVO deployment manifests (namespace, deployment, RBAC, ServiceMonitors, alerts) |
| `test/e2e/` | E2E test suite (operator, network policy, SA token signer) |
| `test/e2e-preferred-host/` | Preferred host communication tests |
| `test/library/` | Shared test utilities |

## Controller Pattern

Controllers use the library-go `factory.Controller` base. Each controller has a `sync(ctx, syncContext)` method called by the framework on informer events or periodic resyncs. The operator wires them in `pkg/operator/starter.go` via `RunOperator()`.

Config observers follow a specific pattern: each observer function receives the existing config and returns `(observedConfig, errors)`. Observers are registered in `pkg/operator/configobservation/configobservercontroller/observe_config_controller.go`.

## Key Conventions

- **Namespaces:** `openshift-kube-controller-manager-operator` (operator), `openshift-kube-controller-manager` (operand), `openshift-config` (user config), `openshift-config-managed` (platform config). Constants in `pkg/operator/operatorclient/interfaces.go`.
- **Logging:** `k8s.io/klog/v2` with verbosity levels
- **Error handling:** wrap with `fmt.Errorf("context: %w", err)`
- **Feature gates:** controllers that depend on feature gates use `FeatureGateAccessor` from library-go; wait for gates before starting
- **Platform conditionals:** vSphere legacy cloud provider resources are only deployed when `Infrastructure.Status.PlatformStatus.Type == VSpherePlatformType`
- **Upstream changes:** controllers that wrap library-go functionality should have fixes made upstream in [library-go](https://github.com/openshift/library-go), not here

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for full guidelines. Key rules:

- Do not modify files under `vendor/`. Use `go mod tidy && go mod vendor`.
- `bindata/assets.go` uses Go's `embed` directive to embed asset files — update the embedded files, not this file.
- Write unit tests for every change. E2E tests for significant features.
- Backwards compatibility matters — deprecate before removing.
- Before modifying the operator API, ensure there is a corresponding enhancement proposal in [openshift/enhancements](https://github.com/openshift/enhancements). API changes require design review and approval.

## Testing

- **Unit tests:** co-located `*_test.go` files, table-driven, `go test ./pkg/... ./cmd/...`
- **E2E tests:** suites under `test/e2e/` and `test/e2e-preferred-host/`, using Ginkgo v2.
- **OTE framework:** `cluster-kube-controller-manager-operator-tests-ext` binary. See [CONTRIBUTING.md](CONTRIBUTING.md#openshift-tests-extension-ote) for usage.
