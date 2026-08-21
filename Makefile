.PHONY: build check fmt test ui ui-check vet

build:
	go build ./cmd/farfield

ui:
	npm run build --prefix server/ui

ui-check:
	npm run check --prefix server/ui
	npm test --prefix server/ui

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
