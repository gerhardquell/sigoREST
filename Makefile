# Makefile für sigoREST — alle Binaries landen in ./build/
# Targets:
#   make build        — alle Programme kompilieren (sigoREST, sigoE, mockprovider)
#   make sigorest     — nur REST-Server
#   make sigoe        — nur CLI
#   make mockprovider — nur Mock-Provider
#   make test         — alle Tests
#   make clean        — ./build/ entfernen

BUILDDIR := build
GOFLAGS  := -trimpath

.PHONY: all build sigorest sigoe mockprovider test clean

all: build

build: sigorest sigoe mockprovider

sigorest:
	@mkdir -p $(BUILDDIR)
	go build $(GOFLAGS) -o $(BUILDDIR)/sigoREST ./sigoREST/

sigoe:
	@mkdir -p $(BUILDDIR)
	go build $(GOFLAGS) -o $(BUILDDIR)/sigoE ./cmd/sigoE/

mockprovider:
	@mkdir -p $(BUILDDIR)
	go build $(GOFLAGS) -o $(BUILDDIR)/mockprovider ./test/cmd/mockprovider/

test:
	go test ./...

clean:
	rm -rf $(BUILDDIR)
