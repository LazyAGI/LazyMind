import { expect, it, vi } from 'vitest'
import { presentationLoop } from '../src/presentation'
import type { Cdp } from '../src/cdp'
import type { BindingResult } from '../../../workflow-agent-core/src/adapter'

it('unmounts the current session panel when binding access is denied', async () => {
  const lifetime = new AbortController()
  const evaluate = vi.fn(async (expression: string) => {
    if (expression.includes('function readPresentationSession')) return { session_id: 'native-1', epoch: 'epoch-1' }
    if (expression.includes('?.wait(')) await new Promise(resolve => lifetime.signal.addEventListener('abort', resolve, { once: true }))
    return { status: 'unbound' }
  })
  let finish!: (result: BindingResult) => void
  const request = vi.fn(() => new Promise<BindingResult>(resolve => { finish = resolve }))
  const loop = presentationLoop({ evaluate } as unknown as Cdp, request, 'http://127.0.0.1:19091', lifetime.signal)
  try {
    await vi.waitFor(() => expect(request).toHaveBeenCalledOnce())
    finish({ status: 'access-denied' })
    await new Promise(resolve => setImmediate(resolve))
    const disposals = evaluate.mock.calls.filter(([expression]) => expression.includes('"run_id":""'))
    expect(disposals).toHaveLength(1)
    expect(disposals[0][0]).toContain('"session_id":"native-1"')
    expect(disposals[0][0]).toContain('"epoch":"epoch-1"')
  } finally { lifetime.abort(); await loop }
})

it('reports the home page as waiting for a session and cleans up on shutdown', async () => {
  const lifetime = new AbortController()
  const evaluate = vi.fn(async (expression: string) => {
    if (expression.includes('function readPresentationSession')) return { session_id: null, epoch: 'home' }
    if (expression.includes('?.wait(')) await new Promise(resolve => lifetime.signal.addEventListener('abort', resolve, { once: true }))
    return { status: 'unbound' }
  })
  const states: string[] = []
  await presentationLoop({ evaluate } as unknown as Cdp, vi.fn(), 'http://127.0.0.1:19091', lifetime.signal, value => {
    states.push(value.status)
    if (value.status === 'waiting-session') lifetime.abort()
  })
  expect(states).toEqual(['waiting-session', 'disabled'])
  expect(evaluate.mock.calls.at(-1)?.[0]).toContain('__lazymindWorkflowPresentationSession?.dispose()')
})
