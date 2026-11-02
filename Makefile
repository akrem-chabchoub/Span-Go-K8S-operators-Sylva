KIND_CLUSTER ?= span
KIND_KUBECONFIG ?= $(CURDIR)/bin/span.kubeconfig
ENVTEST_K8S_VERSION ?= 1.34.1
CA_PLACEHOLDER ?= U1BBTl9DQV9QTEFDRUhPTERFUg==

KUBECTL = kubectl --kubeconfig $(KIND_KUBECONFIG)

.PHONY: fmt test lint run crds rbac image helper-image helper-self-test kind-up kind-load kind-demo kind-down certs flux-lab argo-lab kcp-lab

fmt:
	gofmt -w .

test:
	mkdir -p bin
	KUBEBUILDER_ASSETS="$$(go tool setup-envtest use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(CURDIR)/bin/k8s)" go test ./...

lint:
	golangci-lint run ./...

run:
	go run ./cmd --enable-webhook=false

crds:
	$(KUBECTL) apply -f config/crd/bases

rbac:
	$(KUBECTL) apply -f config/rbac/role.yaml

image:
	docker build -t span:dev .

helper-image:
	docker build -f Dockerfile.helper -t span-helper:dev .

kind-up:
	mkdir -p bin
	@if ! kind get clusters | grep -qx $(KIND_CLUSTER); then \
		kind create cluster --name $(KIND_CLUSTER) --kubeconfig $(KIND_KUBECONFIG); \
	else \
		kind export kubeconfig --name $(KIND_CLUSTER) --kubeconfig $(KIND_KUBECONFIG); \
	fi

kind-load: image helper-image
	kind load docker-image span:dev --name $(KIND_CLUSTER)
	kind load docker-image span-helper:dev --name $(KIND_CLUSTER)

certs:
	sh hack/gen-webhook-certs.sh bin/certs

kind-demo: kind-up kind-load crds rbac certs
	$(KUBECTL) apply -f config/helper/helper.yaml
	$(KUBECTL) -n span-system rollout status deploy/span-helper --timeout=180s
	$(KUBECTL) -n span-system create secret tls webhook-server-cert \
		--cert=bin/certs/tls.crt --key=bin/certs/tls.key \
		--dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -f config/manager/manager.yaml
	$(KUBECTL) apply -f config/manager/service.yaml
	$(KUBECTL) -n span-system rollout status deploy/span --timeout=180s
	ca=$$(openssl base64 -A -in bin/certs/tls.crt); \
		sed "s|$(CA_PLACEHOLDER)|$$ca|" config/webhook/manifests.yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -f config/samples/span_v1alpha1_segment.yaml
	$(KUBECTL) wait --for=condition=Ready segment/lab --timeout=90s
	$(KUBECTL) wait --for=condition=Applied segment/lab --timeout=90s
	@if $(KUBECTL) apply -f config/samples/bad_segment.yaml; then \
		echo "bad segment was accepted"; exit 1; \
	fi
	$(KUBECTL) get segments

kind-down:
	kind delete cluster --name $(KIND_CLUSTER)
	rm -f $(KIND_KUBECONFIG)

helper-self-test: helper-image
	docker run --rm --privileged span-helper:dev self-test

flux-lab:
	sh hack/flux-lab.sh

argo-lab:
	sh hack/argo-lab.sh

kcp-lab:
	sh hack/kcp-lab.sh
