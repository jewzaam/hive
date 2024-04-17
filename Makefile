.PHONY: all
all: vendor update test build

# In openshift ci (Prow), we need to set $HOME to a writable directory else tests will fail
# because they don't have permissions to create /.local or /.cache directories
# as $HOME is set to "/" by default.
ifeq ($(HOME),/)
export HOME=/tmp/home
endif

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/controller-gen.mk \
	targets/openshift/yq.mk \
	targets/openshift/bindata.mk \
	targets/openshift/deps.mk \
	targets/openshift/images.mk \
	targets/openshift/kustomize.mk \
)

DOCKER_CMD ?= docker
CONTAINER_BUILD_FLAGS ?= --file ./Dockerfile

# Namespace hive-operator will run:
HIVE_OPERATOR_NS ?= hive

# Namespace hive-controllers/hiveadmission/etc will run:
HIVE_NS ?= hive

# Log level that should be used when running hive from source, or with make deploy.
LOG_LEVEL ?= debug

# Image URL to use all building/pushing image targets
IMG ?= hive-controller:latest

GO_PACKAGES :=./...
GO_BUILD_PACKAGES :=./cmd/... ./contrib/cmd/hiveutil
GO_BUILD_BINDIR :=bin
# Exclude e2e tests from unit testing
GO_TEST_PACKAGES :=./pkg/... ./cmd/... ./contrib/...

GO_SUB_MODULES :=./apis

ifeq "$(GO_MOD_FLAGS)" "-mod=vendor"
	ifeq "$(GOFLAGS)" ""
		GOFLAGS_FOR_GENERATE ?= GOFLAGS=-mod=vendor
	else
		GOFLAGS_FOR_GENERATE ?= GOFLAGS=-mod=vendor,$(GOFLAGS)
	endif
endif

# Look up distro name (e.g. Fedora)
DISTRO ?= $(shell if which lsb_release &> /dev/null; then lsb_release -si; else echo "Unknown"; fi)

# Default fedora to not using sudo since it's not needed
ifeq ($(DISTRO),Fedora)
	SUDO_CMD =
else # Other distros like RHEL 7 and CentOS 7 currently need sudo.
	SUDO_CMD = sudo
endif

# build-machinery-go adds a versionFromGit to -ldflags that by default constructs the version
# string based on the most recent repository tag in this branch. That doesn't work for us, since
# we don't tag versions. Override using the same versioning we apply to OperatorHub builds:
# v{major}.{minor}.{commitcount}-{sha}
# Note that building against a local commit may result in {major}.{minor} being rendered as
# `UnknownBranch`. However, the {commitcount} and {sha} should still be accurate.
SOURCE_GIT_TAG := $(shell export HOME=$(HOME); python3 -mpip install --user gitpython >&2; hack/version2.py)

BINDATA_INPUTS :=./config/clustersync/... ./config/hiveadmission/... ./config/controllers/... ./config/rbac/... ./config/configmaps/...
$(call add-bindata,operator,$(BINDATA_INPUTS),,assets,pkg/operator/assets/bindata.go)

$(call build-image,hive,$(IMG),./Dockerfile,.)
$(call build-image,hive-fedora-dev-base,hive-fedora-dev-base,./build/fedora-dev/Dockerfile.devbase,.)
$(call build-image,hive-fedora-dev,$(IMG),./build/fedora-dev/Dockerfile.dev,.)
$(call build-image,hive-build,"hive-build:latest",./build/build-image/Dockerfile,.)

clean:
	rm -rf $(GO_BUILD_BINDIR)

.PHONY: vendor
vendor:
	go mod tidy
	go mod vendor

.PHONY: vendor-submodules
vendor-submodules: $(addprefix vendor-submodules-,$(GO_SUB_MODULES))
vendor: vendor-submodules

.PHONY: $(addprefix vendor-submodules-,$(GO_SUB_MODULES))
$(addprefix vendor-submodules-,$(GO_SUB_MODULES)):
	# handle tidy for submodules
	(cd $(subst vendor-submodules-,,$@); go mod tidy && go mod vendor)

# Update the manifest directory of artifacts OLM will deploy. Copies files in from
# the locations kubebuilder generates them.
.PHONY: manifests
manifests: crd

# controller-gen is adding a yaml break (---) at the beginning of each file. OLM does not like this break.
# We use yq to strip out the yaml break by having yq replace each file with yq's formatting.
# $1 - CRD file
define strip-yaml-break
	@$(YQ) m -i $(1) $(1)

endef

# patch-crd-yq allows using yq to merge patch to the CRDs generated by kubebuilder
# like adding annotations or labels etc. see yq m -h for more info
# $1 - crd file
# $2 - patch file
define patch-crd-yq
	$(YQ) m -i -x '$(1)' '$(2)'

endef

