#!/bin/bash
# Installs development tools for Claude Code cloud sessions.
#   - Go: the image ships an older Go; installs GO_VERSION next to it and
#     repoints the /usr/local/go symlink.
#   - OpenSpec CLI: version matches the `generatedBy` stamp of the OpenSpec
#     skills in .claude/skills; override with OPENSPEC_VERSION.
#   - golangci-lint: the preinstalled one may be too old for .golangci.yml;
#     override with GOLANGCI_LINT_VERSION. Rebuilt whenever Go changes,
#     because it cannot load code newer than the Go it was built with.
#   - gopls: required by the gopls-lsp plugin; override with GOPLS_VERSION.
#     Rebuilt whenever Go changes, for the same reason.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

GO_VERSION="${GO_VERSION:-1.27.3}"
OPENSPEC_VERSION="${OPENSPEC_VERSION:-1.15.0}"
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-2.15.0}"
GOPLS_VERSION="${GOPLS_VERSION:-0.24.0}"
TOOLS_BIN="$HOME/.local/bin"

GO_ROOT="/usr/local/go${GO_VERSION}"
if [ ! -x "$GO_ROOT/bin/go" ]; then
  go_archive="go${GO_VERSION}.linux-amd64.tar.gz"
  go_sha256="$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' | python3 -I -c '
import json, sys
name = sys.argv[1]
for release in json.load(sys.stdin):
    for f in release["files"]:
        if f["filename"] == name:
            print(f["sha256"])
            sys.exit(0)
sys.exit("no checksum for " + name)
' "$go_archive")"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT
  curl -fsSL -o "$tmp_dir/$go_archive" "https://go.dev/dl/$go_archive"
  echo "$go_sha256  $tmp_dir/$go_archive" | sha256sum -c --quiet -
  mkdir -p "$tmp_dir/extract"
  tar -C "$tmp_dir/extract" -xzf "$tmp_dir/$go_archive"
  mv "$tmp_dir/extract/go" "$GO_ROOT"
fi
ln -sfn "$GO_ROOT" /usr/local/go
export GOTOOLCHAIN=local

if [ "$(openspec --version 2>/dev/null || true)" != "$OPENSPEC_VERSION" ]; then
  npm install -g --no-fund --no-audit "@fission-ai/openspec@${OPENSPEC_VERSION}"
fi

# Builds the Go tool PKG from MODULE at VERSION into TOOLS_BIN, unless the
# installed binary already matches all three and was built with GO_VERSION. Builds from the Go module
# proxy, because the proxy blocks GitHub release downloads.
install_go_tool() {
  local module="$1" pkg="$2" version="$3"
  local bin="$TOOLS_BIN/${pkg##*/}"
  local info
  info="$(go version -m "$bin" 2>/dev/null || true)"
  if [[ "$info" == "$bin: go${GO_VERSION}"$'\n'* \
    && "$info" == *$'\tpath\t'"$pkg"$'\n'* \
    && "$info" == *$'\tmod\t'"$module"$'\t'"v${version}"$'\t'* ]]; then
    return
  fi
  GOBIN="$TOOLS_BIN" go install "${pkg}@v${version}"
}

mkdir -p "$TOOLS_BIN"
install_go_tool github.com/golangci/golangci-lint/v2 \
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint "$GOLANGCI_LINT_VERSION"
install_go_tool golang.org/x/tools/gopls golang.org/x/tools/gopls "$GOPLS_VERSION"

case ":$PATH:" in
  *":$TOOLS_BIN:"*) ;;
  *) echo "export PATH=\"$TOOLS_BIN:\$PATH\"" >> "$CLAUDE_ENV_FILE" ;;
esac

go version
openspec --version
"$TOOLS_BIN/golangci-lint" --version
"$TOOLS_BIN/gopls" version
