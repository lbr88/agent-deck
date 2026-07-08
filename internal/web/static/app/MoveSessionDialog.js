// MoveSessionDialog.js -- Move a session to another existing group.
import { html } from 'htm/preact'
import { useMemo, useState } from 'preact/hooks'
import { Icon, ICONS } from './icons.js'
import { menuModelSignal } from './dataModel.js'
import { moveSessionDialogSignal } from './state.js'
import { apiFetch } from './api.js'

export function MoveSessionDialog({ sessionId }) {
  const { groups, sessions } = menuModelSignal.value
  const session = sessions.find(s => s.id === sessionId)
  const groupOptions = useMemo(() => {
    const seen = new Set()
    return (groups || [])
      .map(g => g.path)
      .filter(Boolean)
      .filter(path => {
        if (seen.has(path)) return false
        seen.add(path)
        return true
      })
  }, [groups])
  const [groupPath, setGroupPath] = useState(session?.group || groupOptions[0] || 'default')
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)
  const close = () => (moveSessionDialogSignal.value = null)

  async function handleSubmit(e) {
    e.preventDefault()
    const target = (groupPath || '').trim().replace(/^\/+|\/+$/g, '')
    if (!target) return setError('group is required')
    setError(null)
    setSubmitting(true)
    try {
      await apiFetch('POST', `/api/sessions/${sessionId}/group`, { groupPath: target })
      moveSessionDialogSignal.value = null
    } catch (err) {
      setError(err.message)
    } finally {
      setSubmitting(false)
    }
  }

  return html`
    <div class="overlay" onClick=${(e) => e.target === e.currentTarget && close()}>
      <form class="dialog" style="max-width: 460px;"
            onClick=${e => e.stopPropagation()}
            onSubmit=${handleSubmit}>
        <div class="dh">
          <span class="kicker">MOVE</span>
          <div class="t">Move session</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close move session">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>SESSION</label>
            <input readonly value=${session?.title || sessionId}/>
          </div>
          <div class="field">
            <label>GROUP</label>
            <select autofocus value=${groupPath} onChange=${e => setGroupPath(e.target.value)}>
              ${groupOptions.map(path => html`<option key=${path} value=${path}>${path}</option>`)}
            </select>
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
          <button type="submit" class="btn primary" disabled=${submitting || !groupPath}>
            ${submitting ? 'Moving…' : 'Move'}
          </button>
        </div>
      </form>
    </div>
  `
}
