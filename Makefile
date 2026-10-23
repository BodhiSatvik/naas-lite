GO    ?= go
IMAGE ?= naas-lite:dev
KIND  ?= factory
KCTX  ?= kind-$(KIND)
NS    ?= naas-lite

.PHONY: help run test vet fmt fmt-check k8s-validate verify factory kind-up smoke clean

help:
	@echo "make verify      the factory's gate: gofmt + go vet + go test + manifest dry-run"
	@echo "make factory     build the factory controller into bin/factory"
	@echo "make run         run naas-lite on :8080"
	@echo "make kind-up     create the local kind cluster used by the dry-run and smoke test"
	@echo "make smoke       build the image, load it into kind, deploy, wait for rollout"

run:
	$(GO) run ./cmd/naas-lite

test:
	$(GO) test -count=1 ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal

fmt-check:
	@out="$$(gofmt -l cmd internal)"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi; \
	echo "gofmt: ok"

# Server-side dry-run of the manifests, against the LOCAL kind cluster only.
# It never uses your current kubectl context, so it cannot touch a real cluster.
k8s-validate:
	@if command -v kubectl >/dev/null 2>&1 \
	  && kubectl config get-contexts $(KCTX) >/dev/null 2>&1 \
	  && kubectl --context $(KCTX) cluster-info --request-timeout=5s >/dev/null 2>&1; then \
		kubectl --context $(KCTX) apply --dry-run=server -f deploy/k8s/ && echo "k8s-validate: ok ($(KCTX))"; \
	else \
		echo "k8s-validate: SKIPPED (no reachable $(KCTX) context; run 'make kind-up' to turn it on)"; \
	fi

# The gate the factory's verify station runs. Agents cannot edit this file.
verify: fmt-check vet test k8s-validate
	@echo "VERIFY: PASS"

factory:
	cd factory && $(GO) build -o ../bin/factory .
	@echo "built bin/factory"

kind-up:
	@kind get clusters 2>/dev/null | grep -qx $(KIND) || kind create cluster --name $(KIND)
	@kubectl --context $(KCTX) get nodes

smoke:
	docker build -t $(IMAGE) .
	kind load docker-image $(IMAGE) --name $(KIND)
	kubectl --context $(KCTX) create namespace $(NS) --dry-run=client -o yaml | kubectl --context $(KCTX) apply -f -
	kubectl --context $(KCTX) -n $(NS) apply -f deploy/k8s/
	kubectl --context $(KCTX) -n $(NS) rollout restart deploy/naas-lite
	kubectl --context $(KCTX) -n $(NS) rollout status deploy/naas-lite --timeout=120s
	@echo "SMOKE: PASS"

clean:
	rm -rf bin
