# Contributing to cluster-kube-controller-manager-operator

## Prerequisites

- Go 1.25+
- Access to an OpenShift cluster (for e2e tests)

## Development Workflow

1. Fork the repo and clone your fork
2. Create a feature branch from `main`
3. Make your changes, add or update tests
4. Run verification locally:
   ```bash
   make build
   make test-unit
   make verify
   ```
5. If you changed dependencies: `go mod tidy && go mod vendor` (commit vendor separately)
6. Push your branch and open a PR

## Building and Testing

| Command | What It Runs |
|---------|-------------|
| `make build` | Builds the operator and test binaries |
| `make test-unit` | Unit tests (`pkg/...`, `cmd/...`) |
| `make test-e2e` | E2E tests (requires a running OpenShift cluster) |
| `make verify` | Runs `gofmt`, `govet`, and Go version checks |
| `make update` | Auto-fixes `gofmt` issues |

### OTE (OpenShift Tests Extension)

E2E tests use the [OTE framework](https://github.com/openshift-eng/openshift-tests-extension). After `make build`:

```bash
# List available suites
./cluster-kube-controller-manager-operator-tests-ext list suites

# Run the parallel suite
./cluster-kube-controller-manager-operator-tests-ext run-suite \
  openshift/cluster-kube-controller-manager-operator/operator/parallel -c 4

# Run with JUnit output
./cluster-kube-controller-manager-operator-tests-ext run-suite \
  openshift/cluster-kube-controller-manager-operator/operator/parallel \
  --junit-path "${ARTIFACT_DIR}/junit.xml"
```

## Pull Request Guidelines

- Keep PRs focused — one logical change per PR.
- Reference JIRA tickets in the PR title: `OCPBUGS-XXXXX: description` or `CNTRLPLANE-XXXX: description`. Use `NO-JIRA:` for non-ticket work.
- Include tests for new functionality.
- PRs require `/lgtm` from a reviewer and `/approve` from an approver (see [OWNERS](OWNERS)).
- All PRs require `/verified` before merge — see the [OpenShift verification workflow](https://docs.google.com/document/d/1yqnJgOX_Q0LS_TlegrVjFqnP1toyXsWjEEHbOaYGFHE).

## Code Conventions

- Run `make verify` before submitting — CI will reject `gofmt` and `govet` failures.
- Use library-go patterns for new controllers (factory, informer-driven sync).
- Config observers go in `pkg/operator/configobservation/` — each observer in its own subdirectory with tests.
- Static resources go in `bindata/assets/kube-controller-manager/`. RBAC and network policies are applied by StaticResourceController; config and pod template by TargetConfigController.

## Areas Requiring Extra Care

- **Vendored dependencies** (`vendor/`): Never edit directly. Change `go.mod`, then `go mod tidy && go mod vendor`. Always commit vendor changes separately from code changes.
- **Static pod template** (`bindata/assets/kube-controller-manager/pod.yaml`): Changes here affect all control plane nodes. The pod runs four containers (kube-controller-manager, cluster-policy-controller, cert-syncer, recovery-controller).
- **Network observer** (`pkg/operator/configobservation/network/`): Has its own OWNERS file — the networking team reviews changes here.
- **CVO manifests** (`manifests/`): These are applied by the Cluster Version Operator. Ordering matters — files are prefixed with `0000_25_` for run-level 25.

## CI Pipeline

CI is powered by Prow and [ci-operator](https://docs.ci.openshift.org/). The build root image is defined in `.ci-operator.yaml`. PR jobs include:

- `pull-ci-*-unit` — unit tests
- `pull-ci-*-verify` — gofmt, govet, Go version checks
- `pull-ci-*-images` — container image build

E2E jobs run against ephemeral OpenShift clusters provisioned by ci-operator.

## Review and Approval

The `control-plane-approvers` team handles both review and approval (see [OWNERS_ALIASES](OWNERS_ALIASES)). Some subdirectories have their own OWNERS with separate reviewers/approvers — check `test/OWNERS` and `pkg/operator/configobservation/network/OWNERS`.
