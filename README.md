# Kubernetes Controller Manager operator

The Kube Controller Manager operator manages and updates the [kube-controller-manager](https://github.com/kubernetes/kubernetes) deployed on top of [OpenShift](https://openshift.io). The operator is based on the OpenShift [library-go](https://github.com/openshift/library-go) framework and is installed via the [Cluster Version Operator](https://github.com/openshift/cluster-version-operator) (CVO).

It contains the following components:

* Operator
* Bootstrap manifest renderer
* Static pod installer
* Configuration observer

## Quick Start

### Prerequisites

- Go 1.25+
- Access to an OpenShift cluster (for e2e testing)

### Building

```bash
make build
```

### Running Tests

```bash
# Unit tests
make test-unit

# E2E tests (requires a running OpenShift cluster)
make test-e2e
```

### Verification

```bash
make verify
```

## Configuration

The Kube Controller Manager is configured via the [`KubeControllerManager`](https://github.com/openshift/api/blob/main/operator/v1/types_kubecontrollermanager.go) custom resource:

```bash
oc describe kubecontrollermanager cluster
```

The default configuration is in [bindata/assets/config/defaultconfig.yaml](bindata/assets/config/defaultconfig.yaml).

Log verbosity can be tuned via `.spec.logLevel` (for the operand) and `.spec.operatorLogLevel` (for the operator). Valid values: `Normal`, `Debug`, `Trace`, `TraceAll`.

## Debugging

```bash
# Operator events
oc get events -n openshift-kube-controller-manager-operator

# Operator status
oc get clusteroperator/kube-controller-manager
```

## Developing

To use a custom operator image on a running cluster, override CVO management for the operator deployment:

```bash
oc patch clusterversion/version --type='merge' -p "$(cat <<- EOF
spec:
  overrides:
  - group: apps
    kind: Deployment
    name: kube-controller-manager-operator
    namespace: openshift-kube-controller-manager-operator
    unmanaged: true
EOF
)"
```

Then patch the deployment to use your image:

```bash
oc patch deployment/kube-controller-manager-operator -n openshift-kube-controller-manager-operator \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"kube-controller-manager-operator","image":"<your-image>","env":[{"name":"OPERATOR_IMAGE","value":"<your-image>"}]}]}}}}'
```

## Tests

This repository uses the [OpenShift Tests Extension (OTE)](https://github.com/openshift-eng/openshift-tests-extension) framework.

```bash
# Build the test binary
make build

# List available test suites
./cluster-kube-controller-manager-operator-tests-ext list suites

# Run a test suite
./cluster-kube-controller-manager-operator-tests-ext run-suite openshift/cluster-kube-controller-manager-operator/operator/parallel

# Run with parallel execution
./cluster-kube-controller-manager-operator-tests-ext run-suite openshift/cluster-kube-controller-manager-operator/operator/parallel -c 4
```

## Metrics

The operator exposes [Prometheus](https://prometheus.io) metrics via the `metrics` service by default.

## Documentation

- [ARCHITECTURE.md](ARCHITECTURE.md) — Design decisions and component architecture
- [CONTRIBUTING.md](CONTRIBUTING.md) — How to submit changes
- [AGENTS.md](AGENTS.md) — AI agent instructions

## Related Repositories

- [openshift/api](https://github.com/openshift/api) — API types including `KubeControllerManager`
- [openshift/library-go](https://github.com/openshift/library-go) — Shared operator framework
- [openshift/cluster-version-operator](https://github.com/openshift/cluster-version-operator) — Manages this operator's lifecycle
- [openshift/cluster-kube-apiserver-operator](https://github.com/openshift/cluster-kube-apiserver-operator) — Sibling control plane operator
- [openshift/cluster-kube-scheduler-operator](https://github.com/openshift/cluster-kube-scheduler-operator) — Sibling control plane operator
- [openshift/cluster-policy-controller](https://github.com/openshift/cluster-policy-controller) — Runs as a sidecar in the KCM static pod
