all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/library-go/alpha-build-machinery/make/, \
	golang.mk \
	targets/openshift/bindata.mk \
	targets/openshift/images.mk \
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
$(call build-image,ocp-cluster-kube-controller-manager-operator,$(IMAGE_REGISTRY)/ocp/4.2:cluster-kube-controller-manager-operator, ./Dockerfile.rhel7,.)

# This will call a macro called "add-bindata" which will generate bindata specific targets based on the parameters:
# $0 - macro name
# $1 - target suffix
# $2 - input dirs
# $3 - prefix
# $4 - pkg
# $5 - output
# It will generate targets {update,verify}-bindata-$(1) logically grouping them in unsuffixed versions of these targets
# and also hooked into {update,verify}-generated for broader integration.
$(call add-bindata,v4.1.0,./bindata/v4.1.0/...,bindata,v411_00_assets,pkg/operator/v411_00_assets/bindata.go)

clean:
	$(RM) ./cluster-kube-controller-manager-operator
.PHONY: clean

test-e2e: GO_TEST_PACKAGES :=./test/e2e/...
test-e2e: test-unit
.PHONY: test-e2e

CRD_SCHEMA_GEN_VERSION := v1.0.0
crd-schema-gen:
	git clone -b $(CRD_SCHEMA_GEN_VERSION) --single-branch --depth 1 https://github.com/openshift/crd-schema-gen.git $(CRD_SCHEMA_GEN_GOPATH)/src/github.com/openshift/crd-schema-gen
	GOPATH=$(CRD_SCHEMA_GEN_GOPATH) GOBIN=$(CRD_SCHEMA_GEN_GOPATH)/bin go install $(CRD_SCHEMA_GEN_GOPATH)/src/github.com/openshift/crd-schema-gen/cmd/crd-schema-gen
.PHONY: crd-schema-gen

update-codegen-crds: CRD_SCHEMA_GEN_GOPATH :=$(shell mktemp -d)
update-codegen-crds: crd-schema-gen
	$(CRD_SCHEMA_GEN_GOPATH)/bin/crd-schema-gen --apis-dir vendor/github.com/openshift/api/operator/v1
.PHONY: update-codegen-crds
update-codegen: update-codegen-crds
.PHONY: update-codegen

verify-codegen-crds: CRD_SCHEMA_GEN_GOPATH :=$(shell mktemp -d)
verify-codegen-crds: crd-schema-gen
	$(CRD_SCHEMA_GEN_GOPATH)/bin/crd-schema-gen --apis-dir vendor/github.com/openshift/api/operator/v1 --verify-only
.PHONY: verify-codegen-crds
verify-codegen: verify-codegen-crds
.PHONY: verify-codegen

verify: verify-codegen
