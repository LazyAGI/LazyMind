import { describe, expect, it } from 'vitest'
import { watchConnection, type Connection } from '../src/connection'

const tick = () => new Promise(resolve => setTimeout(resolve, 15))
const value = (webUrl: string): Connection => ({ provider: 'deepseek-harness', webUrl, bridgeUrl: 'http://localhost:19091', pairingFile: '/private/pair.json', serverName: 'lazymind', revision: 'one' })

describe('live DSH connection', () => {
  it('replaces only changed connections, releases before install, and stops on invalidation', async () => {
    let current: Connection | undefined = value('http://localhost:8090')
    const events: string[] = []
    const stop = watchConnection(async () => { if (!current) throw new Error('revoked'); return current }, async connection => {
      events.push(`start:${connection.webUrl}`)
      return async () => { events.push(`stop:${connection.webUrl}`) }
    }, () => {}, 2)
    try {
      await tick()
      expect(events).toEqual(['start:http://localhost:8090'])
      current = value('http://localhost:8190')
      await tick()
      expect(events).toEqual(['start:http://localhost:8090', 'stop:http://localhost:8090', 'start:http://localhost:8190'])
      current = undefined
      await tick()
      expect(events.at(-1)).toBe('stop:http://localhost:8190')
      current = value('http://localhost:8290')
      await tick()
    } finally { await stop() }
    expect(events.at(-1)).toBe('stop:http://localhost:8290')
  })
  it('reports initial failure before the host is advertised as ready', async () => {
    const stop = watchConnection(async () => { throw new Error('missing connection') }, async () => async () => {}, () => {}, 2)
    await expect(stop.ready).rejects.toThrow('missing connection')
    await stop()
  })
  it('cannot install a resolver result after disposal' , async () => {
    let complete!: (c: Connection) => void
    const resolved = new Promise<Connection>(resolve => { complete = resolve })
    let installs = 0
    const stop = watchConnection(() => resolved, async () => { installs++; return async () => {} }, () => {}, 2)
    const stopped = stop()
    complete(value('http://localhost:8090'))
    await stopped
    expect(installs).toBe(0)
  })
})
