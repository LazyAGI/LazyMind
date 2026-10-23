import { expect, it, vi } from 'vitest'
import { presentationLoop, type PresentationStatus } from '../src/presentation'
import type { Cdp } from '../src/cdp'

vi.mock('node:timers/promises', () => ({ setTimeout: () => new Promise(resolve => setImmediate(resolve)) }))

it('recovers discovery/reconnection without blocking the display loop and disposes once on stop', async () => {
  const lifetime = new AbortController()
  const statuses: PresentationStatus[] = []
  let attempts = 0
  const evaluate = vi.fn(async (expression: string) => {
    if (expression.includes('function readPresentationSession')) {
      if (++attempts === 1) throw Error('CDP offline')
      return { session_id: 'native-1', epoch: 'epoch-1' }
    }
    if (expression.startsWith('window.__lazymindWorkflowTail?.dispose()')) return undefined
    return { status: 'mounted', page_status: 'ready' }
  })
  const request = vi.fn(async () => ({ session_id: 'native-1', run_id: 'run-1' }))
  await presentationLoop({ evaluate } as unknown as Cdp, request, 'http://127.0.0.1:19091', lifetime.signal, value => {
    statuses.push(value)
    if (value.status === 'mounted') lifetime.abort()
  })
  expect(statuses.map(x => x.status)).toEqual(['host-offline', 'mounted', 'disabled'])
  expect(request).toHaveBeenCalledOnce()
  expect(evaluate.mock.calls.filter(([e]) => e.startsWith('window.__lazymindWorkflowTail?.dispose()'))).toHaveLength(1)
})

it('does not inject a late lookup after disable', async () => {
  const lifetime = new AbortController()
  const evaluate = vi.fn(async (expression: string) => expression.includes('function readPresentationSession') ? { session_id: 'native-1', epoch: 'epoch-1' } : undefined)
  const request = vi.fn(async () => {
    lifetime.abort()
    return { session_id: 'native-1', run_id: 'run-1' }
  })
  await presentationLoop({ evaluate } as unknown as Cdp, request, 'http://127.0.0.1:19091', lifetime.signal)
  expect(evaluate).toHaveBeenCalledTimes(2) // discovery + final cleanup, no mount
  expect(evaluate.mock.calls[1][0]).toContain('dispose()')
})
