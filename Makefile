.PHONY: build install uninstall clean help test

LOCAL_EXTENSION_DIR := local/gh-trends

help:
	@echo "RepoTrends - GitHub Repository Analytics Extension"
	@echo ""
	@echo "Available commands:"
	@echo "  make build     - Build gh-trends"
	@echo "  make install   - Install as a local gh extension"
	@echo "  make uninstall - Remove the gh extension"
	@echo "  make test      - Run all tests"
	@echo "  make clean     - Clean build artifacts"

build:
	@echo "🔨 Building RepoTrends..."
	go build -ldflags="-s -w" -o gh-trends

install: build
	@echo "📦 Installing RepoTrends as a GitHub CLI extension..."
	@mkdir -p $(LOCAL_EXTENSION_DIR)
	@cp gh-trends $(LOCAL_EXTENSION_DIR)/gh-trends
	@git -C $(LOCAL_EXTENSION_DIR) init -q
	@gh extension remove reporhythm >/dev/null 2>&1 || true
	@gh extension remove trends >/dev/null 2>&1 || true
	@cd $(LOCAL_EXTENSION_DIR) && gh extension install .
	@echo "✅ RepoTrends installed successfully!"
	@echo "🎯 You can now run: gh trends"

uninstall:
	@echo "🗑️  Removing the RepoTrends GitHub CLI extension..."
	@gh extension remove trends
	@echo "✅ RepoTrends uninstalled successfully!"

test:
	go test ./...

clean:
	@echo "🧹 Cleaning build artifacts..."
	@rm -f gh-trends
	@rm -f $(LOCAL_EXTENSION_DIR)/gh-trends
	@rm -f *.csv
	@echo "✅ Clean complete!"

dev-build:
	@echo "🔨 Building RepoTrends (development mode)..."
	go build -v -o gh-trends
