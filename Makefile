VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS  = -ldflags "-X github.com/MochaCosine1206/orchestra/internal/version.Version=$(VERSION) \
                      -X github.com/MochaCosine1206/orchestra/internal/version.Commit=$(COMMIT) \
                      -X github.com/MochaCosine1206/orchestra/internal/version.Date=$(DATE)"

.PHONY: build build-bridge install install-dev install-bridge test vet lint clean run release-dry release install-hooks

build:
	go build $(LDFLAGS) -o bin/orchestra ./cmd/orchestra/

build-bridge:
	go build -o bin/telegram-bridge ./cmd/telegram-bridge/

install-bridge: build-bridge
	cp bin/telegram-bridge /usr/local/bin/telegram-bridge
	@echo "Installed telegram-bridge to /usr/local/bin/"

install: build
	@echo "Run: sudo cp bin/orchestra /usr/local/bin/orchestra"
	@echo "Or:  export PATH=\"$$PWD/bin:\$$PATH\""

install-dev:
	go install $(LDFLAGS) ./cmd/orchestra/

test:
	go vet ./...
	go test ./...

vet:
	go vet ./...

lint:
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

clean:
	rm -rf bin/ dist/

run:
	go run ./cmd/orchestra/

release-dry:
	goreleaser release --snapshot --clean

release:
	goreleaser release --clean

install-hooks:
	cp scripts/pre-push .git/hooks/pre-push
	chmod +x .git/hooks/pre-push
	@echo "Installed pre-push hook"

# --- Frontend (Tauri + React) ---

.PHONY: fe-build fe-dev fe-test fe-lint fe-clean

fe-build:
	npm run build

fe-dev:
	npm run dev

fe-test:
	npx vitest run

fe-lint:
	npx biome check src/

fe-clean:
	rm -rf dist node_modules
