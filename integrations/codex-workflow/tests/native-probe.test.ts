import { afterEach, expect, it, vi } from 'vitest'
import { CodexNative } from '../src/native'

const mocked = vi.hoisted(() => ({ targets: vi.fn(), cdp: vi.fn() }))
vi.mock('../src/windows', () => ({ codexTargets: mocked.targets, windowCdp: mocked.cdp, selectCodexTarget: vi.fn() }))

afterEach(() => vi.resetAllMocks())

function probeWindows(results: Array<{ ready: boolean } | Error | undefined>) {
  mocked.targets.mockResolvedValue(results.map((_, index) => ({ id: String(index) })))
  mocked.cdp.mockImplementation((_port, id) => ({
    evaluate: async () => {
      const result = results[Number(id)]
      if (result instanceof Error) throw result
      return result
    },
  }))
  return new CodexNative(9334).probe(AbortSignal.timeout(1000))
}

it('reports a missing chat window without treating it as an incompatible interface', async () => {
  await expect(probeWindows([])).resolves.toEqual({ ready: false, window_count: 0, issue: 'control_window_unavailable' })
  expect(mocked.cdp).not.toHaveBeenCalled()
})

it.each(['WebSocket connection refused', 'CDP operation timed out'])(
  'preserves a transport failure when every window rejects: %s', async message => {
    const result = await probeWindows([new Error(message), new Error('private transport details')])
    expect(result).toEqual({ ready: false, window_count: 2, issue: 'cdp_unavailable' })
    expect(JSON.stringify(result)).not.toContain(message)
    expect(JSON.stringify(result)).not.toContain('private transport details')
  },
)

it('identifies a reachable window whose native interface is not ready', async () => {
  await expect(probeWindows([{ ready: false }])).resolves.toEqual({ ready: false, window_count: 1, issue: 'capability_unavailable' })
})

it('retains valid capability evidence when another window cannot be reached', async () => {
  await expect(probeWindows([new Error('detached window closed'), { ready: false }])).resolves.toEqual({
    ready: false, window_count: 2, issue: 'capability_unavailable',
  })
})

it('accepts a healthy window even when other windows fail or are still initializing', async () => {
  await expect(probeWindows([new Error('detached window closed'), { ready: false }, { ready: true }])).resolves.toEqual({
    ready: true, window_count: 3, issue: '',
  })
})

it('does not classify an invalid probe response as a missing native interface', async () => {
  await expect(probeWindows([undefined])).resolves.toEqual({ ready: false, window_count: 1, issue: 'cdp_unavailable' })
})

it('preserves a discovery failure for the heartbeat transport error handler', async () => {
  const failure = new Error('discovery unavailable')
  mocked.targets.mockRejectedValue(failure)
  await expect(new CodexNative(9334).probe(AbortSignal.timeout(1000))).rejects.toBe(failure)
  expect(mocked.cdp).not.toHaveBeenCalled()
})
