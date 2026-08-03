# Upstream Synchronization and Desync Test Preservation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve the unfinished local-shell desynchronization specification on its feature branch, merge the current `upstream/main` into the fork's `main`, verify the result, and push it to `origin/main`.

**Architecture:** Keep the intentionally red specification isolated on `feat/shell-desync-monitor`, because its production API does not exist on either current fork main or upstream main. Synchronize the fork with a normal merge commit so the fork's existing 170 commits remain visible and no history is rewritten.

**Tech Stack:** Git, Go 1.25.12, golangci-lint, govulncheck, zizmor.

## Global Constraints

- Use Conventional Commits.
- Do not rewrite or force-push history.
- Preserve the uncommitted test file in Git before touching its worktree registration.
- Do not merge a knowingly non-compiling specification into `main`.

---

### Task 1: Evaluate and preserve the local-shell desync specification

**Files:**
- Commit on `feat/shell-desync-monitor`: `internal/session/local_shell_desync_test.go`

**Interfaces:**
- Consumes: existing `session.Instance` and `tmux.Session` test helpers.
- Produces: a committed red specification for `detectLocalShellDesync`, `Instance.DetectLocalShellDesync`, `DesyncReasonTmuxMissing`, and `DesyncReasonInstanceIDMismatch` on the feature branch only.

- [ ] **Step 1: Confirm the production symbols do not exist on either mainline**

Run:

```bash
git grep -n -E 'detectLocalShellDesync|DesyncReasonTmuxMissing|DetectLocalShellDesync' main upstream/main -- internal/session
```

Expected: no matches.

- [ ] **Step 2: Run the targeted test to record its current red state**

Run from `.worktrees/feat-shell-desync-monitor` with an isolated temporary directory:

```bash
go test ./internal/session -run 'LocalShellDesync'
```

Expected: compile failure naming the missing production symbols.

- [ ] **Step 3: Review the file for scope and safety**

Confirm it only adds tests, does not mutate user state, covers missing tmux, foreign instance IDs, stopped/remote exclusions, and tmux session-name propagation.

- [ ] **Step 4: Commit the specification on its existing feature branch**

```bash
git add internal/session/local_shell_desync_test.go
git commit -m 'test(session): specify local shell desync detection'
```

Expected: the feature worktree becomes clean; the red specification remains isolated from `main`.

### Task 2: Merge current upstream main into fork main

**Files:**
- Modify: files changed by the 41 upstream commits and any conflict resolutions.
- Add: `docs/superpowers/plans/2026-08-03-sync-upstream-and-preserve-desync-test.md`

**Interfaces:**
- Consumes: `upstream/main` at `4630080726ddf99885e1d3d190ffcd2e25d18683` or the freshly fetched successor.
- Produces: a merge commit on local `main` containing both upstream changes and all fork-specific commits.

- [ ] **Step 1: Commit this execution plan**

```bash
git add -f docs/superpowers/plans/2026-08-03-sync-upstream-and-preserve-desync-test.md
git commit -m 'docs(git): plan upstream synchronization'
```

- [ ] **Step 2: Merge without rewriting history**

```bash
git merge --no-ff upstream/main
```

Expected: a merge commit, with conflicts resolved in favor of retaining compatible upstream improvements and fork-specific provider/session/hub behavior.

- [ ] **Step 3: Inspect the complete merge diff and repository status**

```bash
git diff --check HEAD^1..HEAD
git status --short --branch
```

Expected: no whitespace errors and no uncommitted main-worktree changes.

### Task 3: Verify and publish the synchronized fork

**Files:**
- Test only; no planned source edits.

**Interfaces:**
- Consumes: merged local `main`.
- Produces: a tested and pushed `origin/main` matching local `main` and containing `upstream/main`.

- [ ] **Step 1: Run the full Go test suite**

```bash
go test -count=1 ./...
```

Expected: exit 0.

- [ ] **Step 2: Run lint, vulnerability, and workflow checks**

```bash
make lint
govulncheck ./...
uvx zizmor .github/workflows
```

Expected: lint and vulnerability checks exit 0. Record any pre-existing zizmor findings precisely; fix only merge-introduced regressions in this task.

- [ ] **Step 3: Push main and verify remote ancestry**

```bash
git push origin main
git fetch origin upstream
git rev-list --left-right --count main...origin/main
git merge-base --is-ancestor upstream/main main
```

Expected: `main...origin/main` is `0 0`, and upstream is an ancestor of fork main.

