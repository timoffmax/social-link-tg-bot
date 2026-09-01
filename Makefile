# Makefile for Social Link Telegram Bot

# Go settings
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=social-link-bot

# Add Go to PATH if needed
export PATH := $(HOME)/go/bin:$(PATH)

.PHONY: all build clean test run dev install-deps help

all: build

## build: Build the application
build:
	$(GOBUILD) -o $(BINARY_NAME) -v .

## clean: Clean build files
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)

## test: Run tests
test:
	$(GOTEST) -v ./...

## run: Build and run the application
run: build
	./$(BINARY_NAME)

## dev: Run in development mode with auto-restart (requires entr)
dev:
	@echo "Starting development mode..."
	@echo "Install entr for auto-restart: sudo apt install entr"
	find . -name "*.go" | entr -r make run

## install-deps: Install development dependencies
install-deps:
	@echo "Installing yt-dlp..."
	pip3 install --user yt-dlp
	@echo "Creating symlink for local development..."
	mkdir -p bin
	ln -sf ~/.local/bin/yt-dlp bin/yt-dlp || ln -sf /usr/bin/yt-dlp bin/yt-dlp || echo "yt-dlp not found in standard locations"
	@echo "Installing ffmpeg (may require sudo)..."
	@command -v ffmpeg >/dev/null 2>&1 || { echo "Please install ffmpeg: sudo apt install ffmpeg"; }

## setup: Initial setup for local development
setup: install-deps
	@echo "Setting up local development environment..."
	@if [ ! -f .env ]; then \
		echo "Creating .env file from template..."; \
		cp .env.example .env; \
		echo "Please edit .env file with your bot token"; \
	fi
	@echo "Setup complete!"

## logs: Show recent logs (for production, reads bin/deploy.conf)
logs:
	@. ./bin/deploy.conf && ssh $${SERVER_SSH_KEY:+-i $$SERVER_SSH_KEY} $$SERVER_USER@$$SERVER_IP "tail -f $$REMOTE_DIR/logs/bot.log"

## deploy: Deploy to production
deploy:
	./bin/deploy

## status: Check production status (reads bin/deploy.conf)
status:
	@. ./bin/deploy.conf && ssh $${SERVER_SSH_KEY:+-i $$SERVER_SSH_KEY} $$SERVER_USER@$$SERVER_IP "systemctl status $$SERVICE_NAME --no-pager"

## help: Show this help
help:
	@echo "Available commands:"
	@sed -n 's/^##//p' Makefile