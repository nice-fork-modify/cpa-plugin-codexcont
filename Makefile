PLUGIN_ID := codexcont
DIST_DIR ?= dist
PLUGIN_DIR ?= plugins
GO ?= go

HOST_GOOS := $(shell $(GO) env GOOS)
HOST_GOARCH := $(shell $(GO) env GOARCH)

GOOS ?= $(HOST_GOOS)
GOARCH ?= $(HOST_GOARCH)

ifeq ($(GOOS),darwin)
PLUGIN_EXT := dylib
else ifeq ($(GOOS),windows)
PLUGIN_EXT := dll
else
PLUGIN_EXT := so
endif

NATIVE_OUTPUT := $(DIST_DIR)/$(GOOS)/$(GOARCH)/$(PLUGIN_ID).$(PLUGIN_EXT)
TREE_OUTPUT := $(PLUGIN_DIR)/$(GOOS)/$(GOARCH)/$(PLUGIN_ID).$(PLUGIN_EXT)

.PHONY: test build build-native build-plugin-tree build-linux-arm64-container build-linux-amd64-container clean

test:
	$(GO) test ./...

build: build-native

build-native: $(NATIVE_OUTPUT)

build-plugin-tree: $(TREE_OUTPUT)

$(NATIVE_OUTPUT): *.go go.mod go.sum
	mkdir -p $(dir $@)
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -buildmode=c-shared -o $@ .
	rm -f $(basename $@).h

$(TREE_OUTPUT): *.go go.mod go.sum
	mkdir -p $(dir $@)
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -buildmode=c-shared -o $@ .
	rm -f $(basename $@).h

build-linux-arm64-container:
	./scripts/build-linux-container.sh arm64

build-linux-amd64-container:
	./scripts/build-linux-container.sh amd64

clean:
	rm -rf $(DIST_DIR) $(PLUGIN_DIR)
