# Fleet Session Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `/` search all known Agent Deck sessions by default, toggle to machine-local sessions with `Tab`, and open the selected session with `Enter`.

**Architecture:** Replace the local-instance-only search rows with fleet-aware result values that retain exact local, SSH-remote, or hub identity. Build the global snapshot from Home's existing in-memory inventories and dispatch selection through the existing activation helpers; do not enable the transcript index.

**Tech Stack:** Go, Bubble Tea, existing Agent Deck session/hub models and Go test suite.

## Global Constraints

- Global means local, configured SSH-remote, and hub sessions currently known to the TUI.
- Local means sessions owned by the current machine only.
- Ordinary `/` defaults to Global; `Alt+/` remains group-scoped Local.
- No disk transcript index, new watcher, network request, or dependency is introduced.
- `Enter` reuses the established local/remote/hub activation behavior.

---

### Task 1: Fleet-aware search model and scope toggle

**Files:**
- Modify: `internal/ui/search.go`
- Modify: `internal/ui/search_test.go`

**Interfaces:**
- Produces: `SessionSearchResult`, constructors for local/remote/hub rows, `Search.SetFleetItems`, `Search.ShowGlobal`, `Search.ShowLocal`, and `Search.Selected`.
- Consumes: `session.FilterByQuery` through lightweight searchable instances so local fuzzy/status matching semantics remain intact.

- [ ] **Step 1: Write failing tests** proving Global is the explicit scope, `Tab` switches between global and local pools, host fields participate in matching/rendering, and selection retains its exact source identity.
- [ ] **Step 2: Run** `go test ./internal/ui -run 'TestSearch' -count=1` and confirm the new assertions fail because the search model only contains local instances.
- [ ] **Step 3: Implement** the result model, dual item pools, scope switch, generic filtering adapter, and host-aware rendering in `internal/ui/search.go`.
- [ ] **Step 4: Re-run** `go test ./internal/ui -run 'TestSearch' -count=1` and confirm it passes.

### Task 2: Global inventory and direct activation

**Files:**
- Create: `internal/ui/fleet_search.go`
- Create: `internal/ui/fleet_search_test.go`
- Modify: `internal/ui/home.go`
- Modify: `internal/ui/home_test.go`
- Modify: `internal/ui/group_nav.go`

**Interfaces:**
- Consumes: Task 1's `SessionSearchResult` constructors and search scope methods.
- Produces: `Home.fleetSearchResults()`, `Home.openFleetSearch()`, and `Home.activateSearchResult(*SessionSearchResult) tea.Cmd`.

- [ ] **Step 1: Write failing tests** proving `fleetSearchResults` returns one correctly identified row for each local, SSH-remote, and non-local hub session; `/` opens Global; `Tab` leaves only local rows; `Alt+/` stays scoped Local; and `Enter` on a hub result returns an attach command and marks the TUI as attaching.
- [ ] **Step 2: Run** `go test ./internal/ui -run 'TestFleetSearch|TestHomeSearch|TestGroupNav_AltSlash' -count=1` and confirm failures are caused by the missing fleet collection and activation behavior.
- [ ] **Step 3: Implement** fleet snapshot collection under the existing mutexes, ordinary/global and group/local opening helpers, and activation dispatch to `activateLocalSession`, `attachRemoteSession`, and `attachHubSession`.
- [ ] **Step 4: Re-run** the focused test command and confirm it passes.

### Task 3: Regression verification and commit

**Files:**
- Verify all files changed in Tasks 1 and 2.

**Interfaces:**
- Consumes: the completed fleet-aware search and activation flow.
- Produces: a tested, reviewable Conventional Commit.

- [ ] **Step 1: Run** `gofmt -w internal/ui/search.go internal/ui/search_test.go internal/ui/fleet_search.go internal/ui/fleet_search_test.go internal/ui/home.go internal/ui/home_test.go internal/ui/group_nav.go`.
- [ ] **Step 2: Run** `go test ./internal/ui -count=1`.
- [ ] **Step 3: Run** `go test ./internal/session ./internal/hub -count=1` to cover the models and transports the search result identities depend on.
- [ ] **Step 4: Inspect** `git diff --check`, `git diff --stat`, and `git status --short`; confirm no generated or temporary artifacts are present.
- [ ] **Step 5: Commit** with `fix(ui): search and open sessions across the fleet`.
