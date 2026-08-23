# Thin wrapper around `go` and `cmd/rgit-install` -- it delegates, it does
# not reimplement the build. CONTRIBUTING.md is the authority on what each
# test lane covers and why -coverpkg=./... is mandatory; this just runs the
# commands it documents.

BINARY        := rgit
DIST          := dist
SQL_CSRC      := internal/resolve/sqlgrammar/csrc
VERSION       := $(shell git describe --tags --always --dirty 2>/dev/null)
# Cross artifact filenames need a non-empty component even when VERSION comes
# back blank (git missing, or the checkout has no commits yet) -- "dev"
# matches main.go's own fallback constant so a filename never goes blank.
CROSS_VERSION := $(if $(VERSION),$(VERSION),dev)

LDFLAGS := -s -w
ifneq ($(VERSION),)
LDFLAGS += -X main.version=$(VERSION)
endif

ZIG ?= zig

# Lazily evaluated, so it re-checks after the sql-parser target below has
# had its chance to generate. A host without the tree-sitter CLI simply
# leaves csrc/ absent and every cross target builds without SQL, the same
# fallback `make install` already promises.
SQL_TAGS = $(if $(wildcard $(SQL_CSRC)/parser.c),-tags rgit_sql,)

.DEFAULT_GOAL := help

.PHONY: help build install test test-short test-race cover cover-short \
        fix-diff fix lint clean sql-parser cross cross-linux-amd64 \
        cross-linux-arm64 cross-windows-amd64 cross-darwin \
        cross-darwin-amd64 cross-darwin-arm64

help:
	@echo "rgit build targets:"
	@echo "  build              build ./rgit for the host"
	@echo "  install            build and install via cmd/rgit-install (PREFIX=dir to override)"
	@echo "  test               go test ./...            (full suite, builds and execs the binary)"
	@echo "  test-short         go test -short ./...      (unit lane -- the one a regression must fail)"
	@echo "  test-race          go test -race ./...       (full tree -- CI's own race job scopes to internal/lsp: jsonrpc2, the LSP spawn lock)"
	@echo "  cover              coverage for the full suite, -coverpkg=./... as CONTRIBUTING.md requires"
	@echo "  cover-short        coverage for the -short lane"
	@echo "  fix-diff           go fix -diff ./...        (preview; read before applying)"
	@echo "  fix                go fix ./... twice        (fixes can unlock fixes)"
	@echo "  lint               golangci-lint run ./...   (.golangci.yml)"
	@echo "  cross              cross-compile linux/amd64, linux/arm64, windows/amd64 into dist/, versioned, with SQL when generatable, plus SHA256SUMS"
	@echo "  cross-darwin       native-compile darwin/amd64 and darwin/arm64 into dist/ -- run this ON a macOS host, not cross-compiled by cross above"
	@echo "  clean              remove build outputs, including a generated SQL parser tree"

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/rgit

install:
	go run ./cmd/rgit-install $(if $(PREFIX),-prefix $(PREFIX))

test:
	go test ./...

test-short:
	go test -short ./...

test-race:
	go test -race ./...

# -coverpkg=./... is mandatory: much of the suite drives code from another
# package, so without it internal/app, internal/cli, internal/resolve, and
# internal/synth report 0.0% while being covered heavily in fact.
cover:
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

cover-short:
	go test -short -coverpkg=./... -coverprofile=short.out ./...
	go tool cover -func=short.out | tail -1

fix-diff:
	go fix -diff ./...

fix:
	go fix ./...
	go fix ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "lint needs golangci-lint on PATH (https://golangci-lint.run/welcome/install/)"; \
		exit 1; \
	}
	golangci-lint run --allow-parallel-runners ./...

clean:
	rm -f $(BINARY) rgit-install coverage.out short.out
	rm -rf $(DIST) $(SQL_CSRC)

