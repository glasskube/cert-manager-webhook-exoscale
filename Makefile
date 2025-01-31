GO ?= $(shell which go)
OS ?= $(shell $(GO) env GOOS)
ARCH ?= $(shell $(GO) env GOARCH)

IMAGE_NAME := "ghcr.io/glasskube/cert-manager-webhook-exoscale"
IMAGE_TAG := "latest"

OUT := $(shell pwd)/_out

KUBEBUILDER_VERSION=1.28.0

HELM_FILES := $(shell find deploy/cert-manager-webhook-exoscale)

test: _test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/etcd _test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/kube-apiserver _test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/kubectl
	TEST_ASSET_ETCD=_test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/etcd \
	TEST_ASSET_KUBE_APISERVER=_test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/kube-apiserver \
	TEST_ASSET_KUBECTL=_test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/kubectl \
	$(GO) test -v .

_test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH).tar.gz: | _test
	curl -fsSL https://go.kubebuilder.io/test-tools/$(KUBEBUILDER_VERSION)/$(OS)/$(ARCH) -o $@

_test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/etcd _test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/kube-apiserver _test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)/kubectl: _test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH).tar.gz | _test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH)
	tar xfO $< kubebuilder/bin/$(notdir $@) > $@ && chmod +x $@

.PHONY: clean
clean:
	rm -r _test $(OUT)

.PHONY: build
build:
	CGO_ENABLED=0 go build -o $(OUT)/webhook -ldflags '-w -extldflags "-static"' .

.PHONY: docker-build
docker-build:
	docker build -t "$(IMAGE_NAME):$(IMAGE_TAG)" --network host .

.PHONY: rendered-manifest.yaml
rendered-manifest.yaml: $(OUT)/rendered-manifest.yaml

$(OUT)/rendered-manifest.yaml: $(HELM_FILES) | $(OUT)
	helm template test \
            --set image.repository=$(IMAGE_NAME) \
            deploy/cert-manager-webhook-exoscale > $@

_test $(OUT) _test/kubebuilder-$(KUBEBUILDER_VERSION)-$(OS)-$(ARCH):
	mkdir -p $@
