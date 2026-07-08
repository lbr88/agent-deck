// AppShell.js -- Five-zone layout shell for the redesigned WebUI.
//
// .app grid: [topbar / sidebar . main . rightrail / footer]. Panes switch
// inside .main via activeTabSignal. Overlays (CommandPalette, TweaksPanel,
// CreateSession/Confirm/GroupName dialogs, toasts) mount as siblings.
//
// Preserves existing dialog + toast components (still Tailwind-classed) so
// no functional regression. Restyling those is a follow-up.
import { html } from 'htm/preact'
import { useEffect } from 'preact/hooks'
import { Topbar } from './Topbar.js'
import { Sidebar } from './Sidebar.js'
import { Footer } from './Footer.js'
import { RightRail } from './RightRail.js'
import { MobileTabs } from './MobileTabs.js'
import { CommandPalette } from './CommandPalette.js'
import { TweaksPanel } from './TweaksPanel.js'
import { TerminalPane } from './panes/TerminalPane.js'
import { CostsPane } from './panes/CostsPane.js'
import { FleetPane } from './panes/FleetPane.js'
import { CommandCenterPane } from './panes/CommandCenterPane.js'
import { ArchivedPane } from './panes/ArchivedPane.js'
import { StubPane } from './panes/StubPane.js'
import { SearchPane } from './panes/SearchPane.js'
import { McpPane } from './panes/McpPane.js'
import { SkillsPane } from './panes/SkillsPane.js'
import { Icon, ICONS } from './icons.js'
import { menuModelSignal } from './dataModel.js'
import {
  selectedIdSignal, createSessionDialogSignal, confirmDialogSignal,
  editSessionDialogSignal, moveSessionDialogSignal, promptSessionDialogSignal,
  notesSessionDialogSignal, groupNameDialogSignal, mutationsEnabledSignal, infoDrawerOpenSignal,
  profilesSignal, systemStatsSignal,
  toolFilterSignal, visibleToolsSignal, toolFilterFallbackSignal,
  hiddenToolsSignal, pickerToolsSignal,
} from './state.js'
import {
  activeTabSignal, paletteOpenSignal, tweaksOpenSignal,
  railSignal, profileSignal,
} from './uiState.js'
import { CreateSessionDialog } from './CreateSessionDialog.js'
import { EditSessionDialog } from './EditSessionDialog.js'
import { MoveSessionDialog } from './MoveSessionDialog.js'
import { PromptSessionDialog } from './PromptSessionDialog.js'
import { NotesSessionDialog } from './NotesSessionDialog.js'
import { ConfirmDialog } from './ConfirmDialog.js'
import { GroupNameDialog } from './GroupNameDialog.js'
import { ToastContainer, addToast } from './Toast.js'
import { ToastHistoryDrawer } from './ToastHistoryDrawer.js'
import { SettingsPanel } from './SettingsPanel.js'
import { KeyboardShortcuts } from './KeyboardShortcuts.js'
import { apiFetch } from './api.js'
import { refreshMenuSnapshot } from './menuRefresh.js'
import { shortcutsOverlaySignal, jumpModeSignal } from './state.js'

