#!/bin/bash
# Installs the OpenSpec CLI for Claude Code cloud sessions.
# The version matches the `generatedBy` stamp of the OpenSpec skills in
# .claude/skills; override with OPENSPEC_VERSION.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

OPENSPEC_VERSION="${OPENSPEC_VERSION:-1.15.0}"

if command -v openspec >/dev/null 2>&1 \
  && [ "$(openspec --version 2>/dev/null)" = "$OPENSPEC_VERSION" ]; then
  exit 0
fi

npm install -g --no-fund --no-audit "@fission-ai/openspec@${OPENSPEC_VERSION}"
openspec --version
