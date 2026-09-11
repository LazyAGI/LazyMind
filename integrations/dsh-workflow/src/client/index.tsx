import type { Context as ClientContext } from '@deepseek-ai/cordis'
import type { ConversationNodeDefinition } from '@deepseek-ai/dsh-client-ui-conversation/client'
import type {} from '@deepseek-ai/dsh-client-ui-layout/client'
import type {} from '@deepseek-ai/dsh-client-ui-renderer/client'
import type {} from '@deepseek-ai/dsh-tools/types'
import type { ChatNode } from '@deepseek-ai/dsh-client-ui-chat/client'
import { useEffect, useRef, useSyncExternalStore, type CSSProperties, type PointerEvent as ReactPointerEvent } from 'react'
import { eventRun, type RunLink } from '../protocol'
import {
  type ResizeEdge, type WindowRect, RESIZE_EDGES, clampRect, defaultWindowRect, moveRect, resizeHandleStyle, resizeRect,
} from './window-geometry'
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
    const drag = useRef<{ offsetX: number; offsetY: number; start: WindowRect }>()
    const resize = useRef<{ edge: ResizeEdge; originX: number; originY: number; start: WindowRect }>()
    if (!current || !sessionId) return null
    if (current.minimized) return <button style={{ position: 'absolute', right: 24, bottom: 24, pointerEvents: 'auto', zIndex: 1 }}
      onClick={() => windows.open(current.run, current.anchor)}>Open LazyMind Workflow</button>
    const viewport = () => ({ width: window.innerWidth, height: window.innerHeight })
    const measured = (): WindowRect => {
      const box = panel.current?.getBoundingClientRect()
      return clampRect(box
        ? { left: box.left, top: box.top, width: box.width, height: box.height }
        : current.layout ?? defaultWindowRect(viewport()), viewport())
    }
    const move = (event: ReactPointerEvent<HTMLElement>) => {
      if (!drag.current) return
      windows.place(sessionId, moveRect(drag.current.start, event.clientX - drag.current.offsetX,
        event.clientY - drag.current.offsetY, viewport()))
    }
    const beginDrag = (event: ReactPointerEvent<HTMLElement>) => {
      if (!panel.current || event.target instanceof HTMLButtonElement) return
      const start = measured()
      drag.current = { offsetX: event.clientX - start.left, offsetY: event.clientY - start.top, start }
      windows.place(sessionId, start)
      event.currentTarget.setPointerCapture(event.pointerId)
    }
    const endDrag = () => { drag.current = undefined }
    const moveResize = (event: ReactPointerEvent<HTMLElement>) => {
      if (!resize.current) return
      windows.place(sessionId, resizeRect(resize.current.start, resize.current.edge,
        { x: event.clientX - resize.current.originX, y: event.clientY - resize.current.originY }, viewport()))
    }
    const beginResize = (edge: ResizeEdge, event: ReactPointerEvent<HTMLElement>) => {
      event.stopPropagation()
      const start = measured()
      resize.current = { edge, originX: event.clientX, originY: event.clientY, start }
      windows.place(sessionId, start)
      event.currentTarget.setPointerCapture(event.pointerId)
    }
    const endResize = () => { resize.current = undefined }
    const url = new URL(`/workflow-runs/${encodeURIComponent(current.run.runId)}/embed`, new URL(current.run.url).origin).href
    const layoutStyle = current.layout
      ? { left: current.layout.left, top: current.layout.top, width: current.layout.width, height: current.layout.height }
      : { right: 20, top: '50%', transform: 'translateY(-50%)' as const,
        width: 'min(960px, calc(100vw - 40px))', height: 'min(720px, calc(100vh - 40px))' }
    return <section ref={panel} role="dialog" aria-label="LazyMind Workflow" style={{ position: 'absolute',
      ...layoutStyle, boxSizing: 'border-box', background: '#fff', color: '#111', border: '1px solid #d9d9d9',
      borderRadius: 10, boxShadow: '0 12px 48px rgba(0, 0, 0, .24)', overflow: 'hidden', display: 'flex',
      flexDirection: 'column', pointerEvents: 'auto', zIndex: 1 }}>
      <header onPointerDown={beginDrag} onPointerMove={move} onPointerUp={endDrag} onPointerCancel={endDrag}
        style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, padding: 12,
          borderBottom: '1px solid #d9d9d9', cursor: 'grab', touchAction: 'none', userSelect: 'none', flexShrink: 0 }}>
        <strong>LazyMind Workflow</strong>
        <span>
          <button onPointerDown={event => event.stopPropagation()} onClick={() => windows.minimize(sessionId)}>Minimize</button>
          <button style={{ marginLeft: 8 }} onPointerDown={event => event.stopPropagation()} onClick={() => windows.minimize(sessionId)}>Close</button>
        </span>
      </header>
      <iframe title="LazyMind Workflow" src={url} style={{ width: '100%', flex: 1, minHeight: 0, border: 0 }} />
      {RESIZE_EDGES.map(edge => <div key={edge} aria-label={edge === 'se' ? 'Resize workflow window' : undefined}
        onPointerDown={event => beginResize(edge, event)} onPointerMove={moveResize}
        onPointerUp={endResize} onPointerCancel={endResize} style={resizeHandleStyle(edge) as CSSProperties}>
        {edge === 'se' ? <span aria-hidden="true" style={{ position: 'absolute', right: 4, bottom: 4, width: 8, height: 8,
          borderRight: '2px solid #8c8c8c', borderBottom: '2px solid #8c8c8c' }} /> : null}
      </div>)}
    </section>
  }
  ctx.uiConversation.events.register(definition)
  ctx.slots.inject('conversation.chat.node', () => ctx.slots.register({ name: 'conversation.chat.node', key: 'lazymind-workflow' }, Entry))
  ctx.slots.inject('shell.overlay', () => ctx.slots.register({ name: 'shell.overlay', id: 'lazymind-workflow-window' }, WorkflowWindow))
}
