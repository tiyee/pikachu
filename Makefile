.PHONY: test test-unit test-race test-coverage test-benchmark build clean lint help

# Default target
all: build test

# Build the application
build:
	@echo "🔨 Building pikachu..."
	go build -o pikachu .

# Run all tests
test: test-unit

# Run unit tests
test-unit:
	@echo "🧪 Running unit tests..."
	go test ./tests/ -v

# Run tests with race detector
test-race:
	@echo "🏃 Running tests with race detector..."
	go test ./tests/ -race -v

# Run tests with coverage
test-coverage:
	@echo "📊 Running tests with coverage..."
	go test ./tests/ -cover -coverprofile=coverage.out -v
	@echo "Coverage report generated: coverage.out"
	@echo "Run 'go tool cover -html=coverage.out' to view HTML report"

# Run benchmark tests
test-benchmark:
	@echo "⚡ Running benchmark tests..."
	go test ./tests/ -bench=. -benchmem -v

# Run comprehensive test suite
test-all: test-unit test-race test-coverage test-benchmark
	@echo "🎉 All tests completed!"

# Run linter
lint:
	@echo "🔍 Running linter..."
	golangci-lint run ./...

# Format code
fmt:
	@echo "📝 Formatting code..."
	go fmt ./...

# Run go vet
vet:
	@echo "🔎 Running go vet..."
	go vet ./...

# Clean build artifacts
clean:
	@echo "🧹 Cleaning up..."
	rm -f pikachu
	rm -f coverage.out
	rm -f coverage.html
	rm -rf test-results/

# Install dependencies
deps:
	@echo "📦 Installing dependencies..."
	go mod download
	go mod tidy

# Run development checks (format, vet, test)
dev-checks: fmt vet test-unit
	@echo "✅ Development checks passed!"

# Run production checks (lint, test-race, test-coverage)
prod-checks: lint test-race test-coverage
	@echo "✅ Production checks passed!"

# Watch for changes and run tests (requires entr)
watch:
	@echo "👀 Watching for changes..."
	find . -name "*.go" | entr -r go test ./tests/ -v

# Show help
help:
	@echo "📚 Available targets:"
	@echo "  build          - Build the application"
	@echo "  test           - Run unit tests"
	@echo "  test-unit      - Run unit tests"
	@echo "  test-race      - Run tests with race detector"
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  test-benchmark - Run benchmark tests"
	@echo "  test-all       - Run comprehensive test suite"
	@echo "  lint           - Run linter"
	@echo "  fmt            - Format code"
	@echo "  vet            - Run go vet"
	@echo "  clean          - Clean build artifacts"
	@echo "  deps           - Install dependencies"
	@echo "  dev-checks     - Run development checks"
	@echo "  prod-checks    - Run production checks"
	@echo "  watch          - Watch for changes and run tests"
	@echo "  help           - Show this help message"

# CI/CD pipeline target
ci: deps fmt vet lint test-race test-coverage
	@echo "✅ CI pipeline completed successfully!"