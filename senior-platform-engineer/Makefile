.PHONY: build test test-integration verify

build:
	go build -o /tmp/cjpod-manager ./cmd/manager

test:
	go test ./...

test-integration:
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use -p path)" go test -tags=integration ./...

verify:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')
	go vet ./...
	go test ./...
	go test -race ./...
	go build -o /tmp/cjpod-manager ./cmd/manager
