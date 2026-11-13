import { expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import type { ConversationNodeDefinition } from '@deepseek-ai/dsh-client-ui-conversation/client'
import type { PresentationConfig } from '../src/presentation-config'
import { apply } from '../src/client'

const observer = vi.hoisted(() => ({ update: undefined as undefined | ((value: PresentationConfig | undefined) => void), stop: vi.fn() }))
vi.mock('../src/presentation-config', () => ({ observePresentationConfig: (_scope: unknown, update: typeof observer.update) => {
  observer.update = update
  update?.(undefined)
  return observer.stop
} }))

it('registers historical workflow projection after a cold login recovery and unregisters on loss', () => {
  let definition: ConversationNodeDefinition | undefined
  const disposers: Array<() => void> = []
  const register = vi.fn((value: ConversationNodeDefinition) => {
    definition = value
    return () => { definition = undefined }
  })
  const ctx = { effect: (effect: () => () => void) => { disposers.push(effect()) },
    uiConversation: { events: { register } }, slots: { inject: vi.fn() } } as unknown as Context
  apply(ctx)
  expect(register).not.toHaveBeenCalled()
  observer.update?.({ serverName: 'lazymind', webUrl: 'http://localhost:8090' })
  expect(register).toHaveBeenCalledTimes(1)
  const historical = { type: 'tool/result', seq: 42, data: {
    content: [{ type: 'text', text: JSON.stringify({ control: {
      protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
      continuation: 'awaiting_user', admission: { can_begin: false },
    } }) }],
  } }
  expect(definition?.match(historical as never)).toEqual({ id: 'run-1:42', role: 'start' })
  observer.update?.(undefined)
  expect(definition).toBeUndefined()
  observer.update?.({ serverName: 'lazymind', webUrl: 'http://localhost:8091' })
  expect(register).toHaveBeenCalledTimes(2)
  for (const dispose of disposers.reverse()) dispose()
  expect(observer.stop).toHaveBeenCalled()
  expect(definition).toBeUndefined()
})
