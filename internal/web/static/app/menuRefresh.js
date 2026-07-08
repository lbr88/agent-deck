// menuRefresh.js -- shared menu snapshot refresh helper.
//
// The initial boot path, SSE fallback, and manual Ctrl/Cmd+R shortcut all need
// the same behavior: fetch /api/menu and replace the shared session/hub-node
// signals with the authoritative server snapshot.
import { apiFetch } from './api.js'
import {
  sessionsSignal,
  hubNodesSignal,
  sessionsLoadedSignal,
  connectionSignal,
} from './state.js'

export async function refreshMenuSnapshot() {
  const data = await apiFetch('GET', '/api/menu')
  sessionsSignal.value = data.items || []
  hubNodesSignal.value = Array.isArray(data.hubNodes) ? data.hubNodes : []
  sessionsLoadedSignal.value = true
  connectionSignal.value = 'connected'
  return data
}
