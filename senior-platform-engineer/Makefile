.PHONY: build generate-crd test test-integration verify verify-crd verify-format

ENVTEST_VERSION := 1.34.1

build:
	go build -o /tmp/cjpod-manager ./cmd/manager

generate-crd:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0 crd paths=./... output:crd:artifacts:config=config/crd
	mv config/crd/interview.cj.dev_cjpods.yaml config/crd/cjpods.yaml

test:
	go test ./...

test-integration:
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20260119125712-e8e550fd8915 use $(ENVTEST_VERSION) -p path)" go test -tags=integration ./...

verify-crd:
	@tmp_dir="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp_dir"' EXIT; \
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0 crd paths=./... output:crd:artifacts:config="$$tmp_dir"; \
	cmp "$$tmp_dir/interview.cj.dev_cjpods.yaml" config/crd/cjpods.yaml

verify-format:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || \
		{ echo 'Go files are not formatted; run gofmt.'; gofmt -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1; }

verify: verify-crd verify-format
	go vet ./...
	go test ./...
	go test -race ./...
	go build -o /tmp/cjpod-manager ./cmd/manager
	git diff --exit-code
