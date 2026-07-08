// ForkSessionDialog.js -- Web parity for TUI Shift+F fork-with-options.
import { html } from 'htm/preact'
import { useState } from 'preact/hooks'
import { menuModelSignal } from './dataModel.js'
import { forkSessionDialogSignal, mutationsEnabledSignal } from './state.js'
import { apiFetch } from './api.js'
import { Icon, ICONS } from './icons.js'
import { refreshMenuSnapshot } from './menuRefresh.js'

function defaultForkTitle(session) {
  const title = (session?.title || session?.id || '').trim()
  return title ? `${title} (fork)` : 'fork'
}

function defaultBranch(session) {
  const title = (session?.title || session?.id || 'fork').toLowerCase()
    .replace(/[^a-z0-9._/-]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return `fork/${title || 'fork'}`
}

export function ForkSessionDialog({ sessionId }) {
  const { sessions } = menuModelSignal.value
  const session = sessions.find(s => s.id === sessionId)
  const [title, setTitle] = useState(defaultForkTitle(session))
  const [groupPath, setGroupPath] = useState(session?.group || 'default')
  const [worktree, setWorktree] = useState(false)
  const [branch, setBranch] = useState(defaultBranch(session))
  const [withState, setWithState] = useState(false)
  const [withIgnored, setWithIgnored] = useState(false)
  const [sandbox, setSandbox] = useState(!!session?.sandbox)
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)

  if (!mutationsEnabledSignal.value || !session) return null

  const close = () => (forkSessionDialogSignal.value = null)
  const handleBackdropClick = (e) => { if (e.target === e.currentTarget) close() }

  async function handleSubmit(e) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      const payload = {
        title: title.trim(),
        groupPath: groupPath.trim(),
        worktree,
        branch: worktree ? branch.trim() : '',
        withState: worktree && withState,
        withIgnored: worktree && withState && withIgnored,
        sandbox,
      }
      await apiFetch('POST', `/api/sessions/${session.id}/fork`, payload)
      await refreshMenuSnapshot()
      close()
    } catch (err) {
      setError(err.message)
    } finally {
      setSubmitting(false)
    }
  }

  return html`
    <div class="overlay" role="dialog" aria-modal="true" onClick=${handleBackdropClick}>
      <form class="dialog" onClick=${e => e.stopPropagation()} onSubmit=${handleSubmit}>
        <div class="dh">
          <span class="kicker">FORK</span>
          <div class="t">Fork session</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>SOURCE</label>
            <div style="font-family: var(--mono); font-size: 12px; color: var(--muted);">
              ${session.title}${session.isHub ? ` — hub:${session.hubNodeName || session.hubNodeId}` : ''}
            </div>
          </div>
          <div class="field">
            <label>TITLE</label>
            <input autofocus required value=${title} onInput=${e => setTitle(e.target.value)} placeholder="fork title"/>
          </div>
          <div class="field">
            <label>GROUP</label>
            <input value=${groupPath} onInput=${e => setGroupPath(e.target.value)} placeholder="default"/>
          </div>
          <label class="check-row">
            <input type="checkbox" checked=${worktree} onInput=${e => setWorktree(e.currentTarget.checked)}/>
            <span>Create worktree / branch</span>
          </label>
          ${worktree && html`
            <div class="field">
              <label>BRANCH</label>
              <input required value=${branch} onInput=${e => setBranch(e.target.value)} placeholder="fork/my-branch"/>
            </div>
            <label class="check-row">
              <input type="checkbox" checked=${withState} onInput=${e => setWithState(e.currentTarget.checked)}/>
              <span>Carry uncommitted state</span>
            </label>
            ${withState && html`
              <label class="check-row">
                <input type="checkbox" checked=${withIgnored} onInput=${e => setWithIgnored(e.currentTarget.checked)}/>
                <span>Include gitignored files</span>
              </label>
            `}
          `}
          <label class="check-row">
            <input type="checkbox" checked=${sandbox} onInput=${e => setSandbox(e.currentTarget.checked)}/>
            <span>Run fork in sandbox</span>
          </label>
          ${session.isHub && worktree && html`
            <div style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-yellow, #e0af68); padding: 8px 10px;
                        border: 1px solid rgba(224,175,104,0.3); border-radius: 4px; background: rgba(224,175,104,0.06);">
              Hub worktree/state fork depends on the remote node supporting fork options.
            </div>
          `}
          ${error && html`
            <div style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-red); padding: 8px 10px;
                        border: 1px solid rgba(247,118,142,0.3); border-radius: 4px; background: rgba(247,118,142,0.06);">
              ${error}
            </div>
          `}
        </div>
        <div class="df">
          <button type="button" class="btn ghost" onClick=${close}>Cancel</button>
          <button type="submit" class="btn primary" disabled=${submitting || !title.trim() || (worktree && !branch.trim())}>
            ${submitting ? 'Forking…' : html`Fork session <span class="kbd">Shift+F</span>`}
          </button>
        </div>
      </form>
    </div>
  `
}
