.DEFAULT_GOAL := help
.PHONY: all help fmt lint test build clean

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

all: fmt lint test build ## Run formatting, linting, tests, and build

fmt: ## Format the Go code using go fmt
	@echo "=> Formatting code..."
	@go fmt ./...

lint: ## Run the golangci-lint linter
	@echo "=> Running golangci-lint..."
	@golangci-lint run ./...

test: ## Run all tests with coverage
	@echo "=> Running tests..."
	@go test -v -cover ./...

build: ## Build the binary locally to bin/companion
	@echo "=> Building binary..."
	@go build -o bin/companion cmd/companion/main.go

clean: ## Clean up the bin/ directory
	@echo "=> Cleaning..."
	@rm -rf bin/