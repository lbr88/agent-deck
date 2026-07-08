// PromptSessionDialog.js -- Send a one-line prompt to a session without attach.
import { html } from 'htm/preact'
import { useState } from 'preact/hooks'
import { Icon, ICONS } from './icons.js'
import { menuModelSignal } from './dataModel.js'
import { promptSessionDialogSignal } from './state.js'
import { apiFetch } from './api.js'

export function PromptSessionDialog({ sessionId }) {
  const { sessions } = menuModelSignal.value
  const session = sessions.find(s => s.id === sessionId)
  const [message, setMessage] = useState('')
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)
  const close = () => (promptSessionDialogSignal.value = null)

  async function handleSubmit(e) {
    e.preventDefault()
    const text = message.trim()
    if (!text) return setError('message is required')
    setError(null)
    setSubmitting(true)
    try {
      await apiFetch('POST', `/api/sessions/${sessionId}/send`, { message: text })
      promptSessionDialogSignal.value = null
      setMessage('')
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
    <div class="overlay" onClick=${(e) => e.target === e.currentTarget && close()}>
      <form class="dialog" style="max-width: 560px;"
            onClick=${e => e.stopPropagation()}
            onSubmit=${handleSubmit}>
        <div class="dh">
          <span class="kicker">PROMPT</span>
          <div class="t">Prompt session</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close prompt session">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>SESSION</label>
            <input readonly value=${session?.title || sessionId}/>
          </div>
          <div class="field">
            <label>MESSAGE</label>
            <textarea autofocus rows="5" value=${message}
                      placeholder="Type a prompt… Ctrl/Cmd+Enter to send"
                      onInput=${e => setMessage(e.target.value)}
                      onKeyDown=${onKeyDown}></textarea>
          </div>
          ${error && html`
            <div style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-red); padding: 8px 10px;
                        border: 1px solid rgba(247,118,142,0.3); border-radius: 4px; background: rgba(247,118,142,0.06);">
              ${error}
            </div>
          `}
        </div>
        <div class="df">
          <button type="button" class="btn ghost" onClick=${close}>Cancel</button>
          <button type="submit" class="btn primary" disabled=${submitting || !message.trim()}>
            ${submitting ? 'Sending…' : 'Send'}
          </button>
        </div>
      </form>
    </div>
  `
}
