import type { Context as ClientContext } from '@deepseek-ai/cordis'
import type { ConversationNodeDefinition } from '@deepseek-ai/dsh-client-ui-conversation/client'
import type {} from '@deepseek-ai/dsh-client-ui-layout/client'
import type {} from '@deepseek-ai/dsh-client-ui-renderer/client'
import type {} from '@deepseek-ai/dsh-tools/types'
import type { ChatNode } from '@deepseek-ai/dsh-client-ui-chat/client'
import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { eventRun, type RunLink } from '../protocol'
import { runKey, windowStore } from './window-store'

declare module '@deepseek-ai/dsh-client-ui-chat/client' {
  interface ChatNodeDataMap { 'lazymind-workflow': RunLink }
}

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
    if (first === undefined || first !== node.anchorSeq
      || snapshot.entries[run.hostSessionId ?? '']?.run.runId === run.runId) return null
    return <section style={{ margin: '8px 0', border: '1px solid #d9d9d9', borderRadius: 8, padding: 12 }}>
      <strong>LazyMind Workflow</strong>
      <button style={{ marginLeft: 12 }} onClick={() => windows.open(run, node.anchorSeq)}>Open workflow</button>
    </section>
  }

  function WorkflowDock({ session }: { session: { sessionId: string } }) {
    const state = useSyncExternalStore(windows.subscribe, windows.snapshot, windows.snapshot)
    const current = state.entries[session.sessionId]
    const frame = useRef<HTMLIFrameElement>(null)
    const [expanded, setExpanded] = useState(false)
    const [collapsed, setCollapsed] = useState(false)
    const runId = current?.run.runId
    const origin = current ? new URL(current.run.url).origin : undefined
    useEffect(() => { setExpanded(false); setCollapsed(false) }, [runId, session.sessionId])
    useEffect(() => {
      const receive = (event: MessageEvent) => {
        if (!origin || event.origin !== origin || event.source !== frame.current?.contentWindow
          || event.data?.sessionId !== runId) return
        if (event.data.type === 'lazymind.workflow.toggle-expand') setExpanded(value => {
          if (!value) setCollapsed(false)
          return !value
        })
        if (event.data.type === 'lazymind.workflow.toggle-collapse') setCollapsed(value => !value)
      }
      const keydown = (event: KeyboardEvent) => { if (event.key === 'Escape') setExpanded(false) }
      window.addEventListener('message', receive)
      window.addEventListener('keydown', keydown)
      return () => { window.removeEventListener('message', receive); window.removeEventListener('keydown', keydown) }
    }, [origin, runId])
    const syncExpansion = () => {
      if (origin) frame.current?.contentWindow?.postMessage({ type: 'lazymind.workflow.expansion', sessionId: runId, expanded }, origin)
      if (origin) frame.current?.contentWindow?.postMessage({ type: 'lazymind.workflow.collapse', sessionId: runId, collapsed }, origin)
    }
    useEffect(syncExpansion, [expanded, collapsed, origin, runId])
    if (!current) return null
    const url = new URL(`/workflow-runs/${encodeURIComponent(current.run.runId)}/embed`, origin)
    url.searchParams.set('hostOrigin', window.location.origin)
    return <section aria-label="LazyMind Workflow" style={{
      height: collapsed && !expanded ? 66 : 'min(480px, 55dvh)', minHeight: 0, flexShrink: 0,
      width: '100%', maxWidth: 'var(--dsh-chat-content-width, 920px)', alignSelf: 'center', boxSizing: 'border-box',
      display: current.minimized ? 'none' : 'flex', flexDirection: 'column',
      ...(expanded ? { position: 'fixed', inset: 16, width: 'auto', maxWidth: 'none', alignSelf: 'stretch', height: 'auto', zIndex: 1000 } as const : {}),
      background: '#fff', borderRadius: 10, overflow: 'hidden',
    }}>
      <iframe ref={frame} key={runKey(current.run)} title="LazyMind Workflow" src={url.href}
        onLoad={syncExpansion} style={{ width: '100%', height: '100%', flex: 1, minHeight: 0, border: 0 }} />
    </section>
  }
  ctx.uiConversation.events.register(definition)
  ctx.slots.inject('conversation.chat.node', () => ctx.slots.register({ name: 'conversation.chat.node', key: 'lazymind-workflow' }, Entry))
  ctx.slots.inject('conversation.input.dock', () => ctx.slots.register({ name: 'conversation.input.dock', id: 'lazymind-workflow', order: -10 }, WorkflowDock))
}
