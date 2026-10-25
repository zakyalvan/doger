#!/bin/bash
# Installs development tools for Claude Code cloud sessions.
#   - OpenSpec CLI: version matches the `generatedBy` stamp of the OpenSpec
#     skills in .claude/skills; override with OPENSPEC_VERSION.
#   - golangci-lint: the preinstalled one may be too old for .golangci.yml;
#     override with GOLANGCI_LINT_VERSION.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

OPENSPEC_VERSION="${OPENSPEC_VERSION:-1.15.0}"
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-2.15.0}"
TOOLS_BIN="$HOME/.local/bin"

if [ "$(openspec --version 2>/dev/null || true)" != "$OPENSPEC_VERSION" ]; then
  npm install -g --no-fund --no-audit "@fission-ai/openspec@${OPENSPEC_VERSION}"
fi

# GitHub release downloads are blocked by the proxy, so build from the Go
# module proxy instead of using the official install script.
mkdir -p "$TOOLS_BIN"
if ! "$TOOLS_BIN/golangci-lint" --version 2>/dev/null | grep -q "version ${GOLANGCI_LINT_VERSION} "; then
  GOBIN="$TOOLS_BIN" go install \
    "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}"
fi

case ":$PATH:" in
  *":$TOOLS_BIN:"*) ;;
  *) echo "export PATH=\"$TOOLS_BIN:\$PATH\"" >> "$CLAUDE_ENV_FILE" ;;
esac

openspec --version
"$TOOLS_BIN/golangci-lint" --version
