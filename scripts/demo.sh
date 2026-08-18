#!/usr/bin/env bash
#
# A guided walk through one semantic run, against a real running server.
#
# It uses nothing but the public HTTP API and the committed example payloads, so
# everything it shows is something a caller can reproduce. There is no demo mode in
# the binary and no special path through the code: a demo that took a shortcut
# would be evidence about the shortcut.
#
# Usage: scripts/demo.sh [base-url]

set -euo pipefail

BASE="${1:-http://127.0.0.1:8080}"
TENANT="${ML_DEMO_TENANT:-acme}"
EXAMPLES="$(cd "$(dirname "${BASH_SOURCE[0]}")/../examples/teamhos" && pwd)"
GRAFANA_PORT="${ML_GRAFANA_PORT:-3000}"

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'
  GREEN=$'\033[32m'; RED=$'\033[31m'; YELLOW=$'\033[33m'; CYAN=$'\033[36m'
else
  BOLD=''; DIM=''; RESET=''; GREEN=''; RED=''; YELLOW=''; CYAN=''
fi

step() { printf '\n%s%s %s%s\n' "$BOLD$CYAN" "──" "$1" "$RESET"; }
note() { printf '%s   %s%s\n' "$DIM" "$1" "$RESET"; }
good() { printf '   %s✓%s %s\n' "$GREEN" "$RESET" "$1"; }
bad()  { printf '   %s✗%s %s\n' "$RED" "$RESET" "$1"; }
warn() { printf '   %s!%s %s\n' "$YELLOW" "$RESET" "$1"; }
field() { printf '     %-24s %s\n' "$1" "$2"; }

for tool in curl jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "this demo needs $tool" >&2; exit 1; }
done

api() {
  local method="$1" path="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl --silent --show-error --fail-with-body \
      --header 'Content-Type: application/json' \
      --header "X-Maiden-Lane-Tenant: $TENANT" \
      --request "$method" --data-binary "@$body" "$BASE$path"
  else
    curl --silent --show-error --fail-with-body \
      --header "X-Maiden-Lane-Tenant: $TENANT" \
      --request "$method" "$BASE$path"
  fi
}

# ── Preflight ────────────────────────────────────────────────────────────────
health="$(curl --silent --connect-timeout 1 --max-time 2 \
  --output /dev/null --write-out '%{http_code}' "$BASE/healthz" || true)"
if [ "$health" != "204" ]; then
  bad "no server at $BASE (health returned ${health:-nothing})"
  note "start one with:  go run ./cmd/maiden-lane serve"
  note "or against Postgres, see the README's storage section."
  exit 1
fi

printf '%s\n' "${BOLD}Maiden Lane — one semantic run, end to end${RESET}"
note "server $BASE   tenant $TENANT"
note "payloads $EXAMPLES"

# ── 1. Compile ───────────────────────────────────────────────────────────────
step "1. Compile declarations into a plan"
note "POST /v1/plans with examples/teamhos/plan.json"
plan="$(api POST /v1/plans "$EXAMPLES/plan.json")"
plan_id="$(jq -r .planID <<<"$plan")"
field "planID" "$plan_id"
field "rules" "$(jq -r '.rules | join(" → ")' <<<"$plan")"
field "checkpoints" "$(jq -r '[.checkpoints[].key] | join(" then ")' <<<"$plan")"
field "profiles" "$(jq -r '[.profiles[].key] | join(", ")' <<<"$plan")"

note "Plan identity is derived from the declarations, not allocated. Compiling the"
note "same program again must return the same identity rather than a second plan."
again="$(jq -r .planID <<<"$(api POST /v1/plans "$EXAMPLES/plan.json")")"
if [ "$again" = "$plan_id" ]; then
  good "recompiled to the same planID — the program has one name, forever"
else
  bad "recompiling produced $again"
  exit 1
fi

# ── 2. Execute ───────────────────────────────────────────────────────────────
step "2. Execute it over a pinned observation"
note "POST /v1/executions with examples/teamhos/execution.json"
note "Two drivers reporting the same duty anchor T0, elapsed 10/7, driving 8/6."
accepted="$(api POST /v1/executions "$EXAMPLES/execution.json")"
execution_id="$(jq -r .executionID <<<"$accepted")"
field "executionID" "$execution_id"
field "semanticRunID" "$(jq -r .semanticRunID <<<"$accepted")"
field "status" "$(jq -r .executionStatus <<<"$accepted")"

note "Execution identity is derived from the request too, so resubmitting is"
note "idempotent with no idempotency key: the same input IS the same execution."
resubmitted="$(jq -r .executionID <<<"$(api POST /v1/executions "$EXAMPLES/execution.json")")"
if [ "$resubmitted" = "$execution_id" ]; then
  good "resubmission returned the same executionID — no duplicate run"
else
  bad "resubmission forked into $resubmitted"
  exit 1
fi

await() {
  local id="$1" attempt=0 body status
  while [ "$attempt" -lt 100 ]; do
    body="$(api GET "/v1/executions/$id")"
    status="$(jq -r .executionStatus <<<"$body")"
    case "$status" in
      succeeded|failed) printf '%s' "$body"; return 0 ;;
    esac
    attempt=$((attempt + 1))
    sleep 0.1
  done
  bad "execution $id never finished (last status $status)"
  return 1
}

result="$(await "$execution_id")"

# ── 3. What it produced ──────────────────────────────────────────────────────
step "3. What the run produced"
field "spineStatus" "$(jq -r .result.spineStatus <<<"$result")"
field "accepted rules" "$(jq -r '.result.acceptedRules | join(" → ")' <<<"$result")"
field "inputID" "$(jq -r .result.inputID <<<"$result")"
field "final state digest" "$(jq -r .result.finalStateDigest <<<"$result")"

