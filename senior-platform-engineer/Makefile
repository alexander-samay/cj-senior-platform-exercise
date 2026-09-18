.PHONY: build generate-crd test test-integration verify

build:
	go build -o /tmp/cjpod-manager ./cmd/manager

generate-crd:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0 crd paths=./... output:crd:artifacts:config=config/crd
	mv config/crd/interview.cj.dev_cjpods.yaml config/crd/cjpods.yaml

test:
	go test ./...

test-integration:
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20260119125712-e8e550fd8915 use -p path)" go test -tags=integration ./...

verify:
	$(MAKE) generate-crd
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')
	go vet ./...
	go test ./...
	go test -race ./...
	go build -o /tmp/cjpod-manager ./cmd/manager
