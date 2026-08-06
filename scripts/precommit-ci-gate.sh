#!/usr/bin/env bash
# Local pre-commit CI gate.
#
# Mirrors the PR-gate classes that have repeatedly caught regressions:
#   - go-test.yml: full race test suite via gotestsum
#   - golangci-lint.yml: strict golangci-lint
#   - govulncheck.yml: reachable vulnerability scan
#   - web-tests.yml: Vitest, Playwright e2e, web parity Go tests
#   - lighthouse-ci.yml: local Lighthouse budget check when relevant files change
#
# This is intentionally stricter than the old "new lint only" hook. It is
# expensive, but it fails locally before a commit is made instead of wasting CI
# cycles after push.

set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.25.12}"
export PERF_BUDGET_MULTIPLIER="${PERF_BUDGET_MULTIPLIER:-2.0}"

# Keep local CI scratch out of /tmp and out of the repository. Full race/web
# checks can create multi-GB build artifacts, and child test binaries inherit
# these values when they run isolated HOME directories.
DEFAULT_CACHE_ROOT="${XDG_CACHE_HOME:-${HOME:-$ROOT}/.cache}/agent-deck/precommit"
CACHE_ROOT="${AGENT_DECK_PRECOMMIT_CACHE_DIR:-$DEFAULT_CACHE_ROOT}"
export GOCACHE="${GOCACHE:-$CACHE_ROOT/go-cache}"
export GOTMPDIR="${GOTMPDIR:-$CACHE_ROOT/go-tmp}"
export GOPATH="${GOPATH:-$CACHE_ROOT/go-path}"
export GOMODCACHE="${GOMODCACHE:-$CACHE_ROOT/go-mod}"
export PATH="$GOPATH/bin:$PATH"
mkdir -p "$GOCACHE" "$GOTMPDIR" "$GOPATH/bin" "$GOMODCACHE"

if [ "${AGENT_DECK_SKIP_PRECOMMIT_CI:-}" = "1" ]; then
  echo "[precommit-ci] AGENT_DECK_SKIP_PRECOMMIT_CI=1; skipping local CI gate."
  exit 0
fi

staged_files="$(git diff --cached --name-only --diff-filter=ACMR)"
if [ -z "$staged_files" ]; then
  echo "[precommit-ci] No staged files; nothing to check."
  exit 0
fi

has_match() {
  local pattern="$1"
  printf '%s\n' "$staged_files" | grep -Eq "$pattern"
}

go_changed=false
web_changed=false
lighthouse_changed=false

if has_match '(^|/)[^/]+\.go$|^go\.mod$|^go\.sum$|^\.github/workflows/(go-test|golangci-lint|govulncheck)\.yml$'; then
  go_changed=true
fi

if has_match '^(internal/web/|cmd/agent-deck/web_cmd\.go$|tests/web/|documentation/webui-overhaul-plan\.md$|Makefile$|go\.mod$|go\.sum$|\.github/workflows/web-tests\.yml$)'; then
  web_changed=true
fi

if has_match '^(internal/web/|tests/lighthouse/|\.lighthouserc\.json$|\.github/workflows/lighthouse-ci\.yml$)'; then
  lighthouse_changed=true
fi

run() {
  echo
  echo "[precommit-ci] $*"
  "$@"
}

ensure_go_tool() {
  local bin="$1"
  local pkg="$2"
  if ! command -v "$bin" >/dev/null 2>&1; then
    run go install "$pkg"
  fi
}

if [ "$go_changed" = true ]; then
  echo "[precommit-ci] Go-relevant staged changes detected."

  go_files="$(git ls-files '*.go' | grep -v -E '^(\.worktrees/|\.claude/worktrees/)' || true)"
  if [ -n "$go_files" ]; then
    unformatted="$(printf '%s\n' "$go_files" | xargs gofmt -l)"
    if [ -n "$unformatted" ]; then
      echo "Run 'go fmt ./...' or gofmt the files below before committing:"
      echo "$unformatted"
      exit 1
    fi
  fi

  ensure_go_tool golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
  run golangci-lint run --timeout=5m

  ensure_go_tool govulncheck golang.org/x/vuln/cmd/govulncheck@latest
  run govulncheck ./...

  ensure_go_tool gotestsum gotest.tools/gotestsum@v1.13.0
  run gotestsum --rerun-fails=2 --rerun-fails-abort-on-data-race --packages="./..." -- -race -timeout 20m
else
  echo "[precommit-ci] No Go CI-triggering staged changes."
fi

if [ "$web_changed" = true ]; then
  echo "[precommit-ci] Web-test-triggering staged changes detected."
  command -v node >/dev/null 2>&1 || { echo "node is required for web CI checks."; exit 1; }
  npm_cmd=(npm)
  if ! command -v npm >/dev/null 2>&1; then
    if command -v pnpm >/dev/null 2>&1; then
      echo "[precommit-ci] npm not found; using pnpm dlx npm@10 for npm-compatible commands."
      npm_cmd=(pnpm dlx npm@10)
    else
      echo "npm is required for web CI checks (or pnpm for the npm@10 fallback)."
      exit 1
    fi
  fi

  run env GOTOOLCHAIN="$GOTOOLCHAIN" go test ./internal/web/ -run TestParity -race -count=1
  run env GOTOOLCHAIN="$GOTOOLCHAIN" go build -o tests/web/.tmp/web-fixture ./tests/web/fixtures/cmd/web-fixture/

  (
    cd tests/web
    run "${npm_cmd[@]}" ci --no-audit --no-fund
    run "${npm_cmd[@]}" run test:unit

    # Use Playwright's matching browser by default. Arbitrary system Chromium
    # launchers can be Snap-confined or protocol-incompatible; callers that
    # intentionally want one can still set PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH.
    # shellcheck source=prepare-playwright-browser.sh
    source "$ROOT/scripts/prepare-playwright-browser.sh"
    prepare_playwright_browser
    run "${npm_cmd[@]}" run test:e2e
  )
else
  echo "[precommit-ci] No web-test-triggering staged changes."
fi

if [ "$lighthouse_changed" = true ]; then
  echo "[precommit-ci] Lighthouse-triggering staged changes detected."
  run make build
  run ./tests/lighthouse/budget-check.sh
else
  echo "[precommit-ci] No Lighthouse-triggering staged changes."
fi

echo
echo "[precommit-ci] Local CI gate passed."
