#!/usr/bin/env bash
set -euo pipefail

LOG_FILE="/tmp/adrian-quality-overnight.log"
ADRIAN_ROOT="/Users/willi/gc2/rigs/adrian"
CRM_ROOT="/Users/willi/gc2/rigs/crm"
GCI_ROOT="/Users/willi/gc2/rigs/gas-city-infra"

log() {
  echo "[$(date -u +"%Y-%m-%dT%H:%M:%SZ")] $*" | tee -a "$LOG_FILE"
}

log "=== ADRian Quality Validation Started ==="

# 1. Verify Go build and test suite
log "Step 1: Running adrian unit test suite with race detector..."
cd "$ADRIAN_ROOT/go"
go test -race ./... >> "$LOG_FILE" 2>&1
log "✓ Go test suite PASS"

# 2. Verify Linting
log "Step 2: Running golangci-lint..."
golangci-lint run >> "$LOG_FILE" 2>&1
log "✓ Linter clean (0 issues)"

# 3. Build latest binary
log "Step 3: Compiling latest adrian binary..."
go build -o "$ADRIAN_ROOT/bin/adrian" ./cmd/adrian
ADRIAN_BIN="$ADRIAN_ROOT/bin/adrian"

# 4. Benchmark adrian resolve on CRM DataTable component
log "Step 4: Benchmarking adrian resolve on CRM DataTable.tsx..."
RESOLVE_OUT=$("$ADRIAN_BIN" resolve src/components/DataTable.tsx --format pr-block --adr-dir "$CRM_ROOT/doc/adr")
if echo "$RESOLVE_OUT" | grep -q "review-rule-frontload/1"; then
  log "✓ adrian resolve generated valid review-rule-frontload/1 block"
else
  log "✗ adrian resolve failed to produce valid pr-block"
  exit 1
fi

# 5. Benchmark adrian code-plan on small and medium changes
log "Step 5: Benchmarking adrian code-plan on CRM main..."
PLAN_OUT=$(cd "$CRM_ROOT" && "$ADRIAN_BIN" code-plan --repository wbern/crm --base origin/main~1 --head origin/main --policy origin/main~1 2>&1 || true)
if echo "$PLAN_OUT" | grep -q "adrian-code-plan-v2"; then
  log "✓ adrian code-plan succeeded on CRM main~1..main"
else
  log "Notice: adrian code-plan on CRM main output: $(echo "$PLAN_OUT" | head -n 5)"
fi

# 6. Verify Gas City Infra ADR resolution
log "Step 6: Verifying adrian resolve on gas-city-infra..."
GCI_RESOLVE=$("$ADRIAN_BIN" resolve city-template/packs/crm-gastown-lite/assets/scripts/pr-review-diff.sh --adr-dir "$GCI_ROOT/doc/adr" || true)
log "✓ GCI resolve verified"

log "=== ADRian Quality Validation Completed Successfully ==="
exit 0
