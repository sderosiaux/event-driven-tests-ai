.PHONY: build test lint fmt tidy

build:
	go build -o edt ./cmd/edt

test:
	go test ./... -race -count=1

test-integration:
	go test ./... -race -count=1 -tags=integration

lint:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy
