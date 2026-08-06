# Unified Preview Safety and Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make local, SSH remote, and hub TUI previews obey one terminal-safety boundary, then publish and deploy the fix as `v1.11.1`.

**Architecture:** Keep tmux, SSH, hub, cache, web, and clipboard payloads raw. Add shared render-boundary helpers that sanitize and cell-truncate every completed TUI preview line, and call them from all three preview renderers. Release the verified merged tree through the existing tag-driven GoReleaser workflow.

**Tech Stack:** Go 1.25.12, Bubble Tea/Lip Gloss, tmux capture-pane, GitHub Actions/GoReleaser, AWS EC2 over SSH.

## Global Constraints

- Local, SSH remote, and hub TUI previews must use the same terminal-safety rules.
- Tabs expand to eight-cell terminal tab stops before cell-width measurement.
- Raw caches, transports, transcripts, web responses, and clipboard paths remain unchanged.
- ANSI SGR styling remains supported; dangerous C0 controls and display-erasing CSI sequences do not reach the outer terminal.
- No completion claim before fresh regression, full race, lint, vulnerability, build, release-asset, and AWS-workstation verification.
- Release version is exactly `v1.11.1`.

---

### Task 1: Add cross-source preview regression coverage

**Files:**
- Create: `internal/ui/preview_source_safety_test.go`
- Reference: `internal/ui/preview_clip_test.go`
- Reference: `internal/ui/hub_integration_test.go`

**Interfaces:**
- Consumes: `(*Home).renderRemotePreview`, `(*Home).renderHubPreview`, `remotePreviewCacheKey`, `hubPreviewCacheKey`, and `cellWidth`.
- Produces: `TestPreviewSources_ExpandTabsAndConstrainEveryLine`, which fails whenever an SSH or hub preview emits a tab or a line wider than its pane budget.

- [ ] **Step 1: Write the failing test**

Create an ANSI-rich fixture containing two horizontal tabs and a long suffix:

```go
pane := "\x1b[48;2;33;58;43m    \x1b[2m588 \x1b[0m\x1b[32m+\x1b[39m\t\t\x1b[35mreturn\x1b[0m err" + strings.Repeat("x", 80)
```

Populate real preview caches for one SSH `RemoteSessionInfo` and one `HubSessionInfo`, render each at width 40, and assert for every result:

```go
if strings.ContainsRune(got, '\t') {
    t.Fatalf("preview leaked a tab: %q", got)
}
if !strings.Contains(got, "\x1b[") {
    t.Fatalf("preview lost ANSI styling: %q", got)
}
for _, line := range strings.Split(got, "\n") {
    if width := cellWidth(line); width > 38 {
        t.Fatalf("preview row width = %d, want <= 38: %q", width, line)
    }
}
```

- [ ] **Step 2: Verify the new test fails for the right reason**

Run:

```bash
go test ./internal/ui -run 'TestPreviewSources_ExpandTabsAndConstrainEveryLine|TestRenderPreviewPane_ExpandsTabsBeforeWidthAccounting' -count=1
```

Expected: the new test fails because SSH or hub output contains `\t` or exceeds 38 cells; the existing local test passes.

### Task 2: Apply one safety boundary to all TUI preview renderers

**Files:**
- Modify: `internal/ui/home.go`
- Test: `internal/ui/preview_source_safety_test.go`
- Test: `internal/ui/preview_clip_test.go`

**Interfaces:**
- Consumes: `stripControlCharsPreserveANSI`, `stripDisplayErasingEscapes`, `remapANSIBackground`, `previewSurfaceANSI`, `cellWidth`, and `cellTruncate`.
- Produces: `sanitizePreviewLine(line string, maxWidth int) string` and `sanitizePreviewOutput(content string, maxWidth int) string`.

- [ ] **Step 1: Implement the minimal shared line helper**

```go
func sanitizePreviewLine(line string, maxWidth int) string {
    line = stripControlCharsPreserveANSI(line)
    line = stripDisplayErasingEscapes(line)
    if GetCurrentTheme() == ThemeLight {
        line = remapANSIBackground(line, previewSurfaceANSI())
    }
    if maxWidth < 1 {
        maxWidth = 1
    }
    if cellWidth(line) > maxWidth {
        line = cellTruncate(line, maxWidth, "")
    }
    if strings.ContainsRune(line, 0x1b) {
        line += "\x1b[0m"
    }
    return line
}
```

- [ ] **Step 2: Implement the completed-output helper**

```go
func sanitizePreviewOutput(content string, maxWidth int) string {
    lines := strings.Split(content, "\n")
    for i := range lines {
        lines[i] = sanitizePreviewLine(lines[i], maxWidth)
    }
    return strings.Join(lines, "\n")
}
```

