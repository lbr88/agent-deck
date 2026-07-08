// HubNodesDialog.js -- Admin dialog for connected hub node metadata.
// It talks to the local web server, which forwards through the configured
// admin hub node token. No hub token is exposed to the browser.
import { html } from 'htm/preact'
import { useEffect, useMemo, useState } from 'preact/hooks'
import { Icon, ICONS } from './icons.js'
import { apiFetch } from './api.js'
import { addToast } from './Toast.js'
import { hubNodesDialogSignal, hubNodesSignal } from './state.js'
import { refreshMenuSnapshot } from './menuRefresh.js'

export function HubNodesDialog() {
  const [nodes, setNodes] = useState([])
  const [invites, setInvites] = useState([])
  const [trustRequests, setTrustRequests] = useState([])
  const [drafts, setDrafts] = useState({})
  const [inviteDraft, setInviteDraft] = useState({ nodeName: '', ttlHours: '24', admin: false })
  const [joinCommand, setJoinCommand] = useState('')
  const [loading, setLoading] = useState(true)
  const [busyID, setBusyID] = useState('')
  const [error, setError] = useState(null)

  useEffect(() => {
    let cancelled = false
    async function load() {
      setLoading(true)
      setError(null)
      try {
        const [nodesData, invitesData, trustData] = await Promise.all([
          apiFetch('GET', '/api/hub/nodes'),
          apiFetch('GET', '/api/hub/invites'),
          apiFetch('GET', '/api/hub/trust/pending'),
        ])
        if (cancelled) return
        const next = Array.isArray(nodesData.nodes) ? nodesData.nodes : []
        setNodes(next)
        setInvites(Array.isArray(invitesData.invites) ? invitesData.invites : [])
        setTrustRequests(Array.isArray(trustData.requests) ? trustData.requests : [])
        setDrafts(Object.fromEntries(next.map(node => [node.id, node.name || node.id])))
      } catch (err) {
        if (!cancelled) setError(err.message || 'Failed to load hub nodes')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  const sortedNodes = useMemo(() => {
    return [...nodes].sort((a, b) => (a.name || a.id || '').localeCompare(b.name || b.id || '') || (a.id || '').localeCompare(b.id || ''))
  }, [nodes])

  const close = () => (hubNodesDialogSignal.value = false)

  async function renameNode(node) {
    if (!node?.id) return
    const name = (drafts[node.id] || '').trim()
    if (!name) {
      setError('Name is required')
      return
    }
    setBusyID('rename:' + node.id)
    setError(null)
    try {
      const renamed = await apiFetch('PATCH', '/api/hub/nodes/' + encodeURIComponent(node.id), { name })
      setNodes(current => current.map(n => n.id === renamed.id ? { ...n, ...renamed } : n))
      setDrafts(current => ({ ...current, [renamed.id]: renamed.name || renamed.id }))
      hubNodesSignal.value = (hubNodesSignal.value || []).map(n => n.id === renamed.id ? { ...n, name: renamed.name || renamed.id } : n)
      refreshMenuSnapshot().catch(() => {})
      addToast(`Renamed hub node ${renamed.id} to ${renamed.name}`, 'success')
    } catch (err) {
      setError(err.message || 'Rename failed')
    } finally {
      setBusyID('')
    }
  }

  async function setNodeAdmin(node, admin) {
    if (!node?.id) return
    const action = admin ? 'promote' : 'demote'
    setBusyID(action + ':' + node.id)
    setError(null)
    try {
      const actionPath = admin ? '/promote' : '/demote'
      const updated = await apiFetch('POST', '/api/hub/nodes/' + encodeURIComponent(node.id) + actionPath)
      setNodes(current => current.map(n => n.id === updated.id ? { ...n, ...updated } : n))
      addToast(`${admin ? 'Promoted' : 'Demoted'} hub node ${updated.name || updated.id}`, 'success')
    } catch (err) {
      setError(err.message || `${admin ? 'Promote' : 'Demote'} failed`)
    } finally {
      setBusyID('')
    }
  }

  async function revokeNode(node) {
    if (!node?.id) return
    if (!window.confirm(`Revoke hub node ${node.name || node.id}? It will need a new invite to reconnect.`)) return
    setBusyID('revoke:' + node.id)
    setError(null)
    try {
      await apiFetch('DELETE', '/api/hub/nodes/' + encodeURIComponent(node.id))
      setNodes(current => current.filter(n => n.id !== node.id))
      hubNodesSignal.value = (hubNodesSignal.value || []).filter(n => n.id !== node.id)
      refreshMenuSnapshot().catch(() => {})
      addToast(`Revoked hub node ${node.name || node.id}`, 'success')
    } catch (err) {
      setError(err.message || 'Revoke failed')
    } finally {
      setBusyID('')
    }
  }

  async function createInvite() {
    const nodeName = (inviteDraft.nodeName || '').trim()
    if (!nodeName) {
      setError('Invite node name is required')
      return
    }
    const ttlHours = Number.parseFloat(inviteDraft.ttlHours || '24')
    if (!Number.isFinite(ttlHours) || ttlHours <= 0) {
      setError('Invite TTL must be greater than zero')
      return
    }
    setBusyID('invite:create')
    setError(null)
    setJoinCommand('')
    try {
      const created = await apiFetch('POST', '/api/hub/invites', {
        nodeName,
        ttlSeconds: Math.round(ttlHours * 3600),
        admin: !!inviteDraft.admin,
      })
      setJoinCommand(created.joinCommand || '')
      setInviteDraft({ nodeName: '', ttlHours: '24', admin: false })
      const next = await apiFetch('GET', '/api/hub/invites')
      setInvites(Array.isArray(next.invites) ? next.invites : [])
      addToast(`Created hub invite for ${nodeName}`, 'success')
    } catch (err) {
      setError(err.message || 'Create invite failed')
    } finally {
      setBusyID('')
    }
  }

  async function revokeInvite(invite) {
    if (!invite?.id) return
    if (!window.confirm(`Revoke invite for ${invite.nodeName || invite.id}?`)) return
    setBusyID('invite:revoke:' + invite.id)
    setError(null)
    try {
      await apiFetch('DELETE', '/api/hub/invites/' + encodeURIComponent(invite.id))
      setInvites(current => current.filter(i => i.id !== invite.id))
      addToast(`Revoked invite for ${invite.nodeName || invite.id}`, 'success')
    } catch (err) {
      setError(err.message || 'Revoke invite failed')
    } finally {
      setBusyID('')
    }
  }

  async function decideTrust(request, allow) {
    if (!request?.nodeId) return
    const action = allow ? 'allow' : 'deny'
    setBusyID('trust:' + action + ':' + request.nodeId)
    setError(null)
    try {
      await apiFetch('POST', '/api/hub/trust/' + encodeURIComponent(request.nodeId) + '/' + action)
      setTrustRequests(current => current.filter(r => r.nodeId !== request.nodeId))
      addToast(`${allow ? 'Allowed' : 'Denied'} hub trust for ${request.nodeName || request.nodeId}`, 'success')
    } catch (err) {
      setError(err.message || `${allow ? 'Allow' : 'Deny'} trust failed`)
    } finally {
      setBusyID('')
    }
  }

  return html`
    <div class="overlay" onClick=${(e) => e.target === e.currentTarget && close()}>
      <div role="dialog" aria-modal="true" aria-label="Hub nodes"
           class="dialog" style="max-width: 720px;"
           onClick=${e => e.stopPropagation()}>
        <div class="dh">
          <span class="kicker">HUB</span>
          <div class="t">Hub nodes</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close hub nodes">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div style="font-family: var(--sans); color: var(--muted); line-height: 1.45;">
            Rename connected hub nodes using this web server's configured admin node credentials.
          </div>
          ${loading && html`<div class="empty">Loading hub nodes…</div>`}
          ${error && html`
            <div style="font-family: var(--mono); font-size: 11.5px; color: var(--tn-red); padding: 8px 10px;
                        border: 1px solid rgba(247,118,142,0.3); border-radius: 4px; background: rgba(247,118,142,0.06);">
              ${error}
            </div>
          `}
          ${!loading && sortedNodes.length === 0 && html`<div class="empty">No hub nodes returned by the configured hub.</div>`}
          ${!loading && sortedNodes.length > 0 && html`
            <div style="display: grid; gap: 8px;">
              ${sortedNodes.map(node => {
                const draft = drafts[node.id] ?? node.name ?? node.id
                const unchanged = draft.trim() === (node.name || node.id)
                return html`
                  <div key=${node.id} class="hub-node-row"
                       style="display: grid; grid-template-columns: minmax(160px, 1fr) minmax(180px, 1.2fr) auto auto; gap: 10px; align-items: end;
                              border: 1px solid var(--border); border-radius: 6px; padding: 10px;">
                    <div>
                      <div style="font-family: var(--mono); font-size: 12px; color: var(--text);">${node.name || node.id}</div>
                      <div style="font-family: var(--mono); font-size: 10.5px; color: var(--muted);">${node.id}</div>
                      <div style="font-family: var(--mono); font-size: 10.5px; color: var(--muted);">${node.status || 'unknown'}${node.admin ? ' · admin' : ''}</div>
                    </div>
                    <div class="field" style="margin: 0;">
                      <label>SHORT NAME</label>
                      <input
                        value=${draft}
                        onInput=${e => setDrafts(current => ({ ...current, [node.id]: e.currentTarget.value }))}
                        onKeyDown=${e => {
                          if (e.key === 'Enter') {
                            e.preventDefault()
                            renameNode(node)
                          }
                        }}
                        placeholder="work-laptop"/>
                    </div>
                    <button
                      type="button"
                      class="btn primary hub-node-rename-btn"
                      disabled=${busyID === 'rename:' + node.id || !draft.trim() || unchanged}
                      onClick=${() => renameNode(node)}>
                      ${busyID === 'rename:' + node.id ? 'Renaming…' : 'Rename'}
                    </button>
                    <div style="display: flex; gap: 6px; justify-content: flex-end;">
                      ${node.admin
                        ? html`<button type="button" class="btn ghost hub-node-demote-btn"
                            disabled=${busyID === 'demote:' + node.id}
                            onClick=${() => setNodeAdmin(node, false)}>
                            ${busyID === 'demote:' + node.id ? 'Demoting…' : 'Demote'}
                          </button>`
                        : html`<button type="button" class="btn ghost hub-node-promote-btn"
                            disabled=${busyID === 'promote:' + node.id}
                            onClick=${() => setNodeAdmin(node, true)}>
                            ${busyID === 'promote:' + node.id ? 'Promoting…' : 'Promote'}
                          </button>`}
                      <button type="button" class="btn danger hub-node-revoke-btn"
                        disabled=${busyID === 'revoke:' + node.id}
                        onClick=${() => revokeNode(node)}>
                        ${busyID === 'revoke:' + node.id ? 'Revoking…' : 'Revoke'}
                      </button>
                    </div>
                  </div>
                `
              })}
            </div>
          `}
          ${!loading && html`
            <div style="border-top: 1px solid var(--border); padding-top: 12px; display: grid; gap: 10px;">
              <div>
                <div class="kicker">INVITES</div>
                <div style="font-family: var(--sans); color: var(--muted); line-height: 1.45;">
                  Create join commands for new hub nodes. Existing invite lists never expose invite tokens.
                </div>
              </div>
              <div class="hub-invite-create" style="display: grid; grid-template-columns: minmax(160px, 1fr) 90px auto auto; gap: 10px; align-items: end;">
                <div class="field" style="margin: 0;">
                  <label>NODE SHORT NAME</label>
                  <input
                    value=${inviteDraft.nodeName}
                    onInput=${e => setInviteDraft(current => ({ ...current, nodeName: e.currentTarget.value }))}
                    placeholder="a-nyvej-gpu"/>
                </div>
                <div class="field" style="margin: 0;">
                  <label>TTL HOURS</label>
                  <input
                    value=${inviteDraft.ttlHours}
                    onInput=${e => setInviteDraft(current => ({ ...current, ttlHours: e.currentTarget.value }))}
                    placeholder="24"/>
                </div>
                <label style="display: flex; gap: 6px; align-items: center; font-family: var(--mono); font-size: 11px; color: var(--muted); padding-bottom: 8px;">
                  <input type="checkbox"
                    checked=${!!inviteDraft.admin}
                    onChange=${e => setInviteDraft(current => ({ ...current, admin: e.currentTarget.checked }))}/>
                  admin
                </label>
                <button type="button" class="btn primary hub-invite-create-btn"
                  disabled=${busyID === 'invite:create'}
                  onClick=${createInvite}>
                  ${busyID === 'invite:create' ? 'Creating…' : 'Create invite'}
                </button>
              </div>
              ${joinCommand && html`
                <div class="field" style="margin: 0;">
                  <label>JOIN COMMAND</label>
                  <textarea readOnly rows="2" style="font-family: var(--mono);">${joinCommand}</textarea>
                </div>
              `}
              ${invites.length === 0
                ? html`<div class="empty">No hub invites.</div>`
                : html`
                  <div style="display: grid; gap: 6px;">
                    ${invites.map(invite => html`
                      <div key=${invite.id || invite.nodeName} class="hub-invite-row"
                           style="display: grid; grid-template-columns: minmax(160px, 1fr) auto auto; gap: 10px; align-items: center; border: 1px solid var(--border); border-radius: 6px; padding: 8px 10px;">
                        <div>
                          <div style="font-family: var(--mono); font-size: 12px; color: var(--text);">${invite.nodeName || invite.id}</div>
                          <div style="font-family: var(--mono); font-size: 10.5px; color: var(--muted);">${invite.status || 'unknown'}${invite.admin ? ' · admin' : ''}</div>
                        </div>
                        <div style="font-family: var(--mono); font-size: 10.5px; color: var(--muted);">${invite.expiresAt || ''}</div>
                        <button type="button" class="btn danger hub-invite-revoke-btn"
                          disabled=${!invite.id || busyID === 'invite:revoke:' + invite.id || invite.status !== 'pending'}
                          onClick=${() => revokeInvite(invite)}>
                          ${busyID === 'invite:revoke:' + invite.id ? 'Revoking…' : 'Revoke'}
                        </button>
                      </div>
                    `)}
                  </div>
                `}
            </div>
          `}
          ${!loading && html`
            <div style="border-top: 1px solid var(--border); padding-top: 12px; display: grid; gap: 10px;">
              <div>
                <div class="kicker">TRUST REQUESTS</div>
                <div style="font-family: var(--sans); color: var(--muted); line-height: 1.45;">
                  Allow or deny pending cross-node hub actions.
                </div>
              </div>
              ${trustRequests.length === 0
                ? html`<div class="empty">No pending hub trust requests.</div>`
                : html`
                  <div style="display: grid; gap: 6px;">
                    ${trustRequests.map(request => html`
                      <div key=${request.nodeId} class="hub-trust-row"
                           style="display: grid; grid-template-columns: minmax(160px, 1fr) auto; gap: 10px; align-items: center; border: 1px solid var(--border); border-radius: 6px; padding: 8px 10px;">
                        <div>
                          <div style="font-family: var(--mono); font-size: 12px; color: var(--text);">${request.nodeName || request.nodeId}</div>
                          <div style="font-family: var(--mono); font-size: 10.5px; color: var(--muted);">${request.nodeId} ${request.os || ''}/${request.arch || ''} ${request.version || ''}</div>
                        </div>
                        <div style="display: flex; gap: 6px; justify-content: flex-end;">
                          <button type="button" class="btn primary hub-trust-allow-btn"
                            disabled=${busyID === 'trust:allow:' + request.nodeId}
                            onClick=${() => decideTrust(request, true)}>
                            ${busyID === 'trust:allow:' + request.nodeId ? 'Allowing…' : 'Allow'}
                          </button>
                          <button type="button" class="btn danger hub-trust-deny-btn"
                            disabled=${busyID === 'trust:deny:' + request.nodeId}
                            onClick=${() => decideTrust(request, false)}>
                            ${busyID === 'trust:deny:' + request.nodeId ? 'Denying…' : 'Deny'}
                          </button>
                        </div>
                      </div>
                    `)}
                  </div>
                `}
            </div>
          `}
        </div>
        <div class="df">
          <button type="button" class="btn ghost" onClick=${close}>Close</button>
        </div>
      </div>
    </div>
  `
}