function WorkHead() {
  const { sessions } = menuModelSignal.value
  const selected = selectedIdSignal.value
  const session = sessions.find(s => s.id === selected) || sessions[0]
  if (!session) return null

  const kindLabel = (session.kind || 'agent').toUpperCase()
  const profile = profileSignal.value || ''
  const canMutate = mutationsEnabledSignal.value
  const scopeLabel = session.isHub ? `hub:${session.hubNodeName || session.hubNodeId}` : profile
  const modelLabel = session.model
    ? `${session.model}${session.modelVersion ? ` ${session.modelVersion}` : ''}`
    : ''

  const action = (verb) => {
    if (!canMutate) return
    if (verb === 'fork') return apiFetch('POST', `/api/sessions/${session.id}/fork`, { title: session.title + '-fork' }).catch(() => {})
    return apiFetch('POST', `/api/sessions/${session.id}/${verb}`).catch(() => {})
  }
  const supportsYolo = session.tool === 'gemini' || session.tool === 'codex' || session.tool === 'hermes'

  return html`
    <div class="work-head">
      <div class="path">
        <span class=${`kind ${session.kind || ''}`}>${kindLabel}</span>
        ${scopeLabel && html`<span class="seg">${scopeLabel} /</span>`}
        <span class="seg">${session.group || 'default'} /</span>
        <span class="cur">${session.title}</span>
      </div>
      <span class=${`status-chip ${session.status}`}><span class="d"/>${session.status}</span>
      ${modelLabel && html`<span class="status-chip model" title=${session.modelId || modelLabel}>${modelLabel}</span>`}
      <span class="spacer"/>
      ${canMutate && html`
        <div class="actions">
          ${(session.status === 'running' || session.status === 'waiting')
            ? html`<button class="btn ghost" onClick=${() => action('stop')}><${Icon} d=${ICONS.stop} size=${12}/>Stop</button>`
            : html`<button class="btn ghost" onClick=${() => action('start')}><${Icon} d=${ICONS.play} size=${12}/>Start</button>`}
          <button class="btn ghost" onClick=${() => action('restart')}><${Icon} d=${ICONS.restart} size=${12}/>Restart</button>
          <button class="btn ghost" onClick=${() => action('restart-fresh')}>Fresh</button>
          ${supportsYolo && html`<button class="btn ghost" onClick=${() => action('toggle-yolo')}>YOLO</button>`}
          <button class="btn ghost" onClick=${() => action('unread')}>Unread</button>
          <button class="btn ghost" onClick=${() => action('approve')}>Approve</button>
          <button class="btn ghost" onClick=${() => (notesSessionDialogSignal.value = { sessionId: session.id })}>Notes</button>
          <button class="btn ghost" onClick=${() => (promptSessionDialogSignal.value = { sessionId: session.id })}>Prompt</button>
          <button class="btn ghost" onClick=${() => (moveSessionDialogSignal.value = { sessionId: session.id })}>Move</button>
          ${session.canFork && html`<button class="btn" onClick=${() => action('fork')}><${Icon} d=${ICONS.fork} size=${12}/>Fork</button>`}
          <button class="btn primary" onClick=${() => (createSessionDialogSignal.value = true)}>
            <${Icon} d=${ICONS.plus} size=${12}/>New <span class="kbd">n</span>
          </button>
        </div>
      `}
    </div>
  `
}

// Pane switcher — TerminalPane is ALWAYS rendered and only hidden via CSS
// when another tab is active. This preserves the xterm.js + WebSocket lifecycle
// across tab switches; unmounting would trigger a reconnect storm and lose
// scrollback. Other panes are cheap enough to mount/unmount on demand.
function Panes({ tab }) {
  return html`
    <div style=${{ display: tab === 'terminal' ? 'flex' : 'none', flex: 1, minHeight: 0, flexDirection: 'column' }}>
      <${TerminalPane}/>
    </div>
    ${tab === 'command-center' && html`<${CommandCenterPane}/>`}
    ${tab === 'fleet'     && html`<${FleetPane}/>`}
    ${tab === 'costs'     && html`<${CostsPane}/>`}
    ${tab === 'search'    && html`<${SearchPane}/>`}
    ${tab === 'archived'  && html`<${ArchivedPane}/>`}
    ${tab === 'mcp'       && html`<${McpPane}/>`}
    ${tab === 'skills'    && html`<${SkillsPane}/>`}
    ${tab === 'conductor' && html`<${StubPane} title="Conductor"
                              message="Conductor orchestration view is TUI-only. The web API does not expose child topology, bridges, or NEED escalation."/>`}
    ${tab === 'watchers'  && html`<${StubPane} title="Watchers"
                              message="Watcher framework events are routed in the backend; the web API does not surface event streams or routing config."/>`}
  `
}

const JUMP_HINT_ALPHABET = 'asdfghjklwertyuiopzxcvbnm1234567890'

function jumpHintForIndex(index, total) {
  const base = JUMP_HINT_ALPHABET.length
  if (total <= base) return JUMP_HINT_ALPHABET[index]
  const first = Math.floor(index / base)
  const second = index % base
  return `${JUMP_HINT_ALPHABET[first % base]}${JUMP_HINT_ALPHABET[second]}`
}

