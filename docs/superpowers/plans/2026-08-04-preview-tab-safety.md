# Preview Tab Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent horizontal tabs in ANSI-rich tmux pane captures from overflowing and corrupting Agent Deck's TUI preview.

**Architecture:** Keep tmux capture, preview caching, clipboard output, and the live session unchanged. Expand tabs into explicit spaces at the existing visual-preview control-character boundary before ANSI-aware width measurement and truncation.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, `github.com/charmbracelet/x/ansi`, tmux.

## Global Constraints

- Expand tabs only for visual preview and notes rendering through `stripControlCharsPreserveANSI`.
- Use eight-cell terminal tab stops and ignore ANSI escape bytes when calculating the current column.
- Preserve ANSI styling, newlines, and existing removal of dangerous C0 controls.
- Do not change copied terminal text, tmux capture semantics, hub/web output, or the live `iac-cicd` session.
- Follow Conventional Commits and do not add attribution trailers.

---

### Task 1: Expand captured tabs before preview width accounting

**Files:**
- Modify: `internal/ui/preview_clip_test.go`
- Modify: `internal/ui/home.go:24418`

**Interfaces:**
- Consumes: `stripControlCharsPreserveANSI(s string) string`, `homeWithRunningPreview`, `cellWidth`.
- Produces: `stripControlCharsPreserveANSI` output containing ANSI and explicit spaces but no horizontal tabs.

- [ ] **Step 1: Write the failing regression test**

Append this test to `internal/ui/preview_clip_test.go`:

```go
func TestRenderPreviewPane_ExpandsTabsBeforeWidthAccounting(t *testing.T) {
	const raw = "\x1b[48;2;33;58;43m    \x1b[2m588 \x1b[0m\x1b[32m+\x1b[39m\t\t\x1b[35mreturn\x1b[0m err\n"

	h := homeWithRunningPreview(t, raw, 40, 20)
	rendered := h.renderPreviewPane(40, 20)

	if strings.ContainsRune(rendered, '\t') {
		t.Fatalf("rendered preview leaked a horizontal tab into the outer terminal: %q", rendered)
	}
	if !strings.Contains(ansi.Strip(rendered), "    588 +               return err") {
		t.Fatalf("tabs were not expanded at eight-cell stops: %q", ansi.Strip(rendered))
	}
	if !strings.Contains(rendered, "\x1b[35mreturn") {
		t.Fatalf("tab sanitization removed ANSI styling: %q", rendered)
	}
	for i, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(line); got > 40 {
			t.Fatalf("rendered row %d is %d cells wide, want <= 40: %q", i, got, line)
		}
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
go test ./internal/ui -run TestRenderPreviewPane_ExpandsTabsBeforeWidthAccounting -count=1
```

Expected: FAIL because `rendered` still contains `\t`.

- [ ] **Step 3: Implement minimal tab expansion**

Replace `stripControlCharsPreserveANSI` in `internal/ui/home.go` with:

```go
// stripControlCharsPreserveANSI removes dangerous C0 control characters while
// preserving ANSI escape sequences (ESC = 0x1b). Horizontal tabs are expanded
// to explicit spaces at eight-cell terminal tab stops before preview width
// measurement, preventing the outer terminal from moving beyond the pane.
func stripControlCharsPreserveANSI(s string) string {
	const tabWidth = 8

	var b strings.Builder
	b.Grow(len(s))
	lineStart := 0

	for _, r := range s {
		switch {
		case r == '\t':
			column := cellWidth(b.String()[lineStart:])
			b.WriteString(strings.Repeat(" ", tabWidth-column%tabWidth))
		case r == '\n':
			b.WriteRune(r)
			lineStart = b.Len()
		case r < 0x20 && r != '\x1b':
			continue
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}
```

- [ ] **Step 4: Run focused and preview safety tests and verify GREEN**

Run:

```bash
go test ./internal/ui -run 'TestRenderPreviewPane_(ExpandsTabsBeforeWidthAccounting|StripsErase|EveryLineFitsWidth|NvimStatusline)' -count=1
```

Expected: PASS with no warnings.

- [ ] **Step 5: Run the complete UI package**

Run:

```bash
go test ./internal/ui -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the tested fix**

```bash
git add internal/ui/home.go internal/ui/preview_clip_test.go
git commit -m "fix(ui): expand tabs before preview rendering"
```

---

### Task 2: Verify, install, and exercise the live failure case

**Files:**
- Verify only: `internal/ui/home.go`
- Verify only: `internal/ui/preview_clip_test.go`
- Generated binary: `build/agent-deck`
- Installed binary: `/home/lrasmussen/.local/bin/agent-deck`

**Interfaces:**
- Consumes: the tab-safe preview renderer from Task 1 and live tmux session `agentdeck_nimble-yew_23c3621f`.
- Produces: a verified and locally installed Agent Deck binary; no mutation of the live Codex session.

- [ ] **Step 1: Run full Go verification**

Run:

```bash
go test -race -count=1 ./...
golangci-lint run --timeout=5m
govulncheck ./...
```

Expected: all commands exit 0; `govulncheck` reports no reachable vulnerabilities.

- [ ] **Step 2: Build the production binary**

Run:

```bash
make build
```

Expected: exit 0 and `build/agent-deck` exists.

- [ ] **Step 3: Validate the actual pane still contains the reproduced trigger**

Run:

```bash
tmux capture-pane -p -e -t agentdeck_nimble-yew_23c3621f:0.0 -S -120 |
  perl -e 'local $/; $s=<STDIN>; $n=()=$s=~/\t/g; print "$n tabs\n"'
```

Expected: a non-zero tab count, proving validation still targets the original trigger.

- [ ] **Step 4: Render a temporary outer TUI against the live session**

Run:

```bash
tmux -L agentdeck-preview-tab-verify new-session -d \
  -s agentdeck-preview-tab-verify -x 190 -y 60 \
  "cd /home/lrasmussen/git/private/agent-deck && env -u TMUX -u TMUX_PANE TERM=xterm-256color ./build/agent-deck --select 93b8feaa-1785425016"

for attempt in {1..100}; do
  if tmux -L agentdeck-preview-tab-verify capture-pane -p \
    -t agentdeck-preview-tab-verify:0.0 | rg -q 'GEN-8524 iac-cicd'; then
    break
  fi
  sleep 0.1
done

tmux -L agentdeck-preview-tab-verify capture-pane -p -e \
  -t agentdeck-preview-tab-verify:0.0
tmux -L agentdeck-preview-tab-verify kill-server
```

Expected: the captured outer frame keeps session-list rows and preview rows in
their own columns, with the Codex diff line numbers in source order rather than
the interleaved order from the reported screenshot. Stop only this explicitly
named temporary outer tmux server; do not restart or send keys to
`agentdeck_nimble-yew_23c3621f`.

- [ ] **Step 5: Install locally**

Run the repository's existing local installation target after inspecting it:

```bash
make install
```

Expected: `/home/lrasmussen/.local/bin/agent-deck version` reports the new commit.

- [ ] **Step 6: Verify repository state**

Run:

```bash
git status --short --branch
git diff --check
```

Expected: clean task branch with no unstaged or uncommitted files.
