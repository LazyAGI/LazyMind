import type { Context as ClientContext } from '@deepseek-ai/cordis'
import type { ConversationNodeDefinition } from '@deepseek-ai/dsh-client-ui-conversation/client'
import type {} from '@deepseek-ai/dsh-client-ui-layout/client'
import type {} from '@deepseek-ai/dsh-client-ui-renderer/client'
import type {} from '@deepseek-ai/dsh-tools/types'
import type { ChatNode } from '@deepseek-ai/dsh-client-ui-chat/client'
import { useEffect, useRef, useSyncExternalStore, type PointerEvent as ReactPointerEvent } from 'react'
import { eventRun, type RunLink } from '../protocol'
import { runKey, windowStore } from './window-store'

declare module '@deepseek-ai/dsh-client-ui-chat/client' {
  interface ChatNodeDataMap { 'lazymind-workflow': RunLink }
}

// Root slots receive the public runtime session selector, as AppFrame does.
interface SessionSelector { useSessions<T>(selector: (state: { current?: string }) => T): T }
export const inject = ['uiConversation', 'slots']

export function apply(ctx: ClientContext, config: { serverName?: string } = {}): void {
  const windows = windowStore()
  const serverName = config.serverName ?? 'lazymind'
  ctx.effect(() => () => windows.dispose())
  const definition: ConversationNodeDefinition<RunLink> = {
    kind: 'lazymind-workflow', target: 'chat',
    match(event) {
      const run = eventRun(event, serverName)
      // Each standard event is immutable. Reusing runId as a start identity would
      // make a repeated state/start result violate DSH's unique-start contract.
      return run ? { id: `${run.runId}:${event.seq}`, role: 'start' } : null
    },
    start(_context, match) {
      const run = eventRun(match.event, serverName)
      if (!run) throw new Error('Workflow presentation requires a valid standard tool result')
      return run
    },
    update(context) { return context.state },
    buildViewNode(context): ChatNode<'lazymind-workflow'> | null {
      if (!context.start || !context.state) return null
      return { key: context.key, kind: 'lazymind-workflow', id: context.id, target: 'chat',
        anchorSeq: context.start.event.seq, location: context.start.location, visibility: 'visible', data: context.state }
    },
  }

  function Entry({ node, sessionId }: { node: ChatNode<'lazymind-workflow'>; sessionId?: string }) {
    const run = node.data.hostSessionId ? node.data : { ...node.data, hostSessionId: sessionId }
    const snapshot = useSyncExternalStore(windows.subscribe, windows.snapshot, windows.snapshot)
    useEffect(() => { windows.observe(run, node.anchorSeq) }, [node.data, node.anchorSeq, sessionId])
    const first = snapshot.firstCards[runKey(run)]
    if (first !== undefined && first !== node.anchorSeq) return null
    return <section style={{ margin: '8px 0', border: '1px solid #d9d9d9', borderRadius: 8, padding: 12 }}>
      <strong>LazyMind Workflow</strong>
      <button style={{ marginLeft: 12 }} onClick={() => windows.open(run, node.anchorSeq)}>Open workflow</button>
    </section>
  }

  function WorkflowWindow({ useSessions }: SessionSelector) {
    const sessionId = useSessions(state => state.current)
    const state = useSyncExternalStore(windows.subscribe, windows.snapshot, windows.snapshot)
    const current = sessionId ? state.entries[sessionId] : undefined
    const panel = useRef<HTMLElement>(null)
    const drag = useRef<{ offsetX: number; offsetY: number }>()
    if (!current || !sessionId) return null
    if (current.minimized) return <button style={{ position: 'absolute', right: 24, bottom: 24, pointerEvents: 'auto', zIndex: 1 }}
      onClick={() => windows.open(current.run, current.anchor)}>Open LazyMind Workflow</button>
    const move = (event: ReactPointerEvent<HTMLElement>) => {
      if (!drag.current || !panel.current) return
      windows.position(sessionId, {
        left: Math.max(0, Math.min(window.innerWidth - panel.current.offsetWidth, event.clientX - drag.current.offsetX)),
        top: Math.max(0, Math.min(window.innerHeight - panel.current.offsetHeight, event.clientY - drag.current.offsetY)),
      })
    }
    const beginDrag = (event: ReactPointerEvent<HTMLElement>) => {
      if (!panel.current || event.target instanceof HTMLButtonElement) return
      const rect = panel.current.getBoundingClientRect()
      drag.current = { offsetX: event.clientX - rect.left, offsetY: event.clientY - rect.top }
      windows.position(sessionId, { left: rect.left, top: rect.top })
      event.currentTarget.setPointerCapture(event.pointerId)
    }
    const endDrag = () => { drag.current = undefined }
    const url = new URL(`/workflow-runs/${encodeURIComponent(current.run.runId)}/embed`, new URL(current.run.url).origin).href
    return <section ref={panel} role="dialog" aria-label="LazyMind Workflow" style={{ position: 'absolute',
      ...(current.position ?? { right: 20, top: '50%', transform: 'translateY(-50%)' }),
      width: 'min(760px, calc(100vw - 40px))', height: 'min(560px, calc(100vh - 40px))', background: '#fff', color: '#111',
      border: '1px solid #d9d9d9', borderRadius: 10, boxShadow: '0 12px 48px rgba(0, 0, 0, .24)', overflow: 'hidden',
      display: 'flex', flexDirection: 'column', pointerEvents: 'auto', zIndex: 1 }}>
      <header onPointerDown={beginDrag} onPointerMove={move} onPointerUp={endDrag} onPointerCancel={endDrag}
        style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, padding: 12,
          borderBottom: '1px solid #d9d9d9', cursor: 'grab', touchAction: 'none', userSelect: 'none' }}>
        <strong>LazyMind Workflow</strong>
        <span>
          <button onPointerDown={event => event.stopPropagation()} onClick={() => windows.minimize(sessionId)}>Minimize</button>
          <button style={{ marginLeft: 8 }} onPointerDown={event => event.stopPropagation()} onClick={() => windows.minimize(sessionId)}>Close</button>
        </span>
      </header>
      <iframe title="LazyMind Workflow" src={url} style={{ width: '100%', flex: 1, border: 0 }} />
    </section>
  }
  ctx.uiConversation.events.register(definition)
  ctx.slots.inject('conversation.chat.node', () => ctx.slots.register({ name: 'conversation.chat.node', key: 'lazymind-workflow' }, Entry))
  ctx.slots.inject('shell.overlay', () => ctx.slots.register({ name: 'shell.overlay', id: 'lazymind-workflow-window' }, WorkflowWindow))
}