function jumpSessionHints() {
  const sessions = (menuModelSignal.value?.sessions) || []
  const total = sessions.length
  return sessions.map((session, index) => ({
    session,
    hint: jumpHintForIndex(index, total),
  }))
}

function openJumpSession(session) {
  if (!session) return
  selectedIdSignal.value = session.id
  activeTabSignal.value = 'terminal'
  jumpModeSignal.value = false
}

function JumpOverlay() {
  if (!jumpModeSignal.value) return null
  const hints = jumpSessionHints()
  return html`
    <div class="overlay jump-overlay"
         role="dialog"
         aria-label="Jump to session"
         data-testid="jump-overlay"
         onClick=${() => (jumpModeSignal.value = false)}>
      <div class="dialog jump-dialog" onClick=${e => e.stopPropagation()}>
        <div class="dh">
          <span class="kicker">JUMP</span>
          <div class="t">Jump to session</div>
          <button class="icon-btn" onClick=${() => (jumpModeSignal.value = false)} aria-label="Close jump mode">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          ${hints.length === 0
            ? html`<div class="muted">No sessions to jump to.</div>`
            : html`<div class="jump-list">
                ${hints.map(({ session, hint }) => html`
                  <button key=${session.id}
                          type="button"
                          class=${`jump-row ${selectedIdSignal.value === session.id ? 'sel' : ''}`}
                          data-testid="jump-hint"
                          data-session-id=${session.id}
                          data-hint=${hint}
                          onClick=${() => openJumpSession(session)}>
                    <span class="kbd">${hint}</span>
                    <span class="jump-title">${session.title}</span>
                    <span class="jump-meta">${session.isHub ? `hub:${session.hubNodeName || session.hubNodeId}` : session.group || ''}</span>
                  </button>
                `)}
              </div>`}
          <div class="kshort-foot">Type a hint to open. Esc, q, or Space cancels.</div>
        </div>
      </div>
    </div>
  `
}

async function copyTextToClipboard(text, successMessage) {
  const value = String(text || '').trim()
  if (!value) {
    addToast('Nothing to copy', 'info')
    return false
  }
  try {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      await navigator.clipboard.writeText(value)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = value
      textarea.setAttribute('readonly', 'readonly')
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      textarea.remove()
    }
    addToast(successMessage, 'success')
    return true
  } catch (_err) {
    addToast('Copy failed', 'error')
    return false
  }
}

function sessionInfoText(session) {
  if (!session) return ''
  const raw = session.raw || {}
  const rows = [
    ['Title', session.title],
    ['ID', session.id],
    ['Tool', session.tool],
    ['Status', session.status],
    ['Group', session.group],
    ['Project path', session.path],
    ['Branch', session.branch && session.branch !== '—' ? session.branch : ''],
    ['Model', session.modelId || session.model],
  ]
  if (session.isHub) {
    rows.push(['Hub node', session.hubNodeName || session.hubNodeId])
    rows.push(['Hub session', session.hubSessionId || raw.hubSessionId])
  }
  if (raw.claudeSessionId) rows.push(['Claude session', raw.claudeSessionId])
  if (raw.codexSessionId) rows.push(['Codex session', raw.codexSessionId])
  if (raw.geminiSessionId) rows.push(['Gemini session', raw.geminiSessionId])
  if (raw.opencodeSessionId) rows.push(['OpenCode session', raw.opencodeSessionId])
  return rows
    .filter(([, value]) => value != null && String(value).trim() !== '')
    .map(([key, value]) => `${key}: ${value}`)
    .join('\n')
}

