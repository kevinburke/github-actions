.PHONY: test release

SHELL = /bin/bash -o pipefail

test:
	go test -trimpath ./...

release: test
	go run github.com/kevinburke/bump_version@latest --tag-prefix=v minor lib/lib.go
