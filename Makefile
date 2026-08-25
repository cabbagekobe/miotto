# Single entry point for verification. Humans, Claude, lefthook, and CI all use the same bar.
.PHONY: fmt fmt-check lint typecheck test audit check fix

fmt: ; gofmt -w .
fmt-check: ; test -z "$$(gofmt -l .)"
lint: ; golangci-lint run
typecheck: ; go vet ./...
test: ; go test -race -cover ./...
audit: ; govulncheck ./...
check: fmt-check lint typecheck test
fix: fmt
