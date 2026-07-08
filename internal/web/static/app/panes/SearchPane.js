// panes/SearchPane.js -- Session search and global Claude conversation search.
import { html } from 'htm/preact'
import { useEffect, useState, useMemo } from 'preact/hooks'
import { menuModelSignal } from '../dataModel.js'
import { selectedIdSignal, globalSearchModeSignal } from '../state.js'
import { activeTabSignal } from '../uiState.js'
import { apiFetch } from '../api.js'

export function SearchPane() {
  const { sessions } = menuModelSignal.value
  const globalMode = globalSearchModeSignal.value
  const [q, setQ] = useState('')
  const [globalResults, setGlobalResults] = useState([])
  const [globalStatus, setGlobalStatus] = useState('')

  const filtered = useMemo(() => {
    if (!q) return sessions
    const t = q.toLowerCase()
    return sessions.filter(s =>
      ((s.title || '') + ' ' + (s.path || '') + ' ' + (s.tool || '') + ' ' + (s.group || ''))
        .toLowerCase().includes(t)
    )
  }, [sessions, q])

  useEffect(() => {
    if (!globalMode) return
    const query = q.trim()
    if (!query) {
      setGlobalResults([])
      setGlobalStatus('')
      return
    }
    let cancelled = false
    setGlobalStatus('searching')
    const timer = setTimeout(() => {
      apiFetch('GET', `/api/search/global?q=${encodeURIComponent(query)}&limit=20`)
        .then(data => {
          if (cancelled) return
          setGlobalResults(Array.isArray(data.results) ? data.results : [])
          setGlobalStatus('')
        })
        .catch(() => {
          if (cancelled) return
          setGlobalResults([])
          setGlobalStatus('error')
        })
    }, 180)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [globalMode, q])

  const onSelect = (id) => {
    selectedIdSignal.value = id
    activeTabSignal.value = 'terminal'
  }
  const onGlobalSelect = (result) => {
    const local = sessions.find(s => s.raw?.claudeSessionId === result.sessionId || s.id === result.sessionId)
    if (local) onSelect(local.id)
  }
  const showGlobal = globalMode
  const resultCount = showGlobal ? globalResults.length : filtered.length

  return html`
    <div class="search-wrap" data-testid="search-pane">
      <div class="field">
        <label>${showGlobal ? 'GLOBAL CONVERSATION SEARCH' : 'SESSION SEARCH'}</label>
        <input autofocus placeholder=${showGlobal ? 'Search Claude conversation content…' : 'Search sessions by title, path, tool, group…'}
               data-testid="search-input"
               value=${q} onInput=${e => setQ(e.target.value)}/>
      </div>
      <div class="seg-row" data-testid="search-mode-toggle">
        <button class=${`seg-btn ${!showGlobal ? 'on' : ''}`} onClick=${() => (globalSearchModeSignal.value = false)}>Sessions</button>
        <button class=${`seg-btn ${showGlobal ? 'on' : ''}`} onClick=${() => (globalSearchModeSignal.value = true)}>Global</button>
      </div>
      <div data-testid="search-result-count" style="font-family: var(--mono); font-size: 10.5px; color: var(--muted); letter-spacing: 0.08em;">
        ${resultCount} MATCH${resultCount === 1 ? '' : 'ES'}${showGlobal ? ' · Claude conversation index' : ' · current Agent Deck sessions'}
      </div>
      ${showGlobal
        ? html`
          ${globalStatus === 'searching' && html`<div class="search-empty">Searching…</div>`}
          ${globalStatus === 'error' && html`<div class="search-empty">Global search failed.</div>`}
          ${q.trim() === '' && html`<div class="search-empty">Type to search Claude conversation content.</div>`}
          ${globalResults.map(result => html`
            <div key=${result.sessionId + result.filePath}
                 class="sr"
                 data-testid="global-search-result"
                 data-session-id=${result.sessionId}
                 onClick=${() => onGlobalSelect(result)}>
              <div class="sr-h">
                <span class="s">${result.summary || result.sessionId}</span>
                <span class="w">${result.matchCount || 0} hit${result.matchCount === 1 ? '' : 's'} · score ${result.score || 0}</span>
              </div>
              <div class="sr-b">${result.snippet || result.cwd || result.filePath || ''}</div>
            </div>
          `)}
        `
        : filtered.map(s => html`
          <div key=${s.id} class="sr" data-testid="search-result" data-session-id=${s.id} onClick=${() => onSelect(s.id)}>
            <div class="sr-h">
              <span class="s">${s.title}</span>
              <span class="w">${s.tool || '—'} · ${s.status}</span>
            </div>
            <div class="sr-b">${s.path || s.group || ''}</div>
          </div>
        `)}
    </div>
  `
}
