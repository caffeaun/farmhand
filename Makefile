BINARY_NAME=farmhand
BUILD_DIR=./bin
UI_DIR=./ui
UI_DIST=$(UI_DIR)/dist
EMBED_DIR=./internal/embed/ui_dist
VERSION ?= $(shell git describe --tags 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

.PHONY: build test lint ui-build embed-copy embed clean run release help

## build: Compile the Go binary for the current OS/arch
build:
	@mkdir -p $(BUILD_DIR)
	@if [ -d "$(UI_DIST)" ]; then \
		$(MAKE) embed-copy; \
	fi
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/farmhand

## ui-build: Build the Svelte dashboard
ui-build:
	cd $(UI_DIR) && pnpm build

## embed-copy: Copy ui/dist/* into internal/embed/ui_dist/ for go:embed
embed-copy:
	@if [ ! -d "$(UI_DIST)" ]; then \
		echo "Error: $(UI_DIST) does not exist. Run 'make ui-build' first."; \
		exit 1; \
	fi
	@mkdir -p $(EMBED_DIR)
	cp -r $(UI_DIST)/. $(EMBED_DIR)/

## embed: Build UI then compile Go binary with embedded assets (alias for convenience)
embed: ui-build embed-copy
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/farmhand

## release: Cross-compile for all target platforms (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64)
release: ui-build embed-copy
	@mkdir -p $(BUILD_DIR)
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64  ./cmd/farmhand
	GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64  ./cmd/farmhand
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/farmhand
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/farmhand

## test: Run all Go tests with race detector
test:
	go test ./... -race -v

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## clean: Remove build artifacts (preserves internal/embed/ui_dist/.gitkeep)
clean:
	rm -rf $(BUILD_DIR)
	@find $(EMBED_DIR) -not -name '.gitkeep' -not -path '$(EMBED_DIR)' -delete 2>/dev/null || true

## run: Run the server in dev mode
run: build
	$(BUILD_DIR)/$(BINARY_NAME) serve --dev

## help: Show this help message
help:
	@grep -E '^## ' Makefile | sed 's/## //' | column -t -s ':'
