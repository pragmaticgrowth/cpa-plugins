PLUGIN_ID := opencode-go
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
EXT := dylib
ifeq ($(GOOS),linux)
	EXT := so
endif
ifeq ($(GOOS),windows)
	EXT := dll
endif

.PHONY: build test tidy clean
build:
	CGO_ENABLED=1 go build -buildmode=c-shared -o $(PLUGIN_ID).$(EXT) .

test:
	go test ./internal/core/...

tidy:
	go mod tidy

clean:
	rm -f $(PLUGIN_ID).dylib $(PLUGIN_ID).so $(PLUGIN_ID).dll $(PLUGIN_ID).h
