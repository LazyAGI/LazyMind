import type { Context as ClientContext } from '@deepseek-ai/cordis'
import type { ConversationNodeDefinition } from '@deepseek-ai/dsh-client-ui-conversation/client'
import type {} from '@deepseek-ai/dsh-client-ui-chat/client'
import type {} from '@deepseek-ai/dsh-client-ui-conversation/client'
import type {} from '@deepseek-ai/dsh-client-ui-layout/client'
import type {} from '@deepseek-ai/dsh-client-ui-renderer/client'
import type { ChatConversationViewNode } from '@deepseek-ai/dsh-client-ui-chat/client'
import { useEffect, useRef, useState, useSyncExternalStore, type PointerEvent as ReactPointerEvent } from 'react'

type Run = { runId: string; url: string }

declare module '@deepseek-ai/dsh-client-ui-chat/client' {
  interface ChatNodeDataMap {
    'lazymind-workflow': Run
  }
}

const definition: ConversationNodeDefinition<Run> = {
  kind: 'lazymind-workflow',
  target: 'chat',
  match(event) { return event.type === 'lazymind-workflow/open' ? { id: event.data.runId, role: 'start' } : null },
  start(_context, match) {
    if (match.event.type !== 'lazymind-workflow/open') {
      throw new Error('lazymind-workflow start requires lazymind-workflow/open')
    }
    return match.event.data as Run
  },
  update(context) { return context.state },
  buildViewNode(context): ChatConversationViewNode<'lazymind-workflow'> | null {
    if (context.start === undefined) return null
    return {
      key: context.key,
      kind: 'lazymind-workflow',
      id: context.id,
      target: 'chat',
      anchorSeq: context.start.event.seq,
      location: context.start.location,
      visibility: 'visible',
      data: context.state,
    }
  },
}

type WindowState = { readonly run?: Run; readonly minimized: boolean }

let state: WindowState = { minimized: false }
const listeners = new Set<() => void>()
const automaticallyOpened = new Set<string>()

function publish(next: WindowState): void {
  state = next
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function openWorkflow(run: Run): void { publish({ run, minimized: false }) }

/** Keep a visible launch affordance in the transcript while the panel lives above the app frame. */
function Panel({ node }: { node: ChatConversationViewNode<'lazymind-workflow'> }) {
  useEffect(() => {
    if (automaticallyOpened.has(node.data.runId)) return
    automaticallyOpened.add(node.data.runId)
    openWorkflow(node.data)
  }, [node.data])
  return <section style={{ margin: '8px 0', border: '1px solid #d9d9d9', borderRadius: 8, padding: 12 }}>
    <strong>LazyMind Workflow</strong>
    <button style={{ marginLeft: 12 }} onClick={() => openWorkflow(node.data)}>Open workflow</button>
  </section>
}

/** Normalize a legacy interaction URL to the root-level Workflow Run route. */
function workflowPage(run: Run): string {
  const source = new URL(run.url)
  return new URL(`/workflow-runs/${encodeURIComponent(run.runId)}/embed`, source.origin).href
}

/** Fixed app-frame window: it remains visible while the DSH conversation scrolls. */
function WorkflowWindow() {
  const current = useSyncExternalStore(subscribe, () => state, () => state)
  const panel = useRef<HTMLElement>(null)
  const drag = useRef<{ offsetX: number; offsetY: number } | undefined>(undefined)
  const [position, setPosition] = useState<{ left: number; top: number }>()
  if (current.run === undefined) return null
  if (current.minimized) {
    return <button style={{ position: 'absolute', right: 24, bottom: 24, zIndex: 1 }} onClick={() => publish({ ...current, minimized: false })}>
      Open LazyMind Workflow
    </button>
  }
  const move = (event: ReactPointerEvent<HTMLElement>) => {
    if (drag.current === undefined || panel.current === null) return
    const width = panel.current.offsetWidth
    const height = panel.current.offsetHeight
    setPosition({
      left: Math.max(0, Math.min(window.innerWidth - width, event.clientX - drag.current.offsetX)),
      top: Math.max(0, Math.min(window.innerHeight - height, event.clientY - drag.current.offsetY)),
    })
  }
  const beginDrag = (event: ReactPointerEvent<HTMLElement>) => {
    if (panel.current === null || event.target instanceof HTMLButtonElement) return
    const rect = panel.current.getBoundingClientRect()
    drag.current = { offsetX: event.clientX - rect.left, offsetY: event.clientY - rect.top }
    setPosition({ left: rect.left, top: rect.top })
    event.currentTarget.setPointerCapture(event.pointerId)
  }
  const endDrag = () => { drag.current = undefined }
  return <section ref={panel} role="dialog" aria-label="LazyMind Workflow" style={{ position: 'absolute', ...(position === undefined ? { right: 20, top: '50%', transform: 'translateY(-50%)' } : position), width: 'min(760px, calc(100vw - 40px))', height: 'min(460px, calc(100vh - 40px))', background: '#fff', color: '#111', border: '1px solid #d9d9d9', borderRadius: 10, boxShadow: '0 12px 48px rgba(0, 0, 0, .24)', overflow: 'hidden', display: 'flex', flexDirection: 'column', zIndex: 1 }}>
    <header onPointerDown={beginDrag} onPointerMove={move} onPointerUp={endDrag} onPointerCancel={endDrag} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, padding: 12, borderBottom: '1px solid #d9d9d9', cursor: 'grab', touchAction: 'none', userSelect: 'none' }}>
      <strong>LazyMind Workflow</strong>
      <span>
        <button onPointerDown={event => event.stopPropagation()} onClick={() => publish({ ...current, minimized: true })}>Minimize</button>
        <button style={{ marginLeft: 8 }} onPointerDown={event => event.stopPropagation()} onClick={() => publish({ minimized: false })}>Close</button>
      </span>
    </header>
    <iframe title="LazyMind Workflow" src={workflowPage(current.run)} style={{ width: '100%', flex: 1, border: 0 }} />
  </section>
}

/** Required browser services for the durable Conversation Node and its renderer. */
export const inject = ['uiConversation', 'slots']

/** Register the external run event and its WorkflowPanel iframe renderer. */
export function apply(ctx: ClientContext): void {
  ctx.uiConversation.events.register(definition)
  ctx.slots.inject('conversation.chat.node', () => ctx.slots.register({
    name: 'conversation.chat.node',
    key: 'lazymind-workflow',
  }, Panel))
  ctx.slots.inject('shell.overlay', () => ctx.slots.register({
    name: 'shell.overlay',
    id: 'lazymind-workflow-window',
  }, WorkflowWindow))
}
