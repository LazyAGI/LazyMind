/** Shared local CDP transport; each host supplies its own target identity. */
export class CdpClient {
  constructor(private port: number, private selectTarget: (targets: any[]) => any) {
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('Invalid CDP port')
  }
  async evaluate<T>(expression: string, signal: AbortSignal, timeoutMs = 8000): Promise<T> {
    const bounded = AbortSignal.any([signal, AbortSignal.timeout(timeoutMs)])
    const response = await fetch(`http://127.0.0.1:${this.port}/json/list`, { signal: bounded, redirect: 'error' })
    if (!response.ok) throw new Error('CDP discovery failed')
    const page = this.selectTarget(await response.json() as any[])
    const url = new URL(page.webSocketDebuggerUrl)
    if (url.protocol !== 'ws:' || url.hostname !== '127.0.0.1' || Number(url.port) !== this.port || url.username || url.password) {
      throw new Error('Unexpected CDP endpoint')
    }
    bounded.throwIfAborted()
    const socket = new WebSocket(url)
    return await new Promise<T>((resolve, reject) => {
      let settled = false
      const finish = (error?: Error, value?: T) => {
        if (settled) return
        settled = true
        bounded.removeEventListener('abort', abort)
        socket.close()
        error ? reject(error) : resolve(value as T)
      }
      const abort = () => finish(new Error('CDP operation interrupted; admission may be unknown'))
      bounded.addEventListener('abort', abort, { once: true })
      socket.addEventListener('open', () => {
        if (settled) return
        socket.send(JSON.stringify({ id: 1, method: 'Runtime.evaluate', params: {
          expression, awaitPromise: true, returnByValue: true,
        } }))
      })
      socket.addEventListener('message', event => {
        try {
          const value = JSON.parse(String(event.data))
          if (value.id !== 1) return
          if (value.error || value.result?.exceptionDetails) return finish(new Error('Desktop evaluation failed'))
          finish(undefined, value.result?.result?.value)
        } catch { finish(new Error('Invalid CDP response')) }
      })
      socket.addEventListener('error', () => finish(new Error('CDP unavailable')))
      socket.addEventListener('close', () => finish(new Error('CDP disconnected')))
      if (bounded.aborted) abort()
    })
  }
}
