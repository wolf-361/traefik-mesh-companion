.DEFAULT_GOAL := help
.PHONY: all help init tidy fmt lint test build run clean ci-local

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

all: tidy fmt lint test build ## Run tidy, formatting, linting, tests, and build

init: ## Initialize the project (setup git hooks)
	@echo "=> Configuring git hooks..."
	@git config core.hooksPath .githooks || echo "No .githooks directory found"
	@echo "=> Git hooks configured successfully!"

tidy: ## Clean up and verify Go modules
	@echo "=> Running go mod tidy..."
	@go mod tidy

fmt: ## Format the Go code
	@echo "=> Formatting code..."
	@go fmt ./...

lint: ## Run the golangci-lint linter
	@echo "=> Running golangci-lint..."
	@golangci-lint run ./...

test: ## Run all tests with coverage and race detector
	@echo "=> Running tests..."
	@go test -v -race -cover ./...

build: ## Build a static production binary locally to bin/companion
	@echo "=> Building static binary..."
	@CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/companion cmd/companion/main.go

run: build ## Build and run the binary locally
	@echo "=> Running companion locally..."
	@./bin/companion

clean: ## Clean up the bin/ directory
	@echo "=> Cleaning..."
	@rm -rf bin/

ci-local: ## Run the GitHub Actions CI pipeline locally using act
	@echo "=> Running CI locally with act..."
	@act pull_request -W .github/workflows/ci.yml --container-architecture linux/amd64