// CreateSessionDialog.js -- Modal form for creating a new session.
// Restyled (PR-B) to use the bundle's `.dialog` / `.dh` / `.db` / `.df` /
// `.field` / `.seg-row` / `.btn` classes from app.css.
import { html } from 'htm/preact'
import { useState } from 'preact/hooks'
import {
  createSessionDialogSignal, mutationsEnabledSignal,
  toolFilterFallbackSignal, pickerToolsSignal, hubNodesSignal,
} from './state.js'
import { Icon, ICONS } from './icons.js'
import { apiFetch } from './api.js'
import { menuModelSignal } from './dataModel.js'
import { displayLabelForTool, resolveCreateSessionPickerTools } from './pickerTools.js'

const CUSTOM_MODEL = '__custom__'

const REASONING_EFFORT_CATALOG = {
  claude: [
    { value: 'low', label: 'Low' },
    { value: 'medium', label: 'Medium' },
    { value: 'high', label: 'High' },
    { value: 'xhigh', label: 'Extra high' },
    { value: 'max', label: 'Max' },
  ],
  codex: [
    { value: 'minimal', label: 'Minimal' },
    { value: 'low', label: 'Low' },
    { value: 'medium', label: 'Medium' },
    { value: 'high', label: 'High' },
    { value: 'xhigh', label: 'Extra high' },
  ],
}

const MODEL_ID_CATALOG = {
  claude: [
    { value: 'claude-opus-5', label: 'Claude Opus 5' },
    { value: 'claude-sonnet-5', label: 'Claude Sonnet 5' },
    { value: 'claude-fable-5', label: 'Claude Fable 5' },
    { value: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' },
    { value: 'claude-opus-4-8', label: 'Claude Opus 4.8' },
    { value: 'claude-opus-4-7', label: 'Claude Opus 4.7' },
    { value: 'claude-haiku-4-5', label: 'Claude Haiku 4.5 alias' },
    { value: 'claude-haiku-4-5-20251001', label: 'Claude Haiku 4.5 pinned' },
  ],
  codex: [
    { value: 'gpt-5.6-sol', label: 'GPT-5.6 Sol — Power' },
    { value: 'gpt-5.6-terra', label: 'GPT-5.6 Terra — Balanced' },
    { value: 'gpt-5.6-luna', label: 'GPT-5.6 Luna — Fast' },
    { value: 'gpt-5.6', label: 'GPT-5.6' },
    { value: 'gpt-5.5', label: 'GPT-5.5' },
    { value: 'gpt-5.5-pro', label: 'GPT-5.5 Pro' },
    { value: 'gpt-5.4', label: 'GPT-5.4' },
    { value: 'gpt-5.4-pro', label: 'GPT-5.4 Pro' },
    { value: 'gpt-5.4-mini', label: 'GPT-5.4 Mini' },
    { value: 'gpt-5.4-nano', label: 'GPT-5.4 Nano' },
    { value: 'gpt-5.3-codex', label: 'GPT-5.3 Codex' },
    { value: 'gpt-5.2', label: 'GPT-5.2' },
    { value: 'gpt-5.2-pro', label: 'GPT-5.2 Pro' },
    { value: 'gpt-5.1', label: 'GPT-5.1' },
    { value: 'gpt-5-pro', label: 'GPT-5 Pro' },
    { value: 'gpt-5', label: 'GPT-5' },
    { value: 'gpt-5-mini', label: 'GPT-5 Mini' },
    { value: 'gpt-5-nano', label: 'GPT-5 Nano' },
    { value: 'gpt-4.1', label: 'GPT-4.1' },
    { value: 'gpt-4.1-mini', label: 'GPT-4.1 Mini' },
    { value: 'gpt-4o', label: 'GPT-4o' },
    { value: 'gpt-4o-mini', label: 'GPT-4o Mini' },
    { value: 'o3-pro', label: 'o3 Pro' },
    { value: 'o3', label: 'o3' },
  ],
  gemini: [
    { value: 'gemini-3.1-pro-preview', label: 'Gemini 3.1 Pro preview' },
    { value: 'gemini-3.1-pro-preview-customtools', label: 'Gemini 3.1 Pro custom tools' },
    { value: 'gemini-3-flash-preview', label: 'Gemini 3 Flash preview' },
    { value: 'gemini-3.1-flash-lite', label: 'Gemini 3.1 Flash Lite' },
    { value: 'gemini-3.1-flash-lite-preview', label: 'Gemini 3.1 Flash Lite preview' },
    { value: 'gemini-2.5-pro', label: 'Gemini 2.5 Pro' },
    { value: 'gemini-2.5-flash', label: 'Gemini 2.5 Flash' },
    { value: 'gemini-2.5-flash-lite', label: 'Gemini 2.5 Flash Lite' },
  ],
  opencode: [
    { value: 'openai/gpt-5.5', label: 'OpenAI GPT-5.5' },
    { value: 'openai/gpt-5.5-pro', label: 'OpenAI GPT-5.5 Pro' },
    { value: 'openai/gpt-5.4', label: 'OpenAI GPT-5.4' },
    { value: 'openai/gpt-5.4-pro', label: 'OpenAI GPT-5.4 Pro' },
    { value: 'openai/gpt-5.4-mini', label: 'OpenAI GPT-5.4 Mini' },
    { value: 'openai/gpt-5.3-codex', label: 'OpenAI GPT-5.3 Codex' },
    { value: 'openai/gpt-5', label: 'OpenAI GPT-5' },
    { value: 'openai/o3', label: 'OpenAI o3' },
    { value: 'anthropic/claude-opus-5', label: 'Anthropic Claude Opus 5' },
    { value: 'anthropic/claude-sonnet-5', label: 'Anthropic Claude Sonnet 5' },
    { value: 'anthropic/claude-fable-5', label: 'Anthropic Claude Fable 5' },
    { value: 'anthropic/claude-sonnet-4-6', label: 'Anthropic Claude Sonnet 4.6' },
    { value: 'anthropic/claude-opus-4-8', label: 'Anthropic Claude Opus 4.8' },
    { value: 'anthropic/claude-opus-4-7', label: 'Anthropic Claude Opus 4.7' },
    { value: 'anthropic/claude-haiku-4-5', label: 'Anthropic Claude Haiku 4.5' },
  ],
}

function modelIDsForTool(tool) {
  return MODEL_ID_CATALOG[tool] || []
}

function stringValue(value) {
  if (value == null) return ''
  return String(value).trim()
}

function pushPath(paths, seen, value) {
  const path = stringValue(value)
  if (!path || seen.has(path)) return
  seen.add(path)
  paths.push(path)
}

function pathSuggestionsForTarget(targetHubNodeId, groups, sessions) {
  const paths = []
  const seen = new Set()
  const target = stringValue(targetHubNodeId)
  for (const g of groups || []) {
    if (!g) continue
    if (target) {
      if (!g.isHub || g.hubNodeId !== target) continue
    } else if (g.isHub) {
      continue
    }
    pushPath(paths, seen, g.defaultPath)
  }
  for (const s of sessions || []) {
    if (!s) continue
    if (target) {
      if (!s.isHub || s.hubNodeId !== target) continue
    } else if (s.isHub) {
      continue
    }
    pushPath(paths, seen, s.path || s.projectPath || s.raw?.projectPath)
    pushPath(paths, seen, s.worktreePath || s.raw?.worktreePath)
    for (const p of s.additionalPaths || s.raw?.additionalPaths || []) pushPath(paths, seen, p)
  }
  return paths.sort((a, b) => a.localeCompare(b))
}

function reasoningEffortsForTool(tool) {
  return REASONING_EFFORT_CATALOG[tool] || []
}

export function CreateSessionDialog() {
  const [title, setTitle] = useState('')
  const [tool, setTool] = useState('claude')
  const [modelId, setModelId] = useState('')
  const [customModel, setCustomModel] = useState('')
  const [reasoningEffort, setReasoningEffort] = useState('')
  const [path, setPath] = useState('')
  const [multiRepo, setMultiRepo] = useState(false)
  const [additionalPaths, setAdditionalPaths] = useState([])
  const [additionalPathInput, setAdditionalPathInput] = useState('')
  const [hubNodeId, setHubNodeId] = useState('')
  const [error, setError] = useState(null)
  const [submitting, setSubmitting] = useState(false)

  // WEB-P0-4 prevention layer: when mutations are disabled (server
  // webMutations=false), do not render the dialog at all. Hooks order is
  // preserved by placing this guard AFTER all useState calls.
  if (!mutationsEnabledSignal.value) return null

  function handleSubmit(e) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      const targetHubNode = hubNodeId.trim()
      const pendingAdditional = additionalPathValues()
      const payload = { title, tool, projectPath: targetHubNode ? (path || '.') : path }
      if (pendingAdditional.length > 0) payload.additionalPaths = pendingAdditional
      if (targetHubNode) payload.hubNodeId = targetHubNode
      const modelId = selectedModelId()
      if (modelId) payload.modelId = modelId
      if (reasoningEffort) payload.reasoningEffort = reasoningEffort
      await apiFetch('POST', '/api/sessions', payload)
      createSessionDialogSignal.value = false
    } catch (err) {
      setSubmitting(false)
      setError(err.message || 'failed to create session')
    }
  }

  function selectTool(nextTool) {
    setTool(nextTool)
    setModelId('')
    setCustomModel('')
    setReasoningEffort('')
  }

  function selectedModelId() {
    if (modelId === CUSTOM_MODEL) return customModel.trim()
    return modelId || ''
  }


  function additionalPathValues() {
    if (!multiRepo) return []
    const values = []
    const seen = new Set([stringValue(path)])
    for (const p of additionalPaths) {
      const value = stringValue(p)
      if (!value || seen.has(value)) continue
      seen.add(value)
      values.push(value)
    }
    const pending = stringValue(additionalPathInput)
    if (pending && !seen.has(pending)) values.push(pending)
    return values
  }

  function addAdditionalPath(value = additionalPathInput) {
    const next = stringValue(value)
    if (!next) return
    const existing = new Set([stringValue(path), ...additionalPaths.map(stringValue)])
    if (!existing.has(next)) setAdditionalPaths(paths => [...paths, next])
    setAdditionalPathInput('')
  }

  function removeAdditionalPath(pathToRemove) {
    setAdditionalPaths(paths => paths.filter(p => p !== pathToRemove))
  }

  const close = () => (createSessionDialogSignal.value = false)
  const handleBackdropClick = (e) => { if (e.target === e.currentTarget) close() }
  const modelIDs = modelIDsForTool(tool)
  const reasoningEfforts = reasoningEffortsForTool(tool)
  const shownTools = resolveCreateSessionPickerTools(pickerToolsSignal.value)
  const needsCustomModel = modelId === CUSTOM_MODEL
  const { groups, sessions } = menuModelSignal.value
  const hubNodes = []
  const seenHubNodes = new Set()
  for (const n of hubNodesSignal.value || []) {
    if (!n?.id || seenHubNodes.has(n.id)) continue
    hubNodes.push({ id: n.id, name: n.name || n.id })
    seenHubNodes.add(n.id)
  }
  for (const g of groups || []) {
    if (!g?.isHub || !g.hubNodeId || seenHubNodes.has(g.hubNodeId)) continue
    hubNodes.push({ id: g.hubNodeId, name: g.hubNodeName || g.hubNodeId })
    seenHubNodes.add(g.hubNodeId)
  }
  for (const s of sessions || []) {
    if (!s?.isHub || !s.hubNodeId || seenHubNodes.has(s.hubNodeId)) continue
    hubNodes.push({ id: s.hubNodeId, name: s.hubNodeName || s.hubNodeId })
    seenHubNodes.add(s.hubNodeId)
  }
  hubNodes.sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id))
  const creatingOnHub = !!hubNodeId.trim()
  const pathSuggestions = pathSuggestionsForTarget(hubNodeId, groups, sessions)
  const pathListID = creatingOnHub ? 'create-session-hub-paths' : 'create-session-local-paths'
  const additionalSuggestions = pathSuggestions.filter(p => p !== stringValue(path) && !additionalPaths.includes(p))
  const submitDisabled = submitting || !title || (!creatingOnHub && !path) || (multiRepo && additionalPathValues().length === 0) || (needsCustomModel && !customModel.trim())

  return html`
    <div class="overlay" onClick=${handleBackdropClick}>
      <form class="dialog" onClick=${e => e.stopPropagation()} onSubmit=${handleSubmit}>
        <div class="dh">
          <span class="kicker">NEW</span>
          <div class="t">New session</div>
          <button type="button" class="icon-btn" onClick=${close} aria-label="Close">
            <${Icon} d=${ICONS.x}/>
          </button>
        </div>
        <div class="db">
          <div class="field">
            <label>TITLE</label>
            <input autofocus required value=${title} onInput=${e => setTitle(e.target.value)} placeholder="my-session"/>
          </div>
          <div class="field">
            <label>TARGET</label>
            <select value=${hubNodeId} onInput=${e => setHubNodeId(e.target.value)}>
              <option value="">Local</option>
              ${hubNodes.map(n => html`<option key=${n.id} value=${n.id}>${n.name}</option>`)}
            </select>
          </div>
          <div class="field">
            <label>WORKING DIR</label>
            <input required=${!creatingOnHub} value=${path} onInput=${e => setPath(e.target.value)}
                   list=${pathSuggestions.length ? pathListID : undefined}
                   placeholder=${creatingOnHub ? ". on selected hub node" : "/absolute/path/to/project"}/>
            ${pathSuggestions.length > 0 && html`
              <datalist id=${pathListID}>
                ${pathSuggestions.map(p => html`<option key=${p} value=${p}>${p}</option>`)}
              </datalist>
              <div style="font-family: var(--mono); font-size: 11px; color: var(--tn-comment, #888);
                          margin-top: 5px;">
                ${creatingOnHub ? 'Suggestions come from the selected hub node.' : 'Suggestions come from known local sessions and group defaults.'}
              </div>
            `}
          </div>
          <div class="field">
            <label>PATH MODE</label>
            <label style="display:flex; align-items:center; gap:8px; font-family:var(--mono); font-size:11px; color:var(--muted);">
              <input type="checkbox" checked=${multiRepo} onChange=${e => setMultiRepo(e.currentTarget.checked)}/>
              Multi-repo session
            </label>
          </div>
          ${multiRepo && html`
            <div class="field">
              <label>ADDITIONAL PATHS</label>
              ${additionalPaths.length > 0 && html`
                <div class="path-chip-row">
                  ${additionalPaths.map(p => html`
                    <button type="button" key=${p} class="path-chip" title="Remove path" onClick=${() => removeAdditionalPath(p)}>${p} ×</button>
                  `)}
                </div>
              `}
              <div style=${{ display: 'flex', gap: '6px' }}>
                <input value=${additionalPathInput}
                       onInput=${e => setAdditionalPathInput(e.target.value)}
                       onKeyDown=${e => { if (e.key === 'Enter') { e.preventDefault(); addAdditionalPath() } }}
                       list=${pathSuggestions.length ? pathListID : undefined}
                       placeholder=${creatingOnHub ? "/path/on/selected/hub/node" : "/absolute/path/to/another/repo"}/>
                <button type="button" class="btn ghost" onClick=${() => addAdditionalPath()} disabled=${!stringValue(additionalPathInput)}>Add</button>
              </div>
              ${additionalSuggestions.length > 0 && html`
                <div class="path-chip-row suggestions" data-testid="create-session-path-suggestions">
                  ${additionalSuggestions.slice(0, 8).map(p => html`
                    <button type="button" key=${p} class="path-chip" onClick=${() => addAdditionalPath(p)}>+ ${p}</button>
                  `)}
                </div>
              `}
              <div style="font-family: var(--mono); font-size: 11px; color: var(--tn-comment, #888); margin-top: 5px;">
                Multi-repo creates use the working dir as the primary repo and these paths as additional repos.
              </div>
            </div>
          `}
          <div class="field">
            <label>TOOL</label>
            <div class="seg-row tool-picker-row">
              ${shownTools.map(t => html`
                <button type="button" key=${t}
                        class=${`seg-btn ${tool === t ? 'on' : ''}`}
                        onClick=${() => selectTool(t)}>${displayLabelForTool(t)}</button>
              `)}
            </div>
            ${toolFilterFallbackSignal.value && html`
              <div style="font-family: var(--mono); font-size: 11px; color: var(--tn-comment, #888);
                          margin-top: 6px;">
                No tools matched PATH; showing all. Set <code>show_only_installed_tools = false</code> to silence.
              </div>
            `}
          </div>
          ${modelIDs.length > 0 && html`
            <div class="field">
              <label>MODEL ID</label>
              <select value=${modelId} onInput=${e => setModelId(e.target.value)}>
                <option value="">Tool default</option>
                ${modelIDs.map(m => html`
                  <option key=${m.value} value=${m.value}>${m.value} — ${m.label}</option>
                `)}
                <option value=${CUSTOM_MODEL}>Custom model ID…</option>
              </select>
            </div>
            ${needsCustomModel && html`
              <div class="field">
                <label>MODEL ID</label>
                <input required value=${customModel} onInput=${e => setCustomModel(e.target.value)} placeholder="provider/model-or-version"/>
              </div>
            `}
          `}
          ${reasoningEfforts.length > 0 && html`
            <div class="field">
              <label>REASONING EFFORT</label>
              <select value=${reasoningEffort} onInput=${e => setReasoningEffort(e.target.value)}>
                <option value="">Tool default</option>
                ${reasoningEfforts.map(effort => html`
                  <option key=${effort.value} value=${effort.value}>${effort.label} — ${effort.value}</option>
                `)}
              </select>
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
          <button type="submit" class="btn primary" disabled=${submitDisabled}>
            ${submitting ? 'Creating…' : html`Create session <span class="kbd">⏎</span>`}
          </button>
        </div>
      </form>
    </div>
  `
}
