#!/usr/bin/env bash
# Drive a minimal MCP handshake through the binary (or Docker image) on
# stdin and assert a well-formed tools/list response on stdout. Used by
# both the binary and Docker stdio-smoke CI gates.
#
# Usage:
#   stdio-smoke.sh binary <path-to-binary>
#   stdio-smoke.sh docker <image-ref>
#
# Sequence of frames sent on stdin:
#   1. initialize                              (request, id=1)
#   2. notifications/initialized               (notification — required by
#                                              MCP spec after init before
#                                              the client may issue other
#                                              requests; older versions of
#                                              the Go SDK were lenient,
#                                              newer ones are not, and the
#                                              Docker container's buffering
#                                              behavior surfaces this gap)
#   3. tools/list                              (request, id=2)
# After writing all frames we hold stdin open with a sleep so the server
# has time to read, process, and flush both responses before it sees EOF.

set -euo pipefail

mode="${1:-}"
target="${2:-}"

if [ -z "$mode" ] || [ -z "$target" ]; then
  echo "usage: $0 (binary|docker) <path-or-image>" >&2
  exit 2
fi

# The smoke does not exercise the auth probe — we want to verify stdio
# framing regardless of token validity. The binary supports --skip-probe.
export PIPEDRIVE_API_TOKEN="${PIPEDRIVE_API_TOKEN:-smoke-token}"
export PIPEDRIVE_COMPANY_DOMAIN="${PIPEDRIVE_COMPANY_DOMAIN:-smoke}"

request_init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
notif_initialized='{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'
request_list='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'

# Hold stdin open for HOLD seconds after writing all frames. Tuned for
# Docker cold-start on a CI runner — a tighter value works locally but
# loses the safety margin we want.
HOLD_SECS="${HOLD_SECS:-3}"

feed() {
  printf '%s\n' "$request_init" "$notif_initialized" "$request_list"
  sleep "$HOLD_SECS"
}

case "$mode" in
  binary)
    output=$(feed | timeout 15 "$target" --skip-probe 2>/dev/null || true)
    ;;
  docker)
    output=$(feed | timeout 15 docker run -i --rm \
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
