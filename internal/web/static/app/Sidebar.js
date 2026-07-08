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
  selectedIdSignal, mutationsEnabledSignal, confirmDialogSignal,
  createSessionDialogSignal, editSessionDialogSignal,
  moveSessionDialogSignal, promptSessionDialogSignal, notesSessionDialogSignal,
  pathsSessionDialogSignal,
} from './state.js'
import { statusFiltersSignal, showColsSignal, activeTabSignal } from './uiState.js'
import { apiFetch } from './api.js'
import { addToast } from './Toast.js'

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

function doAction(action, s) {
  if (!mutationsEnabledSignal.value) {
    addToast('mutations disabled')
    return
  }
  const id = s.id
  if (action === 'start')   return apiFetch('POST', `/api/sessions/${id}/start`).catch(() => {})
  if (action === 'stop')    return apiFetch('POST', `/api/sessions/${id}/stop`).catch(() => {})
  if (action === 'close')   return apiFetch('POST', `/api/sessions/${id}/close`).catch(() => {})
  if (action === 'restart') return apiFetch('POST', `/api/sessions/${id}/restart`).catch(() => {})
  if (action === 'restartFresh') return apiFetch('POST', `/api/sessions/${id}/restart-fresh`).catch(() => {})
  if (action === 'toggleYolo') return apiFetch('POST', `/api/sessions/${id}/toggle-yolo`).catch(() => {})
  if (action === 'unread') return apiFetch('POST', `/api/sessions/${id}/unread`).catch(() => {})
  if (action === 'approve') return apiFetch('POST', `/api/sessions/${id}/approve`).catch(() => {})
  if (action === 'fork')    return apiFetch('POST', `/api/sessions/${id}/fork`, { title: s.title + '-fork' }).catch(() => {})
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
        })
        .catch(() => {}),
    }
  }
  if (action === 'delete') {
    confirmDialogSignal.value = {
      message: `Delete session "${s.title}"? This stops the tmux session and removes metadata.`,
      onConfirm: () => apiFetch('DELETE', `/api/sessions/${id}`).catch(() => {}),
    }
  }
  if (action === 'worktreeFinish') {
    // Issue #1126 — POST /api/sessions/{id}/worktree/finish. Mirrors TUI
    // W/shift+w. Body left empty so the backend auto-detects target
    // branch and uses default flags (merge + delete branch).
    const branch = s.worktreeBranch || s.branch
    confirmDialogSignal.value = {
      message: `Finish worktree for "${s.title}"? Merges branch "${branch}" into default branch, removes worktree, deletes branch, and removes session.`,
      onConfirm: () => apiFetch('POST', `/api/sessions/${id}/worktree/finish`).catch(() => {}),
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
  if (action === 'notes') {
    notesSessionDialogSignal.value = { sessionId: id }
  }
  if (action === 'paths') {
    pathsSessionDialogSignal.value = { sessionId: id }
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
        <button class="mini good" title="Restart fresh" data-testid="session-restart-fresh-btn" onClick=${() => doAction('restartFresh', s)}>T</button>
        ${(s.tool === 'gemini' || s.tool === 'codex' || s.tool === 'hermes') && html`
          <button class="mini" title="Toggle YOLO / auto-approve" data-testid="session-toggle-yolo-btn" onClick=${() => doAction('toggleYolo', s)}>YOLO</button>
        `}
        <button class="mini" title="Close process; keep metadata" data-testid="session-close-btn" onClick=${() => doAction('close', s)}>D</button>
        <button class="mini" title="Mark unread / needs attention" data-testid="session-unread-btn" onClick=${() => doAction('unread', s)}>u</button>
        <button class="mini" title="Quick approve Claude prompt" data-testid="session-approve-btn" onClick=${() => doAction('approve', s)}>a</button>
        <button class="mini" title="Edit notes" data-testid="session-notes-btn" onClick=${() => doAction('notes', s)}>e</button>
        <button class="mini" title="Prompt without attaching" data-testid="session-prompt-btn" onClick=${() => doAction('prompt', s)}>o</button>
        <button class="mini" title="Move to group" data-testid="session-move-btn" onClick=${() => doAction('move', s)}>M</button>
        ${s.multiRepoEnabled && html`<button class="mini" title="Edit multi-repo paths" data-testid="session-paths-btn" onClick=${() => doAction('paths', s)}>p</button>`}
        <button class="mini" title="Edit" data-testid="edit-session-btn" onClick=${() => doAction('edit', s)}><${Icon} d=${ICONS.edit} size=${12}/></button>
        ${s.canFork && html`<button class="mini fork" title="Fork" data-testid="session-fork-btn" onClick=${() => doAction('fork', s)}><${Icon} d=${ICONS.fork} size=${12}/></button>`}
        ${!s.isHub && s.worktree && html`<button class="mini" title="Finish worktree (merge + cleanup)" onClick=${() => doAction('worktreeFinish', s)} data-action="worktree-finish" data-testid="session-worktree-finish-btn">⎇✓</button>`}
        <button class="mini" title="Archive" onClick=${() => doAction('archive', s)}>⌂</button>
        <button class="mini danger" title="Remove metadata only" data-testid="session-remove-btn" onClick=${() => doAction('remove', s)}>X</button>
        <button class="mini danger" title="Delete" data-testid="session-delete-btn" onClick=${() => doAction('delete', s)}><${Icon} d=${ICONS.trash} size=${12}/></button>
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
