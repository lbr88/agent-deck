// EditPathsDialog.js -- Web parity for TUI EditPathsDialog (`p`).
//
// Edits the path set for an existing multi-repo session. The server rewrites
// the session's multi-repo symlink tree, persists state, and restarts.

import { html } from 'htm/preact'
import { useMemo, useState } from 'preact/hooks'
import { pathsSessionDialogSignal, mutationsEnabledSignal } from './state.js'
import { menuModelSignal } from './dataModel.js'
import { Icon, ICONS } from './icons.js'
import { apiFetch } from './api.js'

function initialPaths(session) {
  if (!session) return ''
  const raw = session.raw || {}
  const paths = [session.path || raw.projectPath || '', ...(session.additionalPaths || raw.additionalPaths || [])]
    .map(p => String(p || '').trim())
    .filter(Boolean)
  return paths.join('\n')
}

function parsePaths(value) {
  const seen = new Set()
  const paths = []
  for (const line of String(value || '').split(/\r?\n/)) {
    const p = line.trim()
    if (!p || seen.has(p)) continue
    seen.add(p)
    paths.push(p)
  }
  return paths
}

export function EditPathsDialog() {
  const open = pathsSessionDialogSignal.value
  const { sessions } = menuModelSignal.value
  const session = useMemo(
    () => (open ? sessions.find(s => s.id === open.sessionId) : null),
    [open && open.sessionId, sessions],
  )
  const [value, setValue] = useState(initialPaths(session))
  const [seededFor, setSeededFor] = useState(open ? open.sessionId : null)
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)

  if (open && session && seededFor !== open.sessionId) {
    setValue(initialPaths(session))
    setError(null)
    setSeededFor(open.sessionId)
  }

  if (!open || !mutationsEnabledSignal.value || !session) return null

  function close() {
    pathsSessionDialogSignal.value = null
    setSeededFor(null)
  }

  async function handleSubmit(e) {
    e.preventDefault()
    setError(null)
    const paths = parsePaths(value)
    if (paths.length < 2) {
      setError('Multi-repo sessions require at least two paths.')
      return
    }
    setSubmitting(true)
    try {
      await apiFetch('POST', `/api/sessions/${encodeURIComponent(session.id)}/paths`, { paths })
      close()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setSubmitting(false)
    }
  }

  const handleBackdropClick = (e) => { if (e.target === e.currentTarget) close() }
  return html`
    <div class="overlay" onClick=${handleBackdropClick} data-testid="edit-paths-dialog">
      <form class="dialog" onClick=${e => e.stopPropagation()} onSubmit=${handleSubmit}>
        <div class="dh">
          <span class="kicker">PATHS</span>
          <div class="t">Edit multi-repo paths</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>PATHS — one directory per line</label>
            <textarea
              autofocus
              data-testid="edit-paths-textarea"
              rows="8"
              value=${value}
              onInput=${e => setValue(e.target.value)}
              placeholder="/repo/app&#10;/repo/shared"/>
          </div>
          <div class="muted" style="font-family: var(--mono); font-size: 11.5px;">
            Saving rewrites the multi-repo workspace and restarts the session.
          </div>
          ${error && html`
            <div data-testid="edit-paths-error"
                 style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-red); padding: 8px 10px;
                        border: 1px solid rgba(247,118,142,0.3); border-radius: 4px; background: rgba(247,118,142,0.06);">
              ${error}
            </div>
          `}
        </div>
        <div class="df">
          <button type="button" class="btn ghost" onClick=${close}>Cancel</button>
          <button type="submit" class="btn primary"
                  data-testid="edit-paths-save"
                  disabled=${submitting}>
            ${submitting ? 'Saving…' : html`Save paths <span class="kbd">⏎</span>`}
          </button>
        </div>
      </form>
    </div>
  `
}
