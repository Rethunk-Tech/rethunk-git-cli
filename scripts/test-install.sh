#!/bin/sh
# Dry-run and mocked-network checks for scripts/install.sh. CI (install-script job) and .gate.toml both run this.
set -eu
cd "$(dirname "$0")/.."

VERSION=v1.2.0 sh scripts/install.sh --dry-run >/dev/null

# Show cosign plan in dry-run
(
  mock_bin="$(mktemp -d)"
  trap 'rm -rf "$mock_bin"' EXIT
  printf '%s\n' \
    '#!/bin/sh' \
    'exit 0' >"$mock_bin/cosign"
  chmod +x "$mock_bin/cosign"
  output="$(
    PATH="$mock_bin:/usr/bin:/bin" VERSION=v1.2.0 \
      sh scripts/install.sh --dry-run
  )"
  printf '%s\n' "$output"
  printf '%s\n' "$output" | grep -Fq 'cosign/Sigstore bundle' || {
    printf '%s\n' 'missing cosign/Sigstore dry-run plan' >&2
    exit 1
  }
)

# Refuse Darwin without network
(
  mock_bin="$(mktemp -d)"
  trap 'rm -rf "$mock_bin"' EXIT
  printf '%s\n' \
    '#!/bin/sh' \
    "case \"\$1\" in" \
    '  -s) printf "%s\n" Darwin ;;' \
    '  -m) printf "%s\n" arm64 ;;' \
    'esac' >"$mock_bin/uname"
  chmod +x "$mock_bin/uname"
  if output="$(
    PATH="$mock_bin:$PATH" VERSION=v1.2.0 sh scripts/install.sh --dry-run 2>&1
  )"; then
    printf '%s\n' "$output"
    printf '%s\n' 'install.sh accepted Darwin' >&2
    exit 1
  fi
  printf '%s\n' "$output"
  printf '%s\n' "$output" | grep -Fq 'no macOS binaries are published' || {
    printf '%s\n' 'missing Darwin refusal message' >&2
    exit 1
  }
)

# Verify optional cosign branch without network
(
  fixture="$(mktemp -d)"
  mock_bin="$(mktemp -d)"
  no_cosign_prefix="$(mktemp -d)"
  cosign_prefix="$(mktemp -d)"
  cosign_marker="$(mktemp)"
  trap 'rm -rf "$fixture" "$mock_bin" "$no_cosign_prefix" "$cosign_prefix" "$cosign_marker"' EXIT
  asset="rgit-v1.2.0-linux-amd64"
  printf '%s\n' \
    '#!/bin/sh' \
    'printf "%s\n" "rgit v1.2.0"' >"$fixture/$asset"
  chmod +x "$fixture/$asset"
  asset_digest="$(sha256sum "$fixture/$asset" | cut -d' ' -f1)"
  printf '%s  %s\n' "$asset_digest" "$asset" >"$fixture/SHA256SUMS"
  printf '%s\n' '{"mock":true}' >"$fixture/SHA256SUMS.sigstore.json"
  export COSIGN_FIXTURE="$fixture"
  export COSIGN_MARKER="$cosign_marker"
  printf '%s\n' \
    '#!/bin/sh' \
    'set -eu' \
    'out=' \
    'url=' \
    "while [ \"\$#\" -gt 0 ]; do" \
    "  case \"\$1\" in" \
    "    -o) out=\$2; shift 2 ;;" \
    '    -*) shift ;;' \
    "    *) url=\$1; shift ;;" \
    '  esac' \
    'done' \
    "case \"\$url\" in" \
    "  */SHA256SUMS.sigstore.json) cp \"\$COSIGN_FIXTURE/SHA256SUMS.sigstore.json\" \"\$out\" ;;" \
    "  */SHA256SUMS) cp \"\$COSIGN_FIXTURE/SHA256SUMS\" \"\$out\" ;;" \
    "  */rgit-v1.2.0-linux-amd64) cp \"\$COSIGN_FIXTURE/rgit-v1.2.0-linux-amd64\" \"\$out\" ;;" \
    '  *) exit 1 ;;' \
    'esac' >"$mock_bin/curl"
  chmod +x "$mock_bin/curl"
  PATH="$mock_bin:/usr/bin:/bin" \
    PREFIX="$no_cosign_prefix" VERSION=v1.2.0 \
    sh scripts/install.sh
  test ! -s "$cosign_marker"
  printf '%s\n' \
    '#!/bin/sh' \
    'set -eu' \
    "[ \"\${1:-}\" = verify-blob ]" \
    'shift' \
    'bundle=' \
    'identity=' \
    'issuer=' \
    'checksum=' \
    "while [ \"\$#\" -gt 0 ]; do" \
    "  case \"\$1\" in" \
    "    --bundle) bundle=\$2; shift 2 ;;" \
    "    --certificate-identity-regexp) identity=\$2; shift 2 ;;" \
    "    --certificate-oidc-issuer) issuer=\$2; shift 2 ;;" \
    "    *) checksum=\$1; shift ;;" \
    '  esac' \
    'done' \
    "[ \"\$bundle\" = SHA256SUMS.sigstore.json ]" \
    "[ \"\$checksum\" = SHA256SUMS ]" \
    "[ \"\$identity\" = \"https://github.com/Rethunk-Tech/rethunk-git-cli/.github/workflows/release.yml@.*\" ]" \
    "[ \"\$issuer\" = \"https://token.actions.githubusercontent.com\" ]" \
    "test -s \"\$bundle\"" \
    "test -s \"\$checksum\"" \
    "printf \"%s\n\" verified >\"\$COSIGN_MARKER\"" >"$mock_bin/cosign"
  chmod +x "$mock_bin/cosign"
  PATH="$mock_bin:/usr/bin:/bin" \
    PREFIX="$cosign_prefix" VERSION=v1.2.0 \
    sh scripts/install.sh
  grep -Fqx verified "$cosign_marker"
)
