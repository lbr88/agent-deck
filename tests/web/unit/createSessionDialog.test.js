import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { render } from 'preact'
import { html } from 'htm/preact'

import { CreateSessionDialog } from '../../../internal/web/static/app/CreateSessionDialog.js'
import {
  createSessionDialogSignal,
  hubNodesSignal,
  mutationsEnabledSignal,
  pickerToolsSignal,
  sessionsSignal,
} from '../../../internal/web/static/app/state.js'

describe('CreateSessionDialog module', () => {
  beforeEach(() => {
    createSessionDialogSignal.value = true
    hubNodesSignal.value = []
    mutationsEnabledSignal.value = true
    pickerToolsSignal.value = ['claude', 'codex']
    sessionsSignal.value = []
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('loads and renders the new-session form', () => {
    const root = document.createElement('div')
    document.body.append(root)

    render(html`<${CreateSessionDialog}/>`, root)

    expect(root.querySelector('form')).not.toBeNull()
    expect(root.textContent).toContain('New session')
    expect(root.textContent).toContain('WORKING DIR')
  })
})
