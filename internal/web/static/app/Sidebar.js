// Sidebar.js -- REWRITE. Status filters + groups + sessions list.
//
// Drops the old Tailwind Sidebar (still present in SessionList.js / SessionRow.js
// / GroupRow.js but no longer mounted). New design: bundle's `.sidebar` class
// stack with side-head / side-filter / side-list / sess rows.
//
// Action handlers route through apiFetch; mutations gated by mutationsEnabledSignal.
import { html } from 'htm/preact'
import { useState, useMemo } from 'preact/hooks'
import { Icon, ICONS, Dot, kindSigil } from './icons.js'
import { menuModelSignal } from './dataModel.js'
import {
  sessionsSignal, selectedIdSignal, terminalModeSignal, mutationsEnabledSignal, confirmDialogSignal,
  createSessionDialogSignal, editSessionDialogSignal,
  moveSessionDialogSignal, promptSessionDialogSignal, sendOutputDialogSignal, notesSessionDialogSignal,
  pathsSessionDialogSignal,
  forkSessionDialogSignal,
  groupNameDialogSignal, groupMoveDialogSignal,
} from './state.js'
import { statusFiltersSignal, showColsSignal, activeTabSignal } from './uiState.js'
import { apiFetch } from './api.js'
import { addToast } from './Toast.js'
import { refreshMenuSnapshot } from './menuRefresh.js'

const STATUS_CHIPS = [
  { id: 'running', sym: '●' },
  { id: 'waiting', sym: '◐' },
  { id: 'error',   sym: '✕' },
  { id: 'idle',    sym: '○' },
]

const SHOW_COL_OPTIONS = [
  { id: 'tool',     label: 'Tool badge' },
  { id: 'cost',     label: 'Cost' },
  { id: 'branch',   label: 'Git branch' },
  { id: 'attach',   label: 'MCPs / skills' },
  { id: 'sandbox',  label: 'Docker / worktree' },
  { id: 'lastSeen', label: 'Last activity' },
]

function optimisticRemoveSessionFromMenu(id) {
  const before = sessionsSignal.value || []
  let removedGroup = ''
  const next = []
  for (const item of before) {
    if (item?.type === 'session' && item.session?.id === id) {
      removedGroup = item.session.groupPath || item.path || ''
      continue
    }
    next.push(item)
  }
  if (next.length === before.length) return () => {}
  sessionsSignal.value = next.map(item => {
    if (item?.type !== 'group' || !item.group || item.group.path !== removedGroup) return item
    return {
      ...item,
      group: {
        ...item.group,
        sessionCount: Math.max(0, (item.group.sessionCount || 0) - 1),
      },
    }
  })
  return () => { sessionsSignal.value = before }
}

