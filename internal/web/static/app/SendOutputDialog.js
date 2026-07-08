// SendOutputDialog.js -- Web parity for TUI `x`: send output to another session.
import { html } from 'htm/preact'
import { useEffect, useMemo, useState } from 'preact/hooks'
import { Icon, ICONS } from './icons.js'
import { menuModelSignal } from './dataModel.js'
import { sendOutputDialogSignal } from './state.js'
import { apiFetch } from './api.js'
import { addToast } from './Toast.js'

function isSendTarget(session, sourceSessionId) {
  if (!session || session.id === sourceSessionId) return false
  return session.status !== 'stopped' && session.status !== 'error'
}

export function SendOutputDialog({ sourceSessionId }) {
  const { sessions } = menuModelSignal.value
  const source = useMemo(
    () => sessions.find(s => s.id === sourceSessionId),
    [sourceSessionId, sessions],
  )
  const targets = useMemo(
    () => sessions.filter(s => isSendTarget(s, sourceSessionId)),
    [sessions, sourceSessionId],
  )
  const [targetSessionId, setTargetSessionId] = useState(targets[0]?.id || '')
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)
  const close = () => (sendOutputDialogSignal.value = null)
  const targetIDs = targets.map(s => s.id).join('\u0000')

  useEffect(() => {
    if (targets.length === 0) {
      if (targetSessionId) setTargetSessionId('')
      return
    }
    if (!targets.some(s => s.id === targetSessionId)) {
      setTargetSessionId(targets[0].id)
    }
  }, [sourceSessionId, targetIDs])

  if (!source) return null

  async function handleSubmit(e) {
    e.preventDefault()
    const targetID = targetSessionId || targets[0]?.id || ''
    if (!targetID) {
      setError('No active target sessions are available.')
      return
    }
    setError(null)
    setSubmitting(true)
    try {
      await apiFetch('POST', `/api/sessions/${encodeURIComponent(source.id)}/send-output`, { targetSessionId: targetID })
      const target = targets.find(s => s.id === targetID)
      addToast(`Sent output to ${target?.title || targetID}`, 'success')
      close()
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return html`
    <div class="overlay" onClick=${(e) => e.target === e.currentTarget && close()} data-testid="send-output-dialog">
      <form class="dialog" style="max-width: 560px;"
            onClick=${e => e.stopPropagation()}
            onSubmit=${handleSubmit}>
        <div class="dh">
          <span class="kicker">SEND OUTPUT</span>
          <div class="t">Send output to session</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close send output">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>SOURCE</label>
            <input readonly value=${source.title || source.id}/>
          </div>
          <div class="field">
            <label>TARGET SESSION</label>
            <select
              autofocus
              data-testid="send-output-target"
              value=${targetSessionId}
              onChange=${e => setTargetSessionId(e.target.value)}
              disabled=${targets.length === 0}>
              ${targets.map(s => html`
                <option value=${s.id}>
                  ${s.title || s.id}${s.isHub ? ` — ${s.hubNodeName || s.hubNodeId}` : ''}
                </option>
              `)}
            </select>
          </div>
          <div class="muted" style="font-family: var(--mono); font-size: 11.5px;">
            Sends the source session output wrapped with the same header/footer used by the TUI.
          </div>
          ${targets.length === 0 && html`
            <div data-testid="send-output-error"
                 style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-red); padding: 8px 10px;
                        border: 1px solid rgba(247,118,142,0.3); border-radius: 4px; background: rgba(247,118,142,0.06);">
              No active target sessions are available.
            </div>
          `}
          ${error && html`
            <div data-testid="send-output-error"
                 style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-red); padding: 8px 10px;
                        border: 1px solid rgba(247,118,142,0.3); border-radius: 4px; background: rgba(247,118,142,0.06);">
              ${error}
            </div>
          `}
        </div>
        <div class="df">
          <button type="button" class="btn ghost" onClick=${close}>Cancel</button>
          <button type="submit" class="btn primary"
                  data-testid="send-output-submit"
                  disabled=${submitting || targets.length === 0}>
            ${submitting ? 'Sending…' : 'Send output'}
          </button>
        </div>
      </form>
    </div>
  `
}
