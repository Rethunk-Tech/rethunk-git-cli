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
# had its chance to generate. sql-parser itself fails when generation
# cannot run; this still lets a caller who already has parser.c skip a
# second generate.
SQL_TAGS = $(if $(wildcard $(SQL_CSRC)/parser.c),-tags rgit_sql,)

.DEFAULT_GOAL := help

.PHONY: help build install test test-short test-race cover cover-short \
        xcheck fix-diff fix lint clean sql-parser cross cross-linux-amd64 \
        cross-linux-arm64 cross-windows-amd64 completions-man

help:
	@echo "rgit build targets:"
	@echo "  build              build ./rgit for the host"
	@echo "  install            build and install via cmd/rgit-install (PREFIX=dir to override)"
	@echo "  test               go test ./...            (full suite, builds and execs the binary)"
	@echo "  test-short         go test -short ./...      (unit lane -- the one a regression must fail)"
	@echo "  xcheck             measure every grammar against its language server (needs all servers)"
	@echo "  test-race          go test -race ./...       (full tree -- CI's own race job is go test -race -coverpkg=./... ./...)"
	@echo "  cover              coverage for the full suite, -coverpkg=./... as CONTRIBUTING.md requires"
	@echo "  cover-short        coverage for the -short lane"
	@echo "  fix-diff           go fix -diff ./...        (preview; read before applying)"
	@echo "  fix                go fix ./... twice        (fixes can unlock fixes)"
	@echo "  lint               golangci-lint run ./...   (.golangci.yml)"
	@echo "  cross              cross-compile linux/amd64, linux/arm64, windows/amd64 into dist/, versioned, with SQL when generatable, plus SHA256SUMS and rgit-completions-man.tar.gz"
	@echo "  completions-man    generate dist/rgit-completions-man.tar.gz from this binary's completion and man commands"
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

# XCHECK_CORPUS is every wired grammar in one list: fixtures for the
# languages this repo does not otherwise contain, plus real files of its own
# for the ones it does. The fixtures carry the shapes that actually broke --
# a grouped CSS selector written across lines, a multi-line Python binding --
# so a regression in either is a failing run rather than a silent drift.
XCHECK_CORPUS ?= \
	internal/resolve/testdata/xcheck/styles.css \
	internal/resolve/testdata/xcheck/config.py \
	internal/resolve/testdata/xcheck/lib.rs \
	internal/resolve/testdata/xcheck/widget.ts \
	internal/resolve/testdata/xcheck/panel.tsx \
	internal/resolve/testdata/xcheck/page.html \
	internal/resolve/testdata/xcheck/manifest.json \
	internal/resolve/testdata/xcheck/settings.jsonc \
	internal/resolve/testdata/xcheck/theme.scss \
	internal/resolve/testdata/xcheck/prompt.zsh \
	internal/app/show.go \
	AGENTS.md \
	.github/workflows/ci.yml \
	scripts/install.sh

# Measures every wired grammar against its own language server. Needs all of
# them installed (docs/INSTALL.md § Language servers); -strict makes an
# unreachable server a failure rather than a quietly smaller table, because a
# run that silently skipped a language measures nothing about it.
xcheck:
	go run -tags rgit_xcheck ./cmd/xcheck -strict -root $(CURDIR) $(XCHECK_CORPUS)

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
# (AGENTS.md § Delegation boundary). Without the tree-sitter CLI,
# -generate-only exits non-zero, so `make cross` writes no dist/ binary.
# `make install` still falls back to an SQL-less host build.
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

cross: sql-parser cross-linux-amd64 cross-linux-arm64 cross-windows-amd64 completions-man
	$(need-sha256sum)
	cd $(DIST) && sha256sum \
		rgit-$(CROSS_VERSION)-linux-amd64 \
		rgit-$(CROSS_VERSION)-linux-arm64 \
		rgit-$(CROSS_VERSION)-windows-amd64.exe \
		rgit-completions-man.tar.gz \
		> SHA256SUMS
	@echo "wrote $(DIST)/SHA256SUMS"

# Host binary: completions and the man page are arch-independent text
# generated from this CLI's own --help, not from the cross-compiled
# artifacts (those need zig and cannot run here for every matrix target).
completions-man:
	@set -eu; \
	assets=$(DIST)/completions-man; \
	bin=$$assets/rgit-gen; \
	rm -rf "$$assets"; \
	mkdir -p "$$assets"; \
	go build -ldflags "$(LDFLAGS)" -o "$$bin" ./cmd/rgit; \
	"$$bin" completion bash > "$$assets/rgit.bash"; \
	"$$bin" completion zsh > "$$assets/_rgit"; \
	"$$bin" completion fish > "$$assets/rgit.fish"; \
	"$$bin" completion pwsh > "$$assets/rgit.ps1"; \
	"$$bin" man > "$$assets/rgit.1"; \
	rm -f "$$bin"; \
	for f in rgit.bash _rgit rgit.fish rgit.ps1 rgit.1; do \
		test -s "$$assets/$$f" || { echo "completions-man: empty $$f"; exit 1; }; \
	done; \
	tar -czf $(DIST)/rgit-completions-man.tar.gz -C "$$assets" rgit.bash _rgit rgit.fish rgit.ps1 rgit.1; \
	rm -rf "$$assets"; \
	echo "wrote $(DIST)/rgit-completions-man.tar.gz"

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

# -o sql-parser (CI's cache-hit path) skips generation. Without this
# check a restored empty csrc/ would compile SQL-less dist/ binaries.
define need-sql-parser
	@test -f $(SQL_CSRC)/parser.c || { \
		echo "$@ needs $(SQL_CSRC)/parser.c -- run make sql-parser (tree-sitter CLI); not writing a SQL-less dist/ binary"; \
		exit 1; \
	}
endef

cross-linux-amd64: sql-parser
	$(need-zig)
	$(need-sql-parser)
	mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC="$(ZIG) cc -target x86_64-linux-gnu" \
		go build $(SQL_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/rgit-$(CROSS_VERSION)-linux-amd64 ./cmd/rgit

cross-linux-arm64: sql-parser
	$(need-zig)
	$(need-sql-parser)
	mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC="$(ZIG) cc -target aarch64-linux-gnu" \
		go build $(SQL_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/rgit-$(CROSS_VERSION)-linux-arm64 ./cmd/rgit

cross-windows-amd64: sql-parser
	$(need-zig)
	$(need-sql-parser)
	mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC="$(ZIG) cc -target x86_64-windows-gnu" \
		go build $(SQL_TAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/rgit-$(CROSS_VERSION)-windows-amd64.exe ./cmd/rgit
