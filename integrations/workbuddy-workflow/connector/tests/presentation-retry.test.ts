import { expect, it, vi } from 'vitest'
import { presentationLoop } from '../src/presentation'
import type { Cdp } from '../src/cdp'

it.each([503, 403])('retains the iframe on temporary failure but removes it on auth rejection (%s)', async status => {
  const lifetime = new AbortController()
  const evaluate = vi.fn(async (expression: string) => expression.includes('function readPresentationSession') ? { session_id: 'native-1', epoch: 'epoch-1' } : undefined)
  let reject!: (error: Error) => void
  const request = vi.fn(() => new Promise((_, fail) => { reject = fail }))
  const loop = presentationLoop({ evaluate } as unknown as Cdp, request, 'http://127.0.0.1:19091', lifetime.signal)
  try {
    await vi.waitFor(() => expect(request).toHaveBeenCalledOnce())
    reject(Object.assign(new Error('unavailable'), { status }))
    // Finish the rejection microtasks before asserting, without retrying polls.
    await new Promise(resolve => setImmediate(resolve))
    const disposals = evaluate.mock.calls.filter(([expression]) => expression.startsWith('window.__lazymindWorkflowTail?.dispose()'))
    expect(disposals).toHaveLength(status === 403 ? 1 : 0)
  } finally { lifetime.abort(); await loop }
})

it('reports the home page as waiting for a session and cleans up on shutdown', async () => {
  const lifetime = new AbortController()
  const evaluate = vi.fn(async (expression: string) => expression.includes('function readPresentationSession')
    ? { session_id: null, epoch: 'home' } : undefined)
  const states: string[] = []
  await presentationLoop({ evaluate } as unknown as Cdp, vi.fn(), 'http://127.0.0.1:19091', lifetime.signal, value => {
    states.push(value.status)
    if (value.status === 'waiting-session') lifetime.abort()
  })
  expect(states).toEqual(['waiting-session', 'disabled'])
  expect(evaluate.mock.calls.at(-1)?.[0]).toContain('__lazymindWorkflowPresentationSession?.dispose()')
})