function doAction(action, s) {
  if (!mutationsEnabledSignal.value) {
    addToast('mutations disabled')
    return
  }
  const id = s.id
  const mutate = (method, path, body) => apiFetch(method, path, body)
    .then(() => refreshMenuSnapshot())
    .catch(() => {})
  if (action === 'start')   return mutate('POST', `/api/sessions/${id}/start`)
  if (action === 'stop')    return mutate('POST', `/api/sessions/${id}/stop`)
  if (action === 'close')   return mutate('POST', `/api/sessions/${id}/close`)
  if (action === 'restart') return mutate('POST', `/api/sessions/${id}/restart`)
  if (action === 'restartFresh') return mutate('POST', `/api/sessions/${id}/restart-fresh`)
  if (action === 'toggleYolo') return mutate('POST', `/api/sessions/${id}/toggle-yolo`)
  if (action === 'unread') return mutate('POST', `/api/sessions/${id}/unread`)
  if (action === 'approve') return mutate('POST', `/api/sessions/${id}/approve`)
  if (action === 'sandboxShell') {
    selectedIdSignal.value = id
    terminalModeSignal.value = 'sandbox'
    activeTabSignal.value = 'terminal'
    return
  }
  if (action === 'fork') {
    forkSessionDialogSignal.value = { sessionId: id }
    return
  }
  if (action === 'remove') {
    confirmDialogSignal.value = {
      message: `Remove session "${s.title}" from the registry? This only works for stopped/error sessions and does not kill active work.`,
      onConfirm: () => apiFetch('POST', `/api/sessions/${id}/remove`)
        .then(() => {
          if (selectedIdSignal.value === id) {
            selectedIdSignal.value = null
            if (window.location.pathname.startsWith('/s/')) {
              history.replaceState(null, '', '/')
            }
          }
          return refreshMenuSnapshot()
        })
        .catch(() => {}),
    }
  }
  if (action === 'archive') {
    confirmDialogSignal.value = {
      message: `Archive session "${s.title}"? The process will be stopped and hidden from the active list.`,
      onConfirm: () => apiFetch('POST', `/api/sessions/${id}/archive`)
        .then(() => {
          if (selectedIdSignal.value === id) {
            selectedIdSignal.value = null
            if (window.location.pathname.startsWith('/s/')) {
              history.replaceState(null, '', '/')
            }
          }
          return refreshMenuSnapshot()
        })
        .catch(() => {}),
    }
  }
  if (action === 'delete') {
    confirmDialogSignal.value = {
      message: `Delete session "${s.title}"? This stops the tmux session and removes metadata.`,
      onConfirm: () => {
        const rollback = optimisticRemoveSessionFromMenu(id)
        const previousSelected = selectedIdSignal.value
        if (selectedIdSignal.value === id) {
          selectedIdSignal.value = null
          if (window.location.pathname.startsWith('/s/')) {
            history.replaceState(null, '', '/')
          }
        }
        return apiFetch('DELETE', `/api/sessions/${id}`)
          .then(() => refreshMenuSnapshot())
          .catch(() => {
            rollback()
            if (previousSelected === id) selectedIdSignal.value = id
          })
      },
    }
  }
  if (action === 'worktreeFinish') {
    // Issue #1126 — POST /api/sessions/{id}/worktree/finish. Mirrors TUI
    // W/shift+w. Body left empty so the backend auto-detects target
    // branch and uses default flags (merge + delete branch).
    const branch = s.worktreeBranch || s.branch
    confirmDialogSignal.value = {
      message: `Finish worktree for "${s.title}"? Merges branch "${branch}" into default branch, removes worktree, deletes branch, and removes session.`,
      onConfirm: () => mutate('POST', `/api/sessions/${id}/worktree/finish`),
    }
  }
  if (action === 'edit') {
    editSessionDialogSignal.value = { sessionId: id }
  }
  if (action === 'move') {
    moveSessionDialogSignal.value = { sessionId: id }
  }
  if (action === 'prompt') {
    promptSessionDialogSignal.value = { sessionId: id }
  }
  if (action === 'sendOutput') {
    sendOutputDialogSignal.value = { sourceSessionId: id }
  }
  if (action === 'notes') {
    notesSessionDialogSignal.value = { sessionId: id }
  }
  if (action === 'paths') {
    pathsSessionDialogSignal.value = { sessionId: id }
  }
}

function groupEndpoint(path, suffix = '') {
  return `/api/groups/${encodeURIComponent(path)}${suffix}`
}

function groupDefaultPath(g) {
  return g?.isHub ? (g.hubGroupPath || '') : (g?.path || '')
}

function isDefaultGroup(g) {
  return groupDefaultPath(g) === 'my-sessions'
}

function doGroupAction(action, g) {
  if (!mutationsEnabledSignal.value) {
    addToast('mutations disabled')
    return
  }
  if (!g || !g.path) return
  if (action === 'create') {
    groupNameDialogSignal.value = { mode: 'create', parentPath: g.path }
    return
  }
  if (action === 'rename') {
    groupNameDialogSignal.value = { mode: 'rename', groupPath: g.path, currentName: g.name || g.label || g.path }
    return
  }
  if (action === 'move') {
    groupMoveDialogSignal.value = { groupPath: g.path }
    return
  }
  if (action === 'up' || action === 'down') {
    return apiFetch('POST', groupEndpoint(g.path, '/reorder'), { direction: action })
      .then(() => refreshMenuSnapshot())
      .catch(() => {})
  }
  if (action === 'delete') {
    confirmDialogSignal.value = {
      message: `Delete group "${g.name || g.label || g.path}"? Sessions move to the default group.`,
      onConfirm: () => apiFetch('DELETE', groupEndpoint(g.path))
        .then(() => refreshMenuSnapshot())
        .catch(() => {}),
    }
  }
}

