import { Context } from '@deepseek-ai/cordis'
import { expect, it, vi } from 'vitest'
import { apply } from '../src/index'

it('keeps the optional plugin active while the initial connection is unavailable', async () => {
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  const ctx = new Context()
  const plugin = ctx.plugin({ apply: (scope: Context) => apply(scope, { connectorConfig: '', command: '' }) })
  try {
    await plugin.await()
    expect(warn).toHaveBeenCalled()
  } finally { await plugin.dispose(); warn.mockRestore() }
})
