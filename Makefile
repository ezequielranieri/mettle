.PHONY: build test vet lint clean run dry-run dashboard export

# Build
build:
	go build -o bin/mettle ./cmd/mettle

# Test
test:
	go test ./...

# Vet
vet:
	go vet ./...

# Lint (requires golangci-lint: https://golangci-lint.run/usage/install/)
lint:
	golangci-lint run

# Clean
clean:
	rm -rf bin/ *.db *.db-wal *.db-shm traces/

# Run with demo agent
run:
	go run ./cmd/mettle run --spec $(SPEC) --agent demo

# Dry run (cost forecast)
dry-run:
	go run ./cmd/mettle run --spec $(SPEC) --dry-run

# Dashboard
dashboard:
	go run ./cmd/mettle dashboard --output dashboard.html

# Export
export:
	go run ./cmd/mettle export --platform $(PLATFORM) --endpoint $(ENDPOINT)

# All checks
check: vet test
	@echo "All checks passed"