function SessionItem({ s, sel, onSelect, showCols }) {
  const [exp, setExp] = useState(false)
  const mcpCount = (s.mcps || []).length
  const skillCount = (s.skills || []).length
  const hasSubline =
    (showCols.branch && s.branch && s.branch !== '—') ||
    (showCols.attach && (mcpCount > 0 || skillCount > 0)) ||
    (showCols.sandbox && (s.sandbox || s.worktree)) ||
    showCols.lastSeen
  return html`
    <div class=${`sess ${sel ? 'sel' : ''} ${s.kind} ${exp ? 'exp' : ''}`} onClick=${() => onSelect(s.id)}>
      <span class="sig">${kindSigil(s.kind)}</span>
      <div class="titleline">
        <${Dot} status=${s.status}/>
        <span class="tt">${s.title}</span>
      </div>
      <div class="meta">
        ${s.isHub && html`<span class="tag">hub:${s.hubNodeName || s.hubNodeId}</span>`}
        ${showCols.tool && s.tool && html`<span class="tag">${s.tool}</span>`}
        ${showCols.cost && s.cost > 0 && html`<span class="cost">$${s.cost.toFixed(2)}</span>`}
        <button class="row-chev" title="Details" onClick=${e => { e.stopPropagation(); setExp(v => !v) }}>
          ${exp ? '▾' : '▸'}
        </button>
      </div>
      ${hasSubline && html`
        <div class="subline">
          ${showCols.branch && s.branch && s.branch !== '—' && html`<span class="trunc"><span class="b">git</span> ${s.branch}</span>`}
          ${showCols.attach && mcpCount > 0 && html`<span class="att-count">${mcpCount} mcp${mcpCount > 1 ? 's' : ''}</span>`}
          ${showCols.attach && skillCount > 0 && html`<span class="att-count skill">${skillCount} skill${skillCount > 1 ? 's' : ''}</span>`}
          ${showCols.sandbox && s.sandbox && html`<span class="att-count warn">docker</span>`}
          ${showCols.sandbox && s.worktree && html`<span class="att-count">worktree</span>`}
        </div>
      `}
      ${exp && html`
        <div class="row-detail" onClick=${e => e.stopPropagation()}>
          <div class="rd-row"><span class="rd-k">tool</span><span class="rd-v">${s.tool || '—'}</span></div>
          ${s.isHub && html`<div class="rd-row"><span class="rd-k">hub</span><span class="rd-v">${s.hubNodeName || s.hubNodeId}</span></div>`}
          ${s.branch && s.branch !== '—' && html`<div class="rd-row"><span class="rd-k">branch</span><span class="rd-v">${s.branch}</span></div>`}
          ${s.path && html`<div class="rd-row"><span class="rd-k">path</span><span class="rd-v" title=${s.path}>${s.path}</span></div>`}
          ${s.cost > 0 && html`<div class="rd-row"><span class="rd-k">cost</span><span class="rd-v ok">$${s.cost.toFixed(2)}</span></div>`}
        </div>
      `}
      <div class="actions" onClick=${e => e.stopPropagation()}>
        ${(s.status === 'running' || s.status === 'waiting')
          ? html`<button class="mini" title="Stop" data-testid="session-stop-btn" onClick=${() => doAction('stop', s)}><${Icon} d=${ICONS.stop} size=${12}/></button>`
          : html`<button class="mini good" title="Start" data-testid="session-start-btn" onClick=${() => doAction('start', s)}><${Icon} d=${ICONS.play} size=${12}/></button>`}
        <button class="mini good" title="Restart" data-testid="session-restart-btn" onClick=${() => doAction('restart', s)}><${Icon} d=${ICONS.restart} size=${12}/></button>
        <button class="mini" title="Prompt without attaching" data-testid="session-prompt-btn" onClick=${() => doAction('prompt', s)}>o</button>
        <button class="mini" title="More actions" data-testid="session-more-btn" aria-haspopup="menu">⋯</button>
        <div class="more-menu" role="menu" data-testid="session-more-menu">
          <button role="menuitem" data-testid="edit-session-btn" onClick=${() => doAction('edit', s)}>Edit</button>
          <button role="menuitem" data-testid="session-notes-btn" onClick=${() => doAction('notes', s)}>Notes</button>
          <button role="menuitem" data-testid="session-send-output-btn" onClick=${() => doAction('sendOutput', s)}>Send output</button>
          <button role="menuitem" data-testid="session-move-btn" onClick=${() => doAction('move', s)}>Move group</button>
          <button role="menuitem" data-testid="session-unread-btn" onClick=${() => doAction('unread', s)}>Mark unread</button>
          <button role="menuitem" data-testid="session-approve-btn" onClick=${() => doAction('approve', s)}>Quick approve</button>
          <button role="menuitem" data-testid="session-close-btn" onClick=${() => doAction('close', s)}>Close process</button>
          ${s.sandbox && html`<button role="menuitem" data-testid="session-sandbox-shell-btn" onClick=${() => doAction('sandboxShell', s)}>Sandbox shell</button>`}
          <button role="menuitem" data-testid="session-restart-fresh-btn" onClick=${() => doAction('restartFresh', s)}>Restart fresh</button>
          ${(s.tool === 'gemini' || s.tool === 'codex' || s.tool === 'hermes') && html`
            <button role="menuitem" data-testid="session-toggle-yolo-btn" onClick=${() => doAction('toggleYolo', s)}>Toggle YOLO</button>
          `}
          ${s.multiRepoEnabled && html`<button role="menuitem" data-testid="session-paths-btn" onClick=${() => doAction('paths', s)}>Edit paths</button>`}
          ${s.canFork && html`<button role="menuitem" class="fork" data-testid="session-fork-btn" onClick=${() => doAction('fork', s)}>Fork</button>`}
          ${!s.isHub && s.worktree && html`<button role="menuitem" onClick=${() => doAction('worktreeFinish', s)} data-action="worktree-finish" data-testid="session-worktree-finish-btn">Finish worktree</button>`}
          <button role="menuitem" data-testid="session-archive-btn" onClick=${() => doAction('archive', s)}>Archive</button>
          <button role="menuitem" class="danger" data-testid="session-remove-btn" onClick=${() => doAction('remove', s)}>Remove metadata</button>
          <button role="menuitem" class="danger" data-testid="session-delete-btn" onClick=${() => doAction('delete', s)}>Delete</button>
        </div>
      </div>
    </div>
  `
}

