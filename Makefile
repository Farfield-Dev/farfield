.PHONY: build check fmt test vet

build:
	go build ./cmd/farfield

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test ./...

vet:
	go vet ./...

check:
	test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))"
	go vet ./...
	go test -race ./...
