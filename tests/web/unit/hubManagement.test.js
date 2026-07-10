import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'preact'
import { fireEvent, screen, waitFor } from '@testing-library/preact'
import { html } from 'htm/preact'

import { HubNodesDialog } from '../../../internal/web/static/app/HubNodesDialog.js'
import {
  hubAdminSignal,
  hubConfiguredSignal,
  hubNodesDialogSignal,
} from '../../../internal/web/static/app/state.js'

function jsonResponse(data) {
  return {
    ok: true,
    statusText: 'OK',
    async json() { return data },
  }
}

function renderDialog(admin) {
  const root = document.createElement('div')
  document.body.append(root)
  render(html`<${HubNodesDialog} admin=${admin}/>`, root)
}

describe('HubNodesDialog role-aware management', () => {
  beforeEach(() => {
    hubAdminSignal.value = false
    hubConfiguredSignal.value = true
    hubNodesDialogSignal.value = true
  })

  afterEach(() => {
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
  })

  it('loads only trust controls for a configured non-admin node', async () => {
    const fetchMock = vi.fn(async path => {
      if (path === '/api/hub/trust/pending') {
        return jsonResponse({ requests: [{ nodeId: 'node_pending', nodeName: 'pending laptop' }] })
      }
      if (path === '/api/hub/trust/node_pending/allow') {
        return jsonResponse({ ok: true })
      }
      throw new Error(`unexpected admin request from user dialog: ${path}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    renderDialog(false)

    expect(await screen.findByText('pending laptop')).toBeInTheDocument()
    expect(screen.getByText(/role: user/)).toBeInTheDocument()
    expect(screen.queryByText('INVITES')).not.toBeInTheDocument()
    expect(screen.queryByText('Rename')).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/hub/trust/pending')

    fireEvent.click(screen.getByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(fetchMock.mock.calls[1][0]).toBe('/api/hub/trust/node_pending/allow')
    expect(fetchMock.mock.calls[1][1].method).toBe('POST')
    await waitFor(() => expect(screen.queryByText('pending laptop')).not.toBeInTheDocument())
  })

  it('loads node, invite, and trust controls for an admin node', async () => {
    const fetchMock = vi.fn(async path => {
      switch (path) {
        case '/api/hub/nodes':
          return jsonResponse({ nodes: [{ id: 'node_remote', name: 'work-laptop', admin: false, status: 'online' }] })
        case '/api/hub/invites':
          return jsonResponse({ invites: [{ id: 'invite_1', nodeName: 'gpu', status: 'pending', admin: false }] })
        case '/api/hub/trust/pending':
          return jsonResponse({ requests: [] })
        default:
          throw new Error(`unexpected request: ${path}`)
      }
    })
    vi.stubGlobal('fetch', fetchMock)

    renderDialog(true)

    expect(await screen.findByText('work-laptop')).toBeInTheDocument()
    expect(screen.getByText(/role: admin/)).toBeInTheDocument()
    expect(screen.getByText('INVITES')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Rename' })).toBeInTheDocument()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))
    expect(new Set(fetchMock.mock.calls.map(call => call[0]))).toEqual(new Set([
      '/api/hub/nodes',
      '/api/hub/invites',
      '/api/hub/trust/pending',
    ]))
  })
})
