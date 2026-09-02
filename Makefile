# Local development Makefile for claude-window-anchor.
# Native builds only (use build.sh for cross-compilation).

PLUGIN_ID := claude-window-anchor
GO ?= go
GOOS ?= $(shell $(GO) env GOOS)
GOARCH ?= $(shell $(GO) env GOARCH)

ifeq ($(GOOS),windows)
EXT := .dll
else ifeq ($(GOOS),darwin)
EXT := .dylib
else
EXT := .so
endif

.PHONY: build test fmt vet clean

build:
	CGO_ENABLED=1 $(GO) build -buildvcs=false -buildmode=c-shared -o $(PLUGIN_ID)$(EXT) .
	rm -f $(PLUGIN_ID).h

test:
	$(GO) test -buildvcs=false -count=1 ./...

fmt:
	gofmt -w .

vet:
	$(GO) vet -buildvcs=false ./...

clean:
	rm -f $(PLUGIN_ID)$(EXT) $(PLUGIN_ID).h
	rm -rf dist
