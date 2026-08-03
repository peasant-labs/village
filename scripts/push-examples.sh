#!/usr/bin/env bash
#
# Push 2 example transcripts to the local village — to exercise the real
# publish pipeline (multipart upload → secret scan → MinIO blob → DB row).
#
# Auth: needs a bearer token. The village backend accepts EITHER an API key
# OR a session JWT on /transcripts/publish — so an API key is NOT required.
# Easiest: copy your logged-in session token from the browser —
#   DevTools -> Application -> Cookies -> https://localhost:8443
#   -> copy the value of "peasant_token".
#
# Setup:
#   1. Sign in at https://localhost:8443
#   2. Grab the "peasant_token" cookie value (or a /publish API key)
#   3. Run:   TOKEN=<paste> ./scripts/push-examples.sh
#
# Override the target with VILLAGE_API=... if not on the default localhost stack.
#
set -euo pipefail

API="${VILLAGE_API:-https://localhost:8443/api/v1}"
KEY="${TOKEN:-${API_KEY:-}}"
[ -n "$KEY" ] || { echo "Set TOKEN=<peasant_token cookie value, or an API key>" >&2; exit 1; }

NOW_MS=$(( $(date +%s) * 1000 ))
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# push <provider> <model> <project> <turns> <transcript-file>
push() {
  local provider="$1" model="$2" project="$3" turns="$4" file="$5"
  local sid hash meta
  sid="$(uuidgen | tr 'A-Z' 'a-z')"
  hash="$(printf '%s' "$project" | shasum -a 256 | cut -d' ' -f1)"
  meta="$TMP/$sid.json"

  cat > "$meta" <<JSON
{
  "identity":    { "sessionId": "$sid", "schemaVersion": 2 },
  "model":       { "modelHarness": "$provider", "model": "$model", "version": "demo/1.0.0" },
  "timestamp":   { "start": $(( NOW_MS - 3600000 )), "end": $NOW_MS },
  "source":      { "format": "jsonl", "filePath": "~/.demo/$project.jsonl" },
  "git":         { "branch": "main", "remote": "https://github.com/example/$project" },
  "project":     { "hash": "$hash", "name": "$project", "filePath": "/Users/dev/$project" },
  "stats":       { "turnCount": $turns, "toolCallCount": 3, "subagentCount": 0,
                   "durationMs": 3600000, "tokensIn": 4200, "tokensOut": 2600 },
  "diagnostics": { "warnings": [] }
}
JSON

  echo "→ pushing '$project' ($provider) — session $sid"
  curl -sk -X POST "$API/transcripts/publish" \
    -H "Authorization: Bearer $KEY" \
    -F "metadata=<$meta" \
    -F "transcript_file=@$file;type=application/json" \
    -w '\n   HTTP %{http_code}\n\n'
}

# ---- example transcript 1 ---------------------------------------------------
cat > "$TMP/t1.jsonl" <<'JSONL'
{"role":"user","content":"Add input validation to the signup form."}
{"role":"assistant","content":"I'll add client and server-side validation, starting with the email field and a clear inline error state."}
{"role":"user","content":"Also enforce a minimum password length of 12 characters."}
{"role":"assistant","content":"Done. Added a 12-character minimum, a strength meter, and matching server-side checks. All tests pass."}
JSONL

# ---- example transcript 2 ---------------------------------------------------
cat > "$TMP/t2.jsonl" <<'JSONL'
{"role":"user","content":"The build is failing on CI but works fine locally."}
{"role":"assistant","content":"Likely a filesystem case-sensitivity issue — macOS is case-insensitive, CI runs case-sensitive Linux."}
{"role":"user","content":"Good call — it was an import with the wrong casing."}
{"role":"assistant","content":"Fixed the import path casing and added a lint rule to catch it. CI is green now."}
JSONL

push "claude" "claude-opus-4-20250514" "signup-flow"  4 "$TMP/t1.jsonl"
push "gemini" "gemini-2.5-pro"         "ci-debugging" 4 "$TMP/t2.jsonl"

echo "Done. Sign in at https://localhost:8443 → Explore."
echo "Note: published transcripts default to visibility=private — you see your own."
echo "Make one public from its detail page, or share it to a group, to test those flows."
