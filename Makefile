all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/images.mk \
	targets/openshift/deps.mk \
	targets/openshift/operator/telepresence.mk \
)

# Exclude e2e tests from unit testing
GO_TEST_PACKAGES :=./pkg/... ./cmd/...

IMAGE_REGISTRY :=registry.svc.ci.openshift.org

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
$(call build-image,ocp-cluster-kube-controller-manager-operator,$(IMAGE_REGISTRY)/ocp/4.4:cluster-kube-controller-manager-operator, ./Dockerfile.rhel7,.)

$(call verify-golang-versions,Dockerfile.rhel7)

test-e2e: GO_TEST_PACKAGES :=./test/e2e/...
test-e2e: GO_TEST_FLAGS :=-race -timeout=30m
test-e2e: test-unit
.PHONY: test-e2e

test-e2e-preferred-host: GO_TEST_PACKAGES :=./test/e2e-preferred-host/...
test-e2e-preferred-host: GO_TEST_FLAGS += -timeout 1h
test-e2e-preferred-host: test-unit
.PHONY: test-e2e-preferred-host

# Configure the 'telepresence' target
# See vendor/github.com/openshift/build-machinery-go/scripts/run-telepresence.sh for usage and configuration details
export TP_DEPLOYMENT_YAML ?=./manifests/0000_25_kube-controller-manager-operator_06_deployment.yaml
export TP_CMD_PATH ?=./cmd/cluster-kube-controller-manager-operator

# Build the openshift-tests-extension binary
tests-ext-build:
	GOOS=$(if $(GOOS),$(GOOS),$(shell go env GOOS)) GOARCH=$(if $(GOARCH),$(GOARCH),$(shell go env GOARCH)) GO_COMPLIANCE_POLICY=exempt_all CGO_ENABLED=0 \
	go build -o cluster-kube-controller-manager-operator-tests-ext \
	-ldflags "-X 'main.CommitFromGit=$(shell git rev-parse --short HEAD)' \
	-X 'main.BuildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)' \
	-X 'main.GitTreeState=$(shell if git diff-index --quiet HEAD --; then echo clean; else echo dirty; fi)'" \
	./cmd/cluster-kube-controller-manager-operator-tests-ext
.PHONY: tests-ext-build
