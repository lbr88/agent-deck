// GroupMoveDialog.js -- Reparent a group under another group or root.
import { html } from 'htm/preact'
import { useMemo, useState } from 'preact/hooks'
import { Icon, ICONS } from './icons.js'
import { menuModelSignal } from './dataModel.js'
import { groupMoveDialogSignal } from './state.js'
import { apiFetch } from './api.js'

function sameGroupSpace(source, candidate) {
  if (!source || !candidate) return false
  if (source.isHub) {
    return candidate.isHub && candidate.hubNodeId === source.hubNodeId
  }
  return !candidate.isHub
}

function isInvalidTarget(sourcePath, candidatePath) {
  if (!candidatePath) return false
  return candidatePath === sourcePath || candidatePath.startsWith(sourcePath + '/')
}

export function GroupMoveDialog({ groupPath }) {
  const { groups } = menuModelSignal.value
  const group = (groups || []).find(g => g.path === groupPath)
  const groupName = group?.name || group?.label || groupPath
  const targets = useMemo(() => {
    const seen = new Set([''])
    const out = [{ path: '', label: 'Root' }]
    for (const candidate of groups || []) {
      const path = candidate.path || ''
      if (!path || seen.has(path)) continue
      seen.add(path)
      if (!sameGroupSpace(group, candidate)) continue
      if (isInvalidTarget(groupPath, path)) continue
      out.push({ path, label: candidate.name || candidate.label || path })
    }
    return out
  }, [groups, groupPath, group])
  const [destParentPath, setDestParentPath] = useState(targets[0]?.path || '')
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)
  const close = () => (groupMoveDialogSignal.value = null)

  async function handleSubmit(e) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      await apiFetch('POST', `/api/groups/${encodeURIComponent(groupPath)}/change`, { destParentPath })
      groupMoveDialogSignal.value = null
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
          <div class="t">Move group</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close move group">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>GROUP</label>
            <input readonly value=${groupName}/>
          </div>
          <div class="field">
            <label>DESTINATION PARENT</label>
            <select autofocus value=${destParentPath} onChange=${e => setDestParentPath(e.target.value)}>
              ${targets.map(target => html`<option key=${target.path || 'root'} value=${target.path}>${target.label}</option>`)}
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
          <button type="submit" class="btn primary" disabled=${submitting || !group}>
            ${submitting ? 'Moving…' : 'Move'}
          </button>
        </div>
      </form>
    </div>
  `
}