export function AppShell() {
  const activeTab = activeTabSignal.value
  const showCreateSession = createSessionDialogSignal.value
  const showMoveSession = moveSessionDialogSignal.value
  const showPromptSession = promptSessionDialogSignal.value
  const showNotesSession = notesSessionDialogSignal.value
  const confirmData = confirmDialogSignal.value
  const groupNameData = groupNameDialogSignal.value
  const drawerOpen = infoDrawerOpenSignal.value

  // Hide the vanilla .app div from the legacy boot path (kept for back-compat
  // until we delete it).
  useEffect(() => {
    const vanillaApp = document.querySelector('body > .app')
    if (vanillaApp && vanillaApp.id !== 'app-root-grid') vanillaApp.style.display = 'none'
    return () => { if (vanillaApp) vanillaApp.style.display = '' }
  }, [])

  // WEB-P0-4 prevention layer: hydrate webMutations gate from /api/settings.
  // Also hydrates the show_only_installed_tools filter (issue #1259) so the
  // new-session dialog can hide tools whose command is not on PATH.
  useEffect(() => {
    fetch('/api/settings')
      .then(r => r.ok ? r.json() : null)
      .then(data => {
        if (!data) return
        if (typeof data.webMutations === 'boolean') {
          mutationsEnabledSignal.value = data.webMutations
        }
        if (typeof data.toolFilter === 'boolean') {
          toolFilterSignal.value = data.toolFilter
        }
        if (Array.isArray(data.visibleTools)) {
          visibleToolsSignal.value = data.visibleTools
        }
        if (typeof data.toolFilterFallback === 'boolean') {
          toolFilterFallbackSignal.value = data.toolFilterFallback
        }
        if (Array.isArray(data.hiddenTools)) {
          hiddenToolsSignal.value = data.hiddenTools
        }
        if (Array.isArray(data.pickerTools) && data.pickerTools.length > 0) {
          pickerToolsSignal.value = data.pickerTools
        }
      })
      .catch(() => {})
  }, [])

  // Hydrate profilesSignal once. The Topbar reads this for the profile
  // dropdown options and uses the `current` field to seed profileSignal
  // (UI-side selection) on first load.
  useEffect(() => {
    fetch('/api/profiles')
      .then(r => r.ok ? r.json() : null)
      .then(data => {
        if (data && Array.isArray(data.profiles)) {
          profilesSignal.value = data
          if (data.current) profileSignal.value = data.current
        }
      })
      .catch(() => {})
  }, [])

  // Poll /api/system/stats every 5s for the Footer indicators. Stops on
  // unmount; the Footer treats absent fields as "unavailable" so the user
  // sees nothing rather than zeros when a collector is offline.
  useEffect(() => {
    let cancelled = false
    const fetchStats = () => {
      fetch('/api/system/stats')
        .then(r => r.ok ? r.json() : null)
        .then(data => { if (!cancelled && data) systemStatsSignal.value = data })
        .catch(() => {})
    }
    fetchStats()
    const id = setInterval(fetchStats, 5000)
    return () => { cancelled = true; clearInterval(id) }
  }, [])

  // Global keyboard shortcuts — TUI parity, issue #780.
  // Top-10 bindings combined with the existing Web-only ones (Ctrl+K, ]).
  // Guard: any key that isn't a modal-bound modifier combo must NOT fire
  // while the user is typing in an input/textarea/select/contenteditable.
  useEffect(() => {
    let jumpBuffer = ''
    // Navigate selectedIdSignal by `delta` (+1 or -1) through the flat
    // session list from menuModelSignal. Stable across SSE updates because
    // we resolve by ID, not by array index in a possibly-stale snapshot.
    const moveFocus = (delta) => {
      const sessions = (menuModelSignal.value?.sessions) || []
      if (sessions.length === 0) return
      const curId = selectedIdSignal.value
      let idx = sessions.findIndex(s => s.id === curId)
      if (idx === -1) idx = delta > 0 ? -1 : sessions.length
      const next = sessions[Math.max(0, Math.min(sessions.length - 1, idx + delta))]
      if (next) {
        // Only change the selected id; do NOT switch to the terminal tab on
        // j/k navigation. Activating the terminal hands focus to xterm.js,
        // which swallows subsequent keypresses (issue #780 review).
        // The TUI's `enter` key is what opens; j/k just moves focus.
        selectedIdSignal.value = next.id
      }
    }
    const focusedSession = () => {
      const sessions = (menuModelSignal.value?.sessions) || []
      const id = selectedIdSignal.value
      return sessions.find(s => s.id === id) || sessions[0] || null
    }
    const closeAllModals = () => {
      paletteOpenSignal.value = false
      tweaksOpenSignal.value = false
      shortcutsOverlaySignal.value = false
      jumpModeSignal.value = false
      createSessionDialogSignal.value = false
      confirmDialogSignal.value = null
      groupNameDialogSignal.value = null
      editSessionDialogSignal.value = null
      moveSessionDialogSignal.value = null
      promptSessionDialogSignal.value = null
      notesSessionDialogSignal.value = null
      infoDrawerOpenSignal.value = false
    }
    const onKey = (e) => {
      const t = e.target
      const inField = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)
      // Cmd+K / Ctrl+K opens palette anywhere (also works inside inputs).
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        paletteOpenSignal.value = true
        return
      }
      // Ctrl/Cmd+R mirrors the TUI manual refresh without forcing a full page
      // reload. The server snapshot is authoritative and includes hub nodes.
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'r') {
        e.preventDefault()
        refreshMenuSnapshot()
          .then(() => addToast('Session list refreshed', 'success'))
          .catch(() => addToast('Refresh failed', 'error'))
        return
      }
      // Esc unfocuses inputs and closes overlays — fires even while typing.
      if (e.key === 'Escape') {
        if (inField && typeof t.blur === 'function') t.blur()
        closeAllModals()
        return
      }
      if (jumpModeSignal.value) {
        if (e.key === ' ' || e.key === 'q') {
          e.preventDefault()
          jumpBuffer = ''
          jumpModeSignal.value = false
          return
        }
        const key = e.key.length === 1 ? e.key.toLowerCase() : ''
        if (key) {
          e.preventDefault()
          jumpBuffer += key
          const hints = jumpSessionHints()
          const match = hints.find(({ hint }) => hint === jumpBuffer)
          if (match) {
            jumpBuffer = ''
            openJumpSession(match.session)
            return
          }
          if (!hints.some(({ hint }) => hint.startsWith(jumpBuffer))) {
            jumpBuffer = ''
          }
        }
        return
      }
      if (inField) return

      // Shift+Enter: open focused session in new browser tab (web equivalent
      // of the TUI's iTerm "new tab" affordance, issue #1077). Check this
      // BEFORE bare Enter so the shift modifier is honored.
      if (e.key === 'Enter' && e.shiftKey) {
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          const url = `${window.location.pathname}#session=${encodeURIComponent(s.id)}`
          window.open(url, '_blank', 'noopener')
        }
        return
      }
      if (e.key === '?') {
        e.preventDefault()
        shortcutsOverlaySignal.value = !shortcutsOverlaySignal.value
      } else if (e.key === '/') {
        e.preventDefault()
        document.querySelector('.side-filter input')?.focus()
      } else if (e.key === ' ') {
        e.preventDefault()
        jumpBuffer = ''
        jumpModeSignal.value = true
      } else if (e.key === 'j') {
        e.preventDefault(); moveFocus(+1)
      } else if (e.key === 'k') {
        e.preventDefault(); moveFocus(-1)
      } else if (e.key === 'Enter') {
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          selectedIdSignal.value = s.id
          activeTabSignal.value = 'terminal'
        }
      } else if (e.key === 'n' && mutationsEnabledSignal.value) {
        createSessionDialogSignal.value = true
      } else if (e.key === 'r') {
        if (!mutationsEnabledSignal.value) return
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          editSessionDialogSignal.value = { sessionId: s.id }
        }
      } else if (e.key === 'o') {
        if (!mutationsEnabledSignal.value) return
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          promptSessionDialogSignal.value = { sessionId: s.id }
        }
      } else if (e.key === 'e') {
        if (!mutationsEnabledSignal.value) return
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          notesSessionDialogSignal.value = { sessionId: s.id }
        }
      } else if (e.key === 'u') {
        if (!mutationsEnabledSignal.value) return
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          apiFetch('POST', `/api/sessions/${s.id}/unread`).catch(() => {})
        }
      } else if (e.key === 'a') {
        if (!mutationsEnabledSignal.value) return
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          apiFetch('POST', `/api/sessions/${s.id}/approve`).catch(() => {})
        }
      } else if (e.key === 'c' && !e.shiftKey) {
        const detail = { handled: false, text: '' }
        window.dispatchEvent(new CustomEvent('agentdeck:copy-terminal-output', { detail }))
        e.preventDefault()
        copyTextToClipboard(detail.text, 'Copied terminal output')
      } else if (e.key === 'C' || (e.key === 'c' && e.shiftKey)) {
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          copyTextToClipboard(sessionInfoText(s), 'Copied session info')
        }
      } else if (e.key === 'M') {
        if (!mutationsEnabledSignal.value) return
        const s = focusedSession()
        if (s) {
          e.preventDefault()
          moveSessionDialogSignal.value = { sessionId: s.id }
        }
      } else if (e.key === 'D') {
        // Shift+D — non-destructive close of focused session. Mirrors
        // TUI's `D` (closeSession): kills the tmux process but keeps the
        // session record so a later start/restart can resurrect it.
        if (!mutationsEnabledSignal.value) return
        const s = focusedSession()
        if (!s) return
        confirmDialogSignal.value = {
          message: `Close session "${s.title}"? The tmux process will be killed; metadata is preserved.`,
          onConfirm: () => apiFetch('POST', `/api/sessions/${s.id}/close`).catch(() => {}),
        }
      } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z') {
        // Ctrl/Cmd+Z — Chrome-style undo of the most recent delete.
        // Mirrors TUI's ctrl+z (Home.undoStack). The server enforces the
        // configurable undo window (default 30s) and returns 404 once
        // the entry expires; surface the result as a toast either way.
        if (!mutationsEnabledSignal.value) return
        e.preventDefault()
        apiFetch('POST', '/api/sessions/undelete')
          .then(resp => {
            if (resp && resp.sessionId) addToast(`Restored session ${resp.sessionId}`, 'success')
            else addToast('Restored last deleted session', 'success')
          })
          .catch(() => addToast('Nothing to undo', 'info'))
      } else if (e.key === 'q') {
        // Mirrors TUI's `q`: dismiss the current modal/overlay. Only fires
        // when no input is focused (guarded above), so it never blocks
        // typing the letter `q` in the search box.
        closeAllModals()
      } else if (e.key === ']') {
        railSignal.value = railSignal.value === 'visible' ? 'hidden' : 'visible'
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Esc closes info drawer (preserved from old AppShell).
  useEffect(() => {
    if (!drawerOpen) return
    const onKey = (e) => { if (e.key === 'Escape') (infoDrawerOpenSignal.value = false) }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [drawerOpen])

  return html`
    <div id="app-root-grid" class="app">
      <${Topbar}/>
      <${Sidebar}/>
      <div class="main">
        <${WorkHead}/>
        <div class="work-body">
          <${Panes} tab=${activeTab}/>
        </div>
      </div>
      <${RightRail}/>
      <${Footer}/>
      <${MobileTabs}/>

      ${showCreateSession && html`<${CreateSessionDialog}/>`}
      <${EditSessionDialog}/>
      ${showMoveSession && html`<${MoveSessionDialog} ...${showMoveSession}/>`}
      ${showPromptSession && html`<${PromptSessionDialog} ...${showPromptSession}/>`}
      ${showNotesSession && html`<${NotesSessionDialog} ...${showNotesSession}/>`}
      ${confirmData && html`<${ConfirmDialog} ...${confirmData}/>`}
      ${groupNameData && html`<${GroupNameDialog} ...${groupNameData}/>`}

      ${drawerOpen && html`
        <div class="overlay" onClick=${() => (infoDrawerOpenSignal.value = false)}>
          <div class="dialog" onClick=${e => e.stopPropagation()}>
            <div class="dh">
              <span class="kicker">SETTINGS</span>
              <div class="t">Settings</div>
              <button class="icon-btn" onClick=${() => (infoDrawerOpenSignal.value = false)} aria-label="Close settings">
                <${Icon} d=${ICONS.x}/>
              </button>
            </div>
            <div class="db">
              <${SettingsPanel}/>
            </div>
          </div>
        </div>
      `}

      <${CommandPalette}/>
      <${TweaksPanel}/>
      <${KeyboardShortcuts}/>
      <${JumpOverlay}/>
      <${ToastContainer}/>
      <${ToastHistoryDrawer}/>
    </div>
  `
}