# Generate CRD yaml from our api types:
.PHONY: crd
crd: ensure-controller-gen ensure-yq
	rm -rf ./config/crds
	(cd apis; '../$(CONTROLLER_GEN)' crd:crdVersions=v1 paths=./hive/v1 paths=./hiveinternal/v1alpha1 output:dir=../config/crds)
	@echo Stripping yaml breaks from CRD files
	$(foreach p,$(wildcard ./config/crds/*.yaml),$(call strip-yaml-break,$(p)))
	@echo Patching CRD files for additional static information
	$(foreach p,$(wildcard ./config/crdspatch/*.yaml),$(call patch-crd-yq,$(subst ./config/crdspatch/,./config/crds/,$(p)),$(p)))
	# Patch ClusterProvision CRD to remove the massive PodSpec def we consider an internal implementation detail:
	@echo Patching ClusterProvision CRD yaml to remove overly verbose PodSpec details:
	$(YQ) d -i config/crds/hive.openshift.io_clusterprovisions.yaml "spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.podSpec"

	# This does not appear possible with controller-runtime flags when dealing with an array,
	# kubebuilder:validation:EmbeddedResource adds the x-kubernetes-embedded-resource to the array,
	# not the elements within it.
	@echo Patching SyncSet CRDs to flag resource RawExtensions as embedded resources:
	$(YQ) w -i config/crds/hive.openshift.io_syncsets.yaml "spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.resources.items.x-kubernetes-embedded-resource" true
	$(YQ) w -i config/crds/hive.openshift.io_syncsets.yaml "spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.resources.items.x-kubernetes-preserve-unknown-fields" true
	$(YQ) w -i config/crds/hive.openshift.io_selectorsyncsets.yaml "spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.resources.items.x-kubernetes-embedded-resource" true
	$(YQ) w -i config/crds/hive.openshift.io_selectorsyncsets.yaml "spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.resources.items.x-kubernetes-preserve-unknown-fields" true
update: crd

.PHONY: verify-crd
verify-crd: ensure-controller-gen ensure-yq
	./hack/verify-crd.sh
verify: verify-crd

.PHONY: test-unit-submodules
test-unit-submodules: $(addprefix test-unit-submodules-,$(GO_SUB_MODULES))
test-unit: test-unit-submodules

.PHONY: $(addprefix test-unit-submodules-,$(GO_SUB_MODULES))
$(addprefix test-unit-submodules-,$(GO_SUB_MODULES)):
	# hande unit test for submodule
	(cd $(subst test-unit-submodules-,,$@); $(GO) test $(GO_MOD_FLAGS) $(GO_TEST_FLAGS) ./...)

.PHONY: test-e2e
test-e2e:
	hack/e2e-test.sh

.PHONY: test-e2e-pool
test-e2e-pool:
	hack/e2e-pool-test.sh

.PHONY: test-e2e-postdeploy
test-e2e-postdeploy:
	go test $(GO_MOD_FLAGS) -v -timeout 0 -count=1 ./test/e2e/postdeploy/...

.PHONY: test-e2e-postinstall
test-e2e-postinstall:
	go test $(GO_MOD_FLAGS) -v -timeout 0 -count=1 ./test/e2e/postinstall/...

.PHONY: test-e2e-destroycluster
test-e2e-destroycluster:
	go test $(GO_MOD_FLAGS) -v -timeout 0 -count=1 ./test/e2e/destroycluster/...

.PHONY: test-e2e-uninstallhive
test-e2e-uninstallhive:
	go test $(GO_MOD_FLAGS) -v -timeout 0 -count=1 ./test/e2e/uninstallhive/...

# Run against the configured cluster in ~/.kube/config
run: build
	./bin/manager --log-level=${LOG_LEVEL}

# Run against the configured cluster in ~/.kube/config
run-operator: build
	./bin/operator --log-level=${LOG_LEVEL}

# Install CRDs into a cluster
install: crd
	oc apply -f config/crds

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
.PHONY: deploy
deploy: ensure-kustomize install
	# Deploy the operator manifests:
	oc create namespace ${HIVE_OPERATOR_NS} || true
	mkdir -p overlays/deploy
	cp overlays/template/kustomization.yaml overlays/deploy
	cd overlays/deploy && ../../$(KUSTOMIZE) edit set image registry.ci.openshift.org/openshift/hive-v4.0:hive=${IMG} && ../../$(KUSTOMIZE) edit set namespace ${HIVE_OPERATOR_NS}
	$(KUSTOMIZE) build overlays/deploy | sed 's/        - info/        - debug/' | oc apply -f -
	rm -rf overlays/deploy
	# Create a default basic HiveConfig so the operator will deploy Hive
	oc process --local=true -p HIVE_NS=${HIVE_NS} -p LOG_LEVEL=${LOG_LEVEL} -f config/templates/hiveconfig.yaml | oc apply -f -

# NOTE: Keep the paths checked below in sync with those passed to the generators in `hack/update-codegen.sh`
verify-codegen: update-codegen
	git diff --exit-code pkg/client
	git diff --exit-code apis
verify: verify-codegen

update-codegen:
	hack/update-codegen.sh
update: update-codegen

# This needs to come after codegen to copy zz_generated.deepcopy files down into vendor/
.PHONY: verify-vendor
verify-vendor: vendor
	git diff --exit-code vendor/
verify: verify-vendor

# Build the template file used for direct (OLM-less) deploy by app-sre
build-app-sre-template: ensure-kustomize
	# Sync CRDs into kustomize resources
	cd hack/app-sre && ../../$(KUSTOMIZE) edit add resource ../../config/crds/*.yaml
	# Generate temporary saas object file
	$(KUSTOMIZE) build --load-restrictor=LoadRestrictionsNone hack/app-sre --output hack/app-sre/saas-objects.yaml
	# Generate saas template
	./hack/app-sre/generate-saas-template.py hack/app-sre/saas-template-stub.yaml hack/app-sre/saas-objects.yaml hack/app-sre/saas-template.yaml
	# Remove temporary saas object file
	rm hack/app-sre/saas-objects.yaml


# This needs to go after codegen so the CRDs are up to date
verify-app-sre-template: build-app-sre-template
	git diff --exit-code hack/app-sre/
verify: verify-app-sre-template

# This needs to go after codegen so the CRDs are up to date
update: build-app-sre-template

# Check import naming
.PHONY: verify-imports
verify-imports: build
	@echo "Verifying import naming"
	@sh -c \
	  'for file in $(GOFILES) ; do \
	     $(BINDIR)/hiveutil verify-imports -c $(VERIFY_IMPORTS_CONFIG) $$file || exit 1 ; \
	   done'
verify: verify-imports

# Check lint
.PHONY: verify-lint
verify-lint: install-tools
	@echo Verifying golint
	@sh -c \
	  'for file in $(GOFILES) ; do \
	     golint --set_exit_status $$file || exit 1 ; \
	   done'
verify: verify-lint

.PHONY: verify-govet-submodules
verify-govet-submodules: $(addprefix verify-govet-submodules-,$(GO_SUB_MODULES))
verify-govet: verify-govet-submodules

.PHONY: $(addprefix verify-govet-submodules-,$(GO_SUB_MODULES))
$(addprefix verify-govet-submodules-,$(GO_SUB_MODULES)):
	# hande govet for submodule
	(cd $(subst verify-govet-submodules-,,$@); $(GO) vet $(GO_MOD_FLAGS) ./...)


# Generate code
.PHONY: generate
generate: install-tools
	$(GOFLAGS_FOR_GENERATE) go generate ./pkg/... ./cmd/...
update: generate

.PHONY: generate-submodules
generate-submodules: $(addprefix generate-submodules-,$(GO_SUB_MODULES))
generate: generate-submodules


.PHONY: $(addprefix generate-submodules-,$(GO_SUB_MODULES))
$(addprefix generate-submodules-,$(GO_SUB_MODULES)):
	# hande go generate for submodule
	(cd $(subst generate-submodules-,,$@); $(GOFLAGS_FOR_GENERATE) $(GO) generate ./...)

# Build the image using docker
.PHONY: docker-build
docker-build:
	@echo "*** DEPRECATED: Use the image-hive target instead ***"
	$(DOCKER_CMD) build $(CONTAINER_BUILD_FLAGS) -t ${IMG} .

# Push the image using docker
.PHONY: docker-push
docker-push:
	$(DOCKER_CMD) push ${IMG}

# Build and push the dev image
.PHONY: docker-dev-push
docker-dev-push: build image-hive-fedora-dev docker-push

# Build the dev image using builah
.PHONY: buildah-dev-build
buildah-dev-build:
	buildah bud --ulimit nofile=10239:10240 -f ./Dockerfile --tag ${IMG}

.PHONY: podman-dev-build
podman-dev-build:
	podman build --tag ${IMG} --ulimit nofile=10239:10240 -f ./Dockerfile .

# Build and push the dev image with buildah
.PHONY: buildah-dev-push
buildah-dev-push: buildah-dev-build
	buildah push --tls-verify=false ${IMG}

# Push the image using buildah
.PHONY: buildah-push
buildah-push:
	$(SUDO_CMD) buildah pull ${IMG}
	$(SUDO_CMD) buildah push ${IMG}

# Run golangci-lint against code
# TODO replace verify (except verify-generated), vet, fmt targets with lint as it covers all of it
.PHONY: lint
lint: install-tools
	golangci-lint run -c ./golangci.yml ./pkg/... ./cmd/... ./contrib/...
# Remove the golangci-lint from the verify until a fix is in place for permisions for writing to the /.cache directory.
#verify: lint

.PHONY: modcheck
modcheck:
	go run ./hack/modcheck.go
verify: modcheck

.PHONY: install-tools
install-tools:
	go install $(GO_MOD_FLAGS) github.com/golang/mock/mockgen
	go install $(GO_MOD_FLAGS) golang.org/x/lint/golint
	go install $(GO_MOD_FLAGS) github.com/golangci/golangci-lint/cmd/golangci-lint

.PHONY: coverage
coverage:
	hack/codecov.sh
