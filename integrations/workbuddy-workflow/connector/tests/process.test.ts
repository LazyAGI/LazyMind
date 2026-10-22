import { expect, it } from 'vitest'
import { createServer } from 'node:http'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'

it('embedded worker polls independently, excludes a second worker and exits on disconnect', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'wb-connector-'))
  const pairingFile = join(directory, 'pair.json')
  const pairing = { provider: 'workbuddy', enabled: true, connector_id: 'host-' + 'a'.repeat(32), token: 'b'.repeat(64) }
  await writeFile(pairingFile, JSON.stringify(pairing), { mode: 0o600 })
  let polls = 0
  const server = createServer((req, res) => {
    res.setHeader('content-type', 'application/json')
    if (req.url === '/json/list') { res.end('[]'); return }
    if (req.headers.authorization !== `Bearer ${pairing.token}`) { res.statusCode = 403; res.end('{}'); return }
    if (req.url === '/v1/workflow-host/actions') { polls++; res.end('{"actions":[]}'); return }
    res.end('{"requests":[]}')
  })
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = (server.address() as { port: number }).port
  const entry = fileURLToPath(new URL('../../../../local/lazymind-cli/internal/workbuddyhost/assets/connector.mjs', import.meta.url))
  const args = [entry, '--pairing-file', pairingFile, '--bridge-url', `http://127.0.0.1:${port}`, '--cdp-port', String(port)]
  const first = spawn(process.execPath, args, { stdio: ['ignore', 'pipe', 'pipe'] })
  const firstExit = new Promise<number | null>(resolve => first.once('exit', resolve))
  let second: ReturnType<typeof spawn> | undefined
  try {
    const deadline = Date.now() + 5000
    while (polls < 2 && Date.now() < deadline) await delay(50)
    expect(polls).toBeGreaterThanOrEqual(2)
    second = spawn(process.execPath, args, { stdio: ['ignore', 'pipe', 'pipe'] })
    const secondExit = new Promise<number | null>(resolve => second!.once('exit', resolve))
    expect(await Promise.race([secondExit, delay(3000).then(() => 'timeout')])).toBe(1)
    const state = JSON.parse(await readFile(pairingFile + '.workbuddy-status.json', 'utf8'))
    expect(state.ready).toBe(false) // Missing CDP does not kill the independent worker.
    expect(first.exitCode).toBe(null)
    await writeFile(pairingFile, JSON.stringify({ ...pairing, enabled: false }))
    expect(await Promise.race([firstExit, delay(4000).then(() => 'timeout')])).toBe(0)
  } finally {
    if (first.exitCode === null) first.kill('SIGKILL')
    if (second?.exitCode === null) second.kill('SIGKILL')
    await firstExit
    await new Promise<void>(resolve => server.close(() => resolve()))
    await rm(directory, { recursive: true, force: true })
  }
}, 12000)