# rgit links tree-sitter through cgo, so CGO_ENABLED=0 is not an option and
# every cross target needs a matching C toolchain. Measured on this repo:
# zig cc covers linux/amd64, linux/arm64, and windows/amd64 from a Linux
# host. darwin/amd64 and darwin/arm64 are deliberately not in this matrix --
# they need a macOS SDK zig cannot supply (net's use in go.lsp.dev/jsonrpc2
# pulls in `resolv`/CoreFoundation at link time); see docs/INSTALL.md.
#
# Cross binaries carry SQL when the build host can generate the parser.
# Generation is host-independent -- it emits C once, from grammar.js -- so
# the per-target cost is only compiling that C, measured at ~6-7s per target
# with zig cc. The sql-parser prerequisite below delegates generation to
# cmd/rgit-install, keeping -tags rgit_sql and generation in one place
# (AGENTS.md § Delegation boundary). Without the tree-sitter CLI, SQL_TAGS
# is empty and every target builds SQL-less rather than failing.
#
# Filenames carry CROSS_VERSION so a second `make cross` at a different tag
# cannot silently overwrite the previous run's artifacts, and SHA256SUMS
# below is regenerated per run (not appended across runs) so it always
# describes exactly what dist/ holds right now, not a mix of tags -- these
# artifacts are produced one full run at a time.
# Delegated rather than inlined: `tree-sitter generate` is invoked in
# exactly one place in this repo, and that place is cmd/rgit-install.
sql-parser:
	go run ./cmd/rgit-install -generate-only

cross: sql-parser cross-linux-amd64 cross-linux-arm64 cross-windows-amd64
	$(need-sha256sum)
	cd $(DIST) && sha256sum \
		rgit-$(CROSS_VERSION)-linux-amd64 \
		rgit-$(CROSS_VERSION)-linux-arm64 \
		rgit-$(CROSS_VERSION)-windows-amd64.exe \
		> SHA256SUMS
	@echo "wrote $(DIST)/SHA256SUMS"

define need-zig
	@command -v $(ZIG) >/dev/null 2>&1 || { \
		echo "$@ needs zig on PATH (https://ziglang.org/download) -- not building without a matching C toolchain"; \
		exit 1; \
	}
endef

define need-sha256sum
	@command -v sha256sum >/dev/null 2>&1 || { \
		echo "cross needs sha256sum on PATH -- not writing $(DIST)/SHA256SUMS without it"; \
		exit 1; \
	}
endef

cross-linux-amd64:
	$(need-zig)
	mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC="$(ZIG) cc -target x86_64-linux-gnu" \
		go build $(SQL_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/rgit-$(CROSS_VERSION)-linux-amd64 ./cmd/rgit

cross-linux-arm64:
	$(need-zig)
	mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC="$(ZIG) cc -target aarch64-linux-gnu" \
		go build $(SQL_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/rgit-$(CROSS_VERSION)-linux-arm64 ./cmd/rgit

cross-windows-amd64:
	$(need-zig)
	mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC="$(ZIG) cc -target x86_64-windows-gnu" \
		go build $(SQL_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/rgit-$(CROSS_VERSION)-windows-amd64.exe ./cmd/rgit

# darwin is deliberately not part of `cross` above (zig cannot supply a
# macOS SDK -- docs/INSTALL.md § Cross builds). These two targets are the
# native-compile counterpart: no CC override and no zig prerequisite,
# because a real macOS host's own clang and SDK already handle both its
# native arch and the other darwin arch (Xcode ships a universal SDK, the
# same reason a Mac can build for either Apple Silicon or Intel without a
# second toolchain) -- only useful run ON a macOS host, hence the separate
# target rather than folding these into `cross`'s own linux/windows matrix.
cross-darwin-amd64:
	mkdir -p $(DIST)
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 \
		go build $(SQL_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/rgit-$(CROSS_VERSION)-darwin-amd64 ./cmd/rgit

cross-darwin-arm64:
	mkdir -p $(DIST)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 \
		go build $(SQL_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/rgit-$(CROSS_VERSION)-darwin-arm64 ./cmd/rgit

cross-darwin: sql-parser cross-darwin-amd64 cross-darwin-arm64
