// panes/PluginsPane.js -- Web UI for Claude plugin management.
//
// Mirrors the TUI `L` plugin dialog and routes hub session IDs through the
// backend /api/sessions/{id}/plugins endpoints.
import { html } from 'htm/preact'
import { useEffect, useState, useCallback } from 'preact/hooks'
import { menuModelSignal } from '../dataModel.js'
import { selectedIdSignal, mutationsEnabledSignal } from '../state.js'
import { apiFetch } from '../api.js'
import { addToast } from '../Toast.js'

function labelForPlugin(plugin) {
  if (!plugin) return ''
  const name = plugin.pluginName || plugin.name || ''
  const source = plugin.source || ''
  return source ? `${name} · ${source}` : name
}

export function PluginsPane() {
  const { sessions } = menuModelSignal.value
  const selectedId = selectedIdSignal.value
  const mutationsEnabled = mutationsEnabledSignal.value
  const session = sessions.find(s => s.id === selectedId)

  const [catalog, setCatalog] = useState([])
  const [plugins, setPlugins] = useState([])
  const [channels, setChannels] = useState([])
  const [noChannelLink, setNoChannelLink] = useState(false)
  const [loading, setLoading] = useState(false)
  const [busyName, setBusyName] = useState('')
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    if (!session) return
    setLoading(true)
    setError('')
    try {
      const state = await apiFetch('GET', `/api/sessions/${encodeURIComponent(session.id)}/plugins`)
      setCatalog(state.catalog || [])
      setPlugins(state.plugins || [])
      setChannels(state.channels || [])
      setNoChannelLink(!!state.pluginChannelLinkDisabled)
    } catch (err) {
      setError(err.message || 'failed to load plugins')
    } finally {
      setLoading(false)
    }
  }, [session && session.id])

  useEffect(() => { refresh() }, [refresh])

  if (!session) {
    return html`
      <div class="costs">
        <div class="chart-card" style="text-align: center; padding: 48px 24px;">
          <div class="title" style="font-size: 16px;">Plugin Manager</div>
          <div style="font-family: var(--mono); font-size: 12px; color: var(--text-dim); padding-top: 8px;">
            Select a Claude session in the sidebar to manage plugins.
          </div>
        </div>
      </div>`
  }

  if ((session.tool || '').toLowerCase() !== 'claude') {
    return html`
      <div class="costs">
        <div class="chart-card" style="text-align: center; padding: 48px 24px;">
          <div class="title" style="font-size: 16px;">Plugins not supported for ${session.tool || 'this tool'}</div>
          <div style="font-family: var(--mono); font-size: 12px; color: var(--text-dim); padding-top: 8px;">
            Plugins are Claude Code enabledPlugins entries.
          </div>
        </div>
      </div>`
  }

  const attached = new Set(plugins)
  const available = catalog.filter(p => !attached.has(p.name))

  async function attach(plugin) {
    if (!plugin || busyName) return
    setBusyName(plugin.name)
    try {
      await apiFetch('POST', `/api/sessions/${encodeURIComponent(session.id)}/plugins/${encodeURIComponent(plugin.name)}`, { noChannelLink })
      addToast(`Attached plugin ${plugin.name}`)
      await refresh()
    } catch (_) {
      // apiFetch already toasts mutation errors.
    } finally {
      setBusyName('')
    }
  }

  async function detach(name) {
    if (!name || busyName) return
    setBusyName(name)
    try {
      await apiFetch('DELETE', `/api/sessions/${encodeURIComponent(session.id)}/plugins/${encodeURIComponent(name)}`)
      addToast(`Detached plugin ${name}`)
      await refresh()
    } catch (_) {
      // apiFetch already toasts mutation errors.
    } finally {
      setBusyName('')
    }
  }

  return html`
    <div class="skills-pane" data-testid="plugins-pane" style="padding: 16px; display: flex; flex-direction: column; gap: 16px; height: 100%; overflow: auto;">
      <div style="display: flex; justify-content: space-between; align-items: center; gap: 12px;">
        <div>
          <div class="title" style="font-size: 14px;">Plugins · ${session.title}</div>
          <div style="font-family: var(--mono); font-size: 11px; color: var(--text-dim); margin-top: 4px;">
            ${session.isHub ? `hub:${session.hubNodeName || session.hubNodeId}` : 'local'} · restart required after plugin changes
          </div>
        </div>
        <button class="btn" data-testid="plugins-refresh" onClick=${refresh} disabled=${loading}>${loading ? 'Loading…' : 'Refresh'}</button>
      </div>

      ${error && html`
        <div data-testid="plugins-error" style="font-family: var(--mono); font-size: 11px; color: var(--err); background: var(--err-bg); padding: 8px 12px; border-radius: 4px;">
          ${error}
        </div>`}

      <label style="display: flex; align-items: center; gap: 8px; font-family: var(--mono); font-size: 12px; color: var(--text-dim);">
        <input type="checkbox" checked=${noChannelLink} disabled=${!mutationsEnabled} onChange=${e => setNoChannelLink(e.currentTarget.checked)}/>
        Do not auto-link channel-emitting plugins
      </label>

      ${channels.length > 0 && html`
        <div style="font-family: var(--mono); font-size: 11px; color: var(--text-dim);">
          Linked channels: ${channels.join(', ')}
        </div>`}

      <section data-testid="plugins-attached" style="border: 1px solid var(--border); border-radius: 6px; padding: 12px;">
        <div style="font-family: var(--mono); font-size: 12px; color: var(--text-dim); margin-bottom: 8px;">
          ATTACHED (${plugins.length})
        </div>
        ${plugins.length === 0
          ? html`<div data-testid="plugins-attached-empty" style="color: var(--muted); font-size: 12px;">No plugins attached.</div>`
          : html`<ul style="list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px;">
              ${plugins.map(name => html`
                <li data-testid="plugin-attached-row" data-plugin-name=${name} style="display: flex; justify-content: space-between; gap: 8px; align-items: center; padding: 6px 8px; background: var(--surface); border-radius: 4px;">
                  <span><strong>${name}</strong></span>
                  <button class="btn btn-danger" data-testid="plugin-detach-btn" disabled=${!mutationsEnabled || busyName === name} onClick=${() => detach(name)}>Detach</button>
                </li>`)}
            </ul>`}
      </section>

      <section data-testid="plugins-catalog" style="border: 1px solid var(--border); border-radius: 6px; padding: 12px;">
        <div style="font-family: var(--mono); font-size: 12px; color: var(--text-dim); margin-bottom: 8px;">
          CATALOG (${available.length})
        </div>
        ${available.length === 0
          ? html`<div data-testid="plugins-catalog-empty" style="color: var(--muted); font-size: 12px;">No additional plugins available to attach.</div>`
          : html`<ul style="list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px;">
              ${available.map(plugin => html`
                <li data-testid="plugin-catalog-row" data-plugin-name=${plugin.name} style="display: flex; justify-content: space-between; gap: 8px; align-items: center; padding: 6px 8px;">
                  <span>
                    <strong>${plugin.name}</strong>
                    <span style="color: var(--muted); font-size: 11px;"> ${labelForPlugin(plugin)}</span>
                    ${plugin.emitsChannel && html`<div style="color: var(--text-dim); font-size: 11px;">emits channel events</div>`}
                  </span>
                  <button class="btn" data-testid="plugin-attach-btn" disabled=${!mutationsEnabled || busyName === plugin.name} onClick=${() => attach(plugin)}>Attach</button>
                </li>`)}
            </ul>`}
      </section>
    </div>
  `
}
