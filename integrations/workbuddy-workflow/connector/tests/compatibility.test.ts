import { afterEach, expect, it, vi } from 'vitest'
import { workbuddyVersionIssue } from '../src/compatibility'
import { WorkBuddyAdapter } from '../src/adapter'

afterEach(() => vi.unstubAllGlobals())
it.each([
 ['5.7.7', 'unsupported_version'], ['5.8.2', 'unsupported_version'], ['5.8.3-rc.1', 'unsupported_version'],
 ['5.8.3', ''], ['5.8.3+build.1', ''], ['5.8.10', ''], ['5.10.0', ''], ['6.0.0', ''], ['5.9.0-rc.1', ''],
 ['', 'version_unreadable'], ['v5.8.3', 'version_unreadable'], ['5.8', 'version_unreadable'],
 ['5.08.3', 'version_unreadable'], ['5.8.3-01', 'version_unreadable'], ['5.8.3-', 'version_unreadable'],
])('classifies %s', (version, issue) => expect(workbuddyVersionIssue(version)).toBe(issue))

it('serializes the version helper and reports capability failures without touching sessions', async () => {
 const get = vi.fn(), list = vi.fn()
 vi.stubGlobal('workbuddyDesktop', { appVersion: '6.0.0' })
 vi.stubGlobal('wb', { conversations: { get, local: { list } } })
 // The renderer receives only an expression, with no module bindings.
 const adapter = new WorkBuddyAdapter({ evaluate: async (expression: string) => (0, eval)(expression) })
 expect(await adapter.probe(AbortSignal.timeout(1000))).toEqual({ version: '6.0.0' })
 expect(get).not.toHaveBeenCalled(); expect(list).not.toHaveBeenCalled()
 vi.stubGlobal('wb', { conversations: { get } })
 expect(await adapter.probe(AbortSignal.timeout(1000))).toEqual({ version: '6.0.0', issue: 'capability_unavailable' })
 vi.stubGlobal('workbuddyDesktop', {})
 expect(await adapter.probe(AbortSignal.timeout(1000))).toEqual({ version: '', issue: 'version_unreadable' })
})