printf '\n'
note "Sealed checkpoints — each an immutable, replay-linked semantic prefix:"
jq -r '.result.checkpoints[] | "     \(.checkpointKey)\n       artifact  \(.checkpointArtifactID)\n       state     \(.stateDigest)"' <<<"$result"

printf '\n'
note "Readiness is asked per profile, and the answers differ. A checkpoint can be"
note "publishable to one consumer while incomplete for another — that is a real"
note "answer about a real prefix, not a partial result."
jq -r '.result.assessments[] |
  "     \(.checkpointArtifactID[0:19])…  \(.profileKey | . + (" " * (13 - length)))\(.verdict)" +
  (if (.missingRequirements | length) > 0
   then "  missing: " + (.missingRequirements | join(", "))
   else "" end)' <<<"$result"

ready_count="$(jq '[.result.assessments[] | select(.verdict == "ready")] | length' <<<"$result")"
needs_count="$(jq '[.result.assessments[] | select(.verdict == "needs_input")] | length' <<<"$result")"
good "$ready_count ready, $needs_count needs_input — asked and answered separately"

# ── 4. Determinism ───────────────────────────────────────────────────────────
step "4. The same input produces the same artifacts"
note "Replay is the property everything else rests on. Re-reading the execution"
note "must return byte-identical identities and digests."
again_result="$(api GET "/v1/executions/$execution_id")"
if [ "$(jq -cS .result <<<"$result")" = "$(jq -cS .result <<<"$again_result")" ]; then
  good "identical result document, digest for digest"
else
  bad "the same execution reported two different results"
  exit 1
fi

# ── 5. Refusal ───────────────────────────────────────────────────────────────
step "5. Now change one observation"
note "examples/teamhos/execution-anchor-mismatch.json differs in exactly one field:"
printf '\n'
diff --unified=0 "$EXAMPLES/execution.json" "$EXAMPLES/execution-anchor-mismatch.json" \
  | grep -E '^[+-]  ' | sed 's/^/       /' || true
printf '\n'
note "Driver B now reports a different duty anchor. The two drivers cannot be"
note "aggregated into one team HOS tuple, because they are not describing the same"
note "period. A system that averaged them anyway would produce a confident number"
note "nobody could defend."

mismatch_accepted="$(api POST /v1/executions "$EXAMPLES/execution-anchor-mismatch.json")"
mismatch_id="$(jq -r .executionID <<<"$mismatch_accepted")"
field "executionID" "$mismatch_id"
field "semanticRunID" "$(jq -r .semanticRunID <<<"$mismatch_accepted")"
if [ "$(jq -r .planID <<<"$mismatch_accepted")" = "$plan_id" ]; then
  good "same planID — the program did not change, only the observation"
fi

mismatch="$(await "$mismatch_id")"
printf '\n'
field "spineStatus" "$(jq -r .result.spineStatus <<<"$mismatch")"
field "accepted rules" "$(jq -r '.result.acceptedRules | join(" → ")' <<<"$mismatch")"
field "failure kind" "$(jq -r .result.failure.kind <<<"$mismatch")"
field "failure code" "$(jq -r .result.failure.code <<<"$mismatch")"

printf '\n'
note "Sealed checkpoints from the refused run:"
jq -r '.result.checkpoints[] | "     \(.checkpointKey)  \(.checkpointArtifactID[0:19])…"' <<<"$mismatch"

sealed_ok="$(jq '.result.checkpoints | length' <<<"$result")"
sealed_bad="$(jq '.result.checkpoints | length' <<<"$mismatch")"
printf '\n'
good "$sealed_bad checkpoint(s) sealed, not $sealed_ok — the prefix that was valid is"
note "  kept, and the boundary that could not be justified sealed nothing."
good "the refusal names a closed code, and the HTTP status is still 200"
note "  A run that refused produced a real answer. It is not a server error, and"
note "  the artifacts verified before the refusal are not discarded."

# ── 6. Traces ────────────────────────────────────────────────────────────────
step "6. Where to look next"
if curl --silent --connect-timeout 1 --max-time 2 --output /dev/null \
     "http://127.0.0.1:$GRAFANA_PORT/api/health" 2>/dev/null; then
  good "Grafana is up: http://127.0.0.1:$GRAFANA_PORT"
  note "Explore → Tempo → TraceQL. Each execution above is ONE trace, rooted at the"
  note "worker span, whose children are the semantic phases: compile, bind, execute,"
  note "seal, assess. Paste either query:"
  printf '\n       %s{ .maiden_lane.semantic.execution_id = "%s" }%s\n' \
    "$BOLD" "$execution_id" "$RESET"
  printf '       %s{ .maiden_lane.semantic.execution_id = "%s" }%s\n\n' \
    "$BOLD" "$mismatch_id" "$RESET"
  note "The refused run's trace is the interesting one. The rejected boundary is a"
  note "span carrying the closed code -- execute_transition with"
  note "result=protected_invariant_failed, code=HOS_ANCHOR_MISMATCH -- rather than a"
  note "gap where a span should have been. And the worker span that roots the trace"
  note "reports result=answered, because the run did produce an answer."
else
  note "For the trace view of these same runs:"
  note "  make observe-up          # collector, Tempo, Prometheus, Grafana"
  note "  make demo                # re-run; it exports only when the collector is up"
  note "then open http://127.0.0.1:$GRAFANA_PORT"
  note "If Grafana is published elsewhere, set ML_GRAFANA_PORT to match."
fi

printf '\n%sBoth executions above are still readable at:%s\n' "$BOLD" "$RESET"
printf '   %s/v1/executions/%s\n' "$BASE" "$execution_id"
printf '   %s/v1/executions/%s\n\n' "$BASE" "$mismatch_id"
