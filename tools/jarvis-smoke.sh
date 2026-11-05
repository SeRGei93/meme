#!/usr/bin/env bash
# Live smoke test for the Jarvis MCP integration. It never revokes the token and
# never prints it.
#
#   JARVIS_TOKEN_FILE=~/.config/second-brain/jarvis_token \
#     BRAIN_URL=https://meme.aimerce.by tools/jarvis-smoke.sh
#
# `JARVIS_TOKEN` is also accepted, but a file avoids shell history. Requires curl
# and jq.
set -euo pipefail

: "${BRAIN_URL:?set BRAIN_URL, for example https://meme.aimerce.by}"

token="${JARVIS_TOKEN:-}"
if [ -z "$token" ] && [ -n "${JARVIS_TOKEN_FILE:-}" ]; then
  token=$(tr -d '\r\n' < "$JARVIS_TOKEN_FILE")
fi
if [ -z "$token" ]; then
  echo "set JARVIS_TOKEN_FILE (preferred) or JARVIS_TOKEN" >&2
  exit 1
fi

command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

endpoint="${BRAIN_URL%/}/mcp"
external_id="jarvis-smoke-$(date +%s)-$$"
text="Jarvis MCP smoke test ${external_id}"
source="${JARVIS_SOURCE:-jarvis}"

# Keep the bearer token out of the curl argv (visible in `ps`).
header_file=$(mktemp)
chmod 600 "$header_file"
trap 'rm -f "$header_file"' EXIT
printf 'Authorization: Bearer %s\n' "$token" > "$header_file"
unset token JARVIS_TOKEN

call_mcp() {
  local payload="$1"
  curl -fsS "$endpoint" \
    -H @"$header_file" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -d "$payload"
}

echo "→ initialize"
initialize=$(call_mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"jarvis-smoke","version":"1"}}}')
echo "$initialize" | jq -e '.result.protocolVersion == "2025-06-18"' >/dev/null \
  || { echo "initialize failed: $initialize" >&2; exit 1; }

echo "→ tools/list"
tools=$(call_mcp '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
echo "$tools" | jq -e '[.result.tools[].name] | index("remember") != null and index("search") != null' >/dev/null \
  || { echo "tools/list is missing remember/search: $tools" >&2; exit 1; }

echo "→ remember ($external_id)"
remember_payload=$(jq -nc \
  --arg text "$text" \
  --arg external_id "$external_id" \
  '{jsonrpc:"2.0",id:3,method:"tools/call",params:{name:"remember",arguments:{
      text:$text,title:"Jarvis smoke test",summary:$text,
      project:"second-brain",tags:["smoke"],external_id:$external_id}}}')
remember=$(call_mcp "$remember_payload")
echo "$remember" | jq -e '.result.isError != true' >/dev/null \
  || { echo "remember failed: $remember" >&2; exit 1; }
record_id=$(echo "$remember" | jq -r '.result.structuredContent.record_id')
echo "  record_id=$record_id"

echo "→ search (source=$source)"
search_payload=$(jq -nc --arg source "$source" --arg query "$external_id" \
  '{jsonrpc:"2.0",id:4,method:"tools/call",params:{name:"search",arguments:{
      query:$query,limit:5,source:$source}}}')
search=$(call_mcp "$search_payload")
echo "$search" | jq -e --argjson id "$record_id" \
  '.result.structuredContent.items | any(.id == $id)' >/dev/null \
  || { echo "search did not return the new record $record_id: $search" >&2; exit 1; }

echo "OK: jarvis MCP remember/search works (record $record_id, external_id $external_id)"
