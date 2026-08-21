.PHONY: build check fmt test ui ui-check vet

build:
	go build ./cmd/farfield

ui:
	pnpm --dir server/ui build

ui-check:
	pnpm --dir server/ui check
	pnpm --dir server/ui test

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
