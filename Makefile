.PHONY: test vet release

SHELL = /bin/bash -o pipefail

test:
	go test -trimpath -race ./...

vet:
	go vet -trimpath ./...
	staticcheck ./...

version ?= minor

release: test
	go run github.com/kevinburke/bump_version@latest --tag-prefix=v $(version) lib/lib.go
