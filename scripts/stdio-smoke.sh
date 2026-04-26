#!/usr/bin/env bash
# Pipe a single MCP `tools/list` request into the binary (or Docker image) on
# stdin and assert a well-formed JSON-RPC response on stdout. Used by both
# the binary and Docker stdio-smoke CI gates.
#
# Usage:
#   stdio-smoke.sh binary <path-to-binary>
#   stdio-smoke.sh docker <image-ref>

set -euo pipefail

mode="${1:-}"
target="${2:-}"

if [ -z "$mode" ] || [ -z "$target" ]; then
  echo "usage: $0 (binary|docker) <path-or-image>" >&2
  exit 2
fi

# The smoke does not exercise the auth probe — we want to verify stdio framing
# regardless of token validity. The binary supports --skip-probe for this case.
# A bad token still exercises the protocol layer.
export PIPEDRIVE_API_TOKEN="${PIPEDRIVE_API_TOKEN:-smoke-token}"
export PIPEDRIVE_COMPANY_DOMAIN="${PIPEDRIVE_COMPANY_DOMAIN:-smoke}"

# Initialize + tools/list. Initialize is required by the MCP handshake.
# After sending both requests we briefly keep stdin open so the SDK has
# time to flush responses before it sees EOF — without the trailing
# sleep the server can race the reader and exit before any output lands
# on stdout.
request_init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
request_list='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'

feed() {
  printf '%s\n' "$request_init" "$request_list"
  sleep 0.5
}

case "$mode" in
  binary)
    output=$(feed | timeout 10 "$target" --skip-probe 2>/dev/null || true)
    ;;
  docker)
    output=$(feed | timeout 10 docker run -i --rm \
      -e PIPEDRIVE_API_TOKEN \
      -e PIPEDRIVE_COMPANY_DOMAIN \
      "$target" --skip-probe 2>/dev/null || true)
    ;;
  *)
    echo "unknown mode: $mode" >&2
    exit 2
    ;;
esac

if [ -z "$output" ]; then
  echo "::error::no output from $mode $target" >&2
  exit 1
fi

# Each frame is a JSON-RPC object on its own line. The list response must
# have id=2 and a tools array (possibly empty in Phase 0).
list_line=$(printf '%s\n' "$output" | grep -E '"id":2' | head -n1 || true)
if [ -z "$list_line" ]; then
  echo "::error::no tools/list response in output" >&2
  echo "$output" >&2
  exit 1
fi

if ! printf '%s' "$list_line" | grep -qE '"tools"\s*:\s*\['; then
  echo "::error::tools/list response missing tools array" >&2
  echo "$list_line" >&2
  exit 1
fi

echo "stdio smoke OK ($mode)"