export function Sidebar() {
  const { groups, byGroup, sessions } = menuModelSignal.value
  const selected = selectedIdSignal.value
  const statusFilters = statusFiltersSignal.value
  const showCols = showColsSignal.value
  const [filter, setFilter] = useState('')
  const [showMenu, setShowMenu] = useState(false)
  const [expanded, setExpanded] = useState(() => Object.fromEntries(groups.map(g => [g.path, g.expanded !== false])))

  const matches = (s) => {
    if (statusFilters.length && !statusFilters.includes(s.status)) return false
    if (!filter) return true
    const t = filter.toLowerCase()
    return ((s.title || '') + ' ' + (s.group || '') + ' ' + (s.path || '') + ' ' + (s.tool || '') + ' ' + (s.branch || ''))
      .toLowerCase().includes(t)
  }

  const totalVisible = useMemo(() => sessions.filter(matches).length, [sessions, filter, statusFilters])
  const toggleStatus = (id) => {
    const cur = statusFiltersSignal.value
    statusFiltersSignal.value = cur.includes(id) ? cur.filter(x => x !== id) : [...cur, id]
  }
  // Open is defined as `expanded[p] !== false` (undefined counts as open: groups
  // arrive after the initial render, so most paths are never seeded). The toggle
  // must mirror that read — plain `!s[p]` maps undefined → true, which is still
  // "open", making the first click on a never-toggled group a silent no-op.
  const toggleGroup = (p) => setExpanded(s => ({ ...s, [p]: s[p] === false }))
  const onSelect = (id) => {
    selectedIdSignal.value = id
    terminalModeSignal.value = 'session'
    activeTabSignal.value = 'terminal'
  }
  const setShowCol = (id) => {
    showColsSignal.value = { ...showCols, [id]: !showCols[id] }
  }

  return html`
    <div class="sidebar">
      <div class="side-head">
        <span class="label">SESSIONS</span>
        <span class="count">${totalVisible}</span>
        <div class="spacer"/>
        <div style="position: relative;">
          <button class=${`icon-btn ${showMenu ? 'active' : ''}`} title="Show columns" aria-label="Show columns"
                  data-testid="show-cols-btn"
                  onClick=${() => setShowMenu(m => !m)}>
            <${Icon} d=${ICONS.filter}/>
          </button>
          ${showMenu && html`
            <div class="show-menu" data-testid="show-cols-menu" onClick=${e => e.stopPropagation()}>
              <div class="sm-head">SHOW IN ROW</div>
              ${SHOW_COL_OPTIONS.map(c => html`
                <label key=${c.id} class="sm-row" data-testid=${`show-col-${c.id}`}>
                  <input type="checkbox" checked=${!!showCols[c.id]} onChange=${() => setShowCol(c.id)}/>
                  <span>${c.label}</span>
                </label>
              `)}
              <div class="sm-foot" onClick=${() => setShowMenu(false)}>done</div>
            </div>
          `}
        </div>
        ${mutationsEnabledSignal.value && html`
          <button class="icon-btn" title="New session (n)" aria-label="New session"
                  onClick=${() => (createSessionDialogSignal.value = true)}>
            <${Icon} d=${ICONS.plus}/>
          </button>
        `}
      </div>
      <div class="side-filter">
        <input
          placeholder="/ filter"
          data-testid="sidebar-filter-input"
          value=${filter}
          onInput=${e => setFilter(e.target.value)}
        />
        ${STATUS_CHIPS.map(s => html`
          <span key=${s.id}
                class=${`side-chip ${statusFilters.includes(s.id) ? 'on' : ''}`}
                data-testid=${`status-chip-${s.id}`}
                onClick=${() => toggleStatus(s.id)}
                title=${s.id}>
            ${s.sym}
          </span>
        `)}
      </div>
      <div class="side-list">
        ${groups.map(g => {
          const members = (byGroup[g.path] || []).filter(matches)
          if (filter && members.length === 0) return null
          const open = expanded[g.path] !== false
          return html`
            <div key=${g.path}>
              <div class=${`side-group-head ${g.kind || ''}`} data-testid=${`group-head-${g.path}`} onClick=${() => toggleGroup(g.path)}>
                <span class="chev">${open ? '▾' : '▸'}</span>
                <span class="name">${g.label}</span>
                <span class="badge">(${members.length})</span>
                ${mutationsEnabledSignal.value && html`
                  <span class="group-actions" onClick=${e => e.stopPropagation()}>
                    <button class="mini" title="New subgroup" data-testid="group-create-btn" onClick=${() => doGroupAction('create', g)}>+</button>
                    <button class="mini" title="Rename group" data-testid="group-rename-btn" onClick=${() => doGroupAction('rename', g)}>r</button>
                    ${!isDefaultGroup(g) && html`
                      <button class="mini" title="Move group" data-testid="group-move-btn" onClick=${() => doGroupAction('move', g)}>M</button>
                    `}
                    <button class="mini" title="Move group up" data-testid="group-reorder-up-btn" onClick=${() => doGroupAction('up', g)}>↑</button>
                    <button class="mini" title="Move group down" data-testid="group-reorder-down-btn" onClick=${() => doGroupAction('down', g)}>↓</button>
                    ${!isDefaultGroup(g) && html`
                      <button class="mini danger" title="Delete group" data-testid="group-delete-btn" onClick=${() => doGroupAction('delete', g)}>×</button>
                    `}
                  </span>
                `}
              </div>
              ${open && members.map(s => html`
                <${SessionItem} key=${s.id} s=${s} sel=${selected === s.id} onSelect=${onSelect} showCols=${showCols}/>
              `)}
            </div>
          `
        })}
        ${sessions.length === 0 && html`
          <div style="padding: 16px; font-family: var(--mono); font-size: 11px; color: var(--muted); text-align: center;">
            No sessions yet. Press <span class="kbd" style="border:1px solid var(--border); padding: 0 4px; border-radius: 3px;">n</span> to create one.
          </div>
        `}
      </div>
    </div>
  `
}
