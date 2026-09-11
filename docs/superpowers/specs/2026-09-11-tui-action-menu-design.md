# TUI Action Menu and Optional Shortcuts

Date: 2026-09-11

## Problem

Agent Deck exposes too many single-key commands directly from the session list.
The same key can also mean different things in different contexts, while the
help overlay and footer try to explain a growing set of bindings. This makes the
TUI difficult to learn and makes accidental actions too easy.

The user asked for the TUI shortcuts to be replaced by menus, with a shortcut
settings menu that can enable each shortcut individually. The user also called
out overloaded keys and the need for a global menu that does not depend on
hovering or selecting an empty area.

## Goals

- Make a global action menu the primary command surface.
- Make the menu open from anywhere in the overview, whether or not a row is
  selected.
- Put actions for the selected session, group, remote node, or hub node at the
  top of that global menu when applicable.
- Keep only the controls required to navigate, activate, dismiss, open the menu,
  and detach from an attached session always available.
- Let users enable or disable every other shortcut independently.
- Prevent enabled shortcuts from colliding.
- Derive menu entries, shortcut settings, dispatch, footer hints, and help text
  from one action catalog so their names and availability cannot drift.
- Preserve explicitly configured `[hotkeys]` bindings.

## Non-goals

- Reworking the behavior of the underlying session, group, hub, or remote
  operations.
- Adding new agent operations.
- Replacing keyboard navigation with mouse-only interaction.
- Intercepting another new control chord while attached to an agent. The
  existing detach chord remains the guaranteed route back to the overview.
- Removing the existing `[hotkeys]` configuration format.

## Interaction model

### Global menu

The overview header and footer expose a visible `Menu` affordance. It is
clickable, and `Space` opens the same menu from anywhere in the overview. The
menu does not depend on hovering an empty area or on a valid selection.

The menu contains:

1. `Selected …` actions when the current row has applicable actions.
2. `Sessions` actions such as new, search, import, and reload.
3. `Manage` actions such as groups, MCPs, plugins, skills, watchers, and hub
   management when supported by the current state.
4. `View` actions such as preview visibility, layout, group ordering, filters,
   and archived sessions.
5. `Application` actions such as settings, keyboard shortcuts, help, update,
   and quit.

The menu is a single searchable list with category headings, avoiding another
navigation level. Typing filters it, Up/Down changes selection, Enter runs the
highlighted action, and Escape closes it. Disabled actions remain visible only
when their explanation helps the user understand why they are unavailable.

### Context actions

Context changes which actions appear; it never changes whether the global menu
can open. A selected session gets actions such as open, prompt, rename, restart,
fork, archive, move, copy, and delete. A group gets group-specific actions. A
remote or hub row gets only operations that are actually supported for that row.

Menu entries have stable action identifiers. The menu invokes an action by its
identifier, not by synthesizing the action's shortcut. This keeps menu behavior
available when the corresponding shortcut is disabled and removes shortcut
overloading from the command model.

### Always-available controls

The following controls are structural rather than optional action shortcuts:

- Arrow keys move through lists and menus.
- Enter activates the highlighted row or menu item.
- Escape dismisses the current overlay or backs out one level.
- Space opens the global menu from the overview.
- The configured attached-session detach chord returns to the overview.

Existing navigation aliases such as `j` and `k` become optional shortcuts in
menu-first mode rather than hidden mandatory behavior.

### Shortcut settings

`Application > Keyboard shortcuts` opens a searchable list grouped the same way
as the global menu. Each row shows:

- action name;
- enabled/disabled state;
- configured key;
- context summary;
- conflict or validation state.

Space toggles the highlighted shortcut. Enter captures a replacement key.
Escape cancels capture or leaves the screen. Saving writes the existing
`[hotkeys]` table: an enabled shortcut stores its key, while a disabled shortcut
stores an empty string. Explicit user bindings remain enabled after upgrade.

The screen provides two explicit presets:

- `Menu-first`: disable every optional shortcut.
- `Legacy`: restore the historical default shortcut set where it does not
  conflict with structural menu controls. Because `Space` is reserved for the
  global menu, legacy jump mode uses `Alt+Space`.

Applying a preset is previewed and requires confirmation because it changes many
bindings. Individual changes apply without a confirmation dialog.

### Defaults and migration

The effective default becomes menu-first for installations with no explicit
shortcut choices. Existing non-empty `[hotkeys]` entries are treated as explicit
enabled choices. Existing empty entries remain disabled. The application writes
a shortcut mode/version marker only when the user saves shortcut settings, so
loading older configuration remains lossless.

On first launch after the change, a short non-modal hint says `Space: Menu` and
explains that legacy shortcuts can be restored under Keyboard shortcuts. It is
shown once.

## Architecture

### Action catalog

Introduce a declarative catalog in `internal/ui` containing stable action IDs,
labels, category, default shortcut, whether the action is structural, an
availability predicate, and an invocation callback or dispatch target.

The catalog is the shared source for:

- global and contextual menu contents;
- shortcut settings rows;
- hotkey lookup and collision detection;
- footer hints;
- help rendering.

Existing command implementations remain in `Home`; the first implementation
extracts action dispatch from the current key switch without changing operation
semantics. Both a menu choice and an enabled shortcut call the same action
dispatcher.

### Menu model

Add a focused Bubble Tea model responsible for filtering, navigation, grouped
categories, rendering, and returning a selected action ID. It receives an
already-evaluated list of action descriptors and does not mutate sessions
directly.

### Shortcut preferences

Extend `UISettings` with a shortcut-mode/version field only if necessary for
unambiguous migration. Continue using `UserConfig.Hotkeys` as the authoritative
per-action binding map. Preference resolution produces both the enabled binding
map and validation diagnostics. Configuration saves must merge with the complete
loaded configuration so unrelated sections are never dropped.

### Input routing

Overlay input remains highest priority. When the action menu or shortcut screen
is visible, keys are consumed there and cannot leak into the home action switch.
Text-entry dialogs continue suppressing single-letter application shortcuts.
The global menu opens before row-specific dispatch, so it also works with no
selected row.

## Error handling

- Duplicate enabled bindings are rejected in the shortcut screen with both
  conflicting action names shown.
- Unsupported key encodings are rejected before saving.
- A configuration-save error keeps the shortcut screen open and displays the
  error without changing the active bindings.
- If an item changes or disappears while its menu is open, availability is
  rechecked at invocation time and the menu reports that the action is no longer
  available.
- Destructive actions continue using their existing confirmation dialogs.

## Testing

Tests will be written before production behavior and must demonstrate:

- Space opens the global menu with and without a selected row.
- Context actions match session, group, hub, remote, and empty selections.
- Menu invocation works when the corresponding shortcut is disabled.
- Disabled shortcuts do not dispatch from the overview.
- Existing explicit bindings remain enabled and empty bindings remain disabled.
- Menu-first and legacy resolution produce the intended binding sets.
- Duplicate bindings and unsupported bindings cannot be saved.
- Shortcut changes round-trip through `config.toml` without dropping unrelated
  sections.
- Menu and shortcut overlays consume keys without leaking actions behind them.
- Footer and help show only enabled shortcuts and always advertise the menu.
- Existing session and group actions retain their behavior when invoked through
  the action dispatcher.

The implementation will also run the repository's isolated-home test suite,
lint, vulnerability checks, and the contributor self-check before review.

## Delivery

The work will be committed on `feat/tui-action-menu`, reviewed against the full
diff, pushed to the user's fork, merged only after required checks and review
conversations are resolved, released as a new version, installed locally, and
deployed to the Agent Deck hub image where applicable.
