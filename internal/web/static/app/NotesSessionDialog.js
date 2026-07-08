// NotesSessionDialog.js -- Inline session notes editor.
import { html } from 'htm/preact'
import { useState } from 'preact/hooks'
import { Icon, ICONS } from './icons.js'
import { menuModelSignal } from './dataModel.js'
import { notesSessionDialogSignal } from './state.js'
import { apiFetch } from './api.js'

export function NotesSessionDialog({ sessionId }) {
  const { sessions } = menuModelSignal.value
  const session = sessions.find(s => s.id === sessionId)
  const [notes, setNotes] = useState(session?.notes || '')
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)
  const close = () => (notesSessionDialogSignal.value = null)

  async function handleSubmit(e) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await apiFetch('POST', `/api/sessions/${sessionId}/notes`, { notes })
      notesSessionDialogSignal.value = null
    } catch (err) {
      setError(err.message)
    } finally {
      setSubmitting(false)
    }
  }

  function onKeyDown(e) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      handleSubmit(e)
    }
  }

  return html`
    <div class="overlay" onClick=${(e) => e.target === e.currentTarget && close()} data-testid="notes-session-dialog">
      <form class="dialog" style="max-width: 560px;"
            onClick=${e => e.stopPropagation()}
            onSubmit=${handleSubmit}>
        <div class="dh">
          <span class="kicker">NOTES</span>
          <div class="t">Edit notes</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close notes editor">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>SESSION</label>
            <input readonly value=${session?.title || sessionId}/>
          </div>
          <div class="field">
            <label>NOTES</label>
            <textarea autofocus rows="8" value=${notes}
                      data-testid="notes-session-notes"
                      placeholder="Write notes for this session… Ctrl/Cmd+Enter to save"
                      onInput=${e => setNotes(e.target.value)}
                      onKeyDown=${onKeyDown}></textarea>
          </div>
          ${error && html`
            <div data-testid="notes-session-error"
                 style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-red); padding: 8px 10px;
                        border: 1px solid rgba(247,118,142,0.3); border-radius: 4px; background: rgba(247,118,142,0.06);">
              ${error}
            </div>
          `}
        </div>
        <div class="df">
          <button type="button" class="btn ghost" onClick=${close}>Cancel</button>
          <button type="submit" class="btn primary" data-testid="notes-session-save" disabled=${submitting}>
            ${submitting ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>
    </div>
  `
}