- [ ] **Step 3: Wire all renderers to the shared boundary**

Use `sanitizePreviewLine` in the local captured-content loop. Replace the local renderer's duplicated final truncation/reset loop with:

```go
return sanitizePreviewOutput(b.String(), max(1, width-2))
```

Return the same expression from `renderRemotePreview` and `renderHubPreview` after their builders are complete. Do not mutate `previewCache` or fetched content.

- [ ] **Step 4: Verify focused tests pass**

```bash
go test ./internal/ui -run 'TestPreviewSources_ExpandTabsAndConstrainEveryLine|TestRenderPreviewPane_ExpandsTabsBeforeWidthAccounting|TestIssue1101_RemotePreview_RendersClaudeFormattedContent' -count=1
```

Expected: PASS with ANSI content retained, no tabs, and every preview row at or below the requested cell width.

- [ ] **Step 5: Run the complete UI package**

```bash
go test ./internal/ui -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the renderer fix**

```bash
git add internal/ui/home.go internal/ui/preview_source_safety_test.go
git commit -m "fix(ui): sanitize every preview source"
```

### Task 3: Prepare release `v1.11.1`

**Files:**
- Modify: `cmd/agent-deck/main.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the release workflow's exact tag/code-version equality check.
- Produces: code version `1.11.1` and a dated changelog section documenting unified preview safety.

- [ ] **Step 1: Bump the code version**

```go
var Version = "1.11.1" // overridden at build time via -ldflags "-X main.Version=..."
```

- [ ] **Step 2: Finalize the changelog section**

Keep an empty `## [Unreleased]`, insert `## [1.11.1] - 2026-08-06`, and place the existing unreleased entries under it. Add:

```markdown
- **Preview terminal safety is consistent across local, SSH remote, and hub sessions.** Tabs are expanded before cell-width accounting, terminal erase controls are removed, and every rendered preview row is constrained to its pane without changing raw cached or copied content.
```

- [ ] **Step 3: Verify version consistency and build**

```bash
go test ./cmd/agent-deck -run 'Test.*Version' -count=1
make build VERSION=1.11.1
./build/agent-deck version
```

Expected: tests and build pass and the binary reports `Agent Deck v1.11.1`.

- [ ] **Step 4: Commit release preparation**

```bash
git add cmd/agent-deck/main.go CHANGELOG.md
git commit -m "chore(release): prepare v1.11.1"
```

### Task 4: Verify, integrate, publish, and deploy

**Files:**
- Verify only: complete repository
- External: `origin/main`, Git tag `v1.11.1`, GitHub release assets, AWS instance `i-03b509e682f38931a`

**Interfaces:**
- Consumes: branch commits from Tasks 2 and 3 and `.github/workflows/release.yml`.
- Produces: published `v1.11.1` release and an AWS workstation running that exact release from `lbr88/agent-deck`.

- [ ] **Step 1: Run fresh local release gates**

```bash
PERF_BUDGET_MULTIPLIER=2.0 gotestsum --rerun-fails=2 --rerun-fails-abort-on-data-race --packages='./...' -- -race -count=1 -timeout 20m
golangci-lint run --timeout=5m
govulncheck ./...
uvx zizmor .github/workflows
make build
```

The Go suite, lint, vulnerability scan, and build must exit zero. Record pre-existing `zizmor` findings separately; no workflow file is changed by this fix.

- [ ] **Step 2: Perform isolated TUI verification**

Launch `./build/agent-deck` in an isolated outer tmux with copied XDG state, select fixtures for local, SSH remote, and hub previews, capture the outer pane, and assert no tab bytes and no row wider than the pane budget.

- [ ] **Step 3: Merge the branch to `main` and repeat focused verification**

Fast-forward `main`, rerun the focused preview test and `make build`, then remove only the task-owned worktree and branch.

- [ ] **Step 4: Push main and tag**

```bash
git push origin main
git tag -a v1.11.1 -m "v1.11.1"
git push origin v1.11.1
```

- [ ] **Step 5: Monitor and verify the release**

Use `gh run watch` for the tag-triggered Release workflow. Verify the release is non-draft and contains the four platform archives plus `checksums.txt`; download the Linux amd64 archive and verify its SHA-256 against `checksums.txt`.

- [ ] **Step 6: Update and verify the AWS workstation**

On `sandbox-lars-workstation` (`i-03b509e682f38931a`, `10.176.4.82`), run the verified updater with `--repo lbr88/agent-deck`, persist that repository in update config, and confirm:

```bash
agent-deck version
systemctl --user is-active agent-deck-hub-connect.service
```

The version must be `v1.11.1`, the connector must be active, the hub must report `lbr-workstation` online, and an isolated TUI capture of a hub preview must have zero tab bytes and stable columns. Do not restart or modify tmux agent sessions.
