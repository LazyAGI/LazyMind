import type { Context } from '@deepseek-ai/cordis'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type { GoalService } from '@deepseek-ai/dsh-goal'
import type {} from '@deepseek-ai/dsh-api-session-controller'
import type { SessionId } from '@deepseek-ai/dsh-session'
import { createHash } from 'node:crypto'
import type { RuntimeAdapter } from '../../workflow-agent-core/src/adapter'
import { object } from '../../workflow-agent-core/src/protocol'
import { eventRun } from './events'

/** DSH SDK translation only. Workflow admission and delivery decisions live in the shared runtime. */
export function dshRuntime(ctx: Context, serverName: string): RuntimeAdapter<Agent> {
  let goals: GoalService | undefined
  ctx.inject(['goals'], goalCtx => {
    goals = goalCtx.goals
    goalCtx.effect(() => () => { goals = undefined })
  })
  function goalReason(runId: string, revision: number, stopped = false) {
    const run = createHash('sha256').update(runId).digest('hex').slice(0, 16)
    return `lazymind-${stopped ? 'stopped' : 'review'}-${run}-r${revision}`
  }

  function inputSeq(events: readonly { type: string; seq: number; data: unknown }[], requestId: string): number {
    for (const event of events) {
      if (event.type !== 'user/message') continue
      const source = object(object(event.data)?.message)?.source ?? object(event.data)?.source
      if (object(source)?.rpcId === requestId) return event.seq
    }
    return 0
  }

  async function reconcile(sessionId: string, actionId: string, caller: AbortSignal): Promise<number> {
    const controller = new AbortController()
    try {
      const frames = ctx.sessionController.follow({ address: { kind: 'session', sessionId: sessionId as SessionId }, maxMessages: 100 }, AbortSignal.any([caller, controller.signal]))
      for await (const frame of frames) {
        if (frame.type !== 'snapshot') continue
        const scan = (records: typeof frame.records) => inputSeq(records.flatMap(record => record.type === 'event' ? [record.event] : []), actionId)
        let found = scan(frame.records)
        let records = frame.records
        let more = frame.hasMore
        while (!found && more) {
          const seqs = records.map(record => record.event.seq)
          if (!seqs.length) break
          const page = await ctx.sessionController.page({ address: { kind: 'session', sessionId: sessionId as SessionId },
            throughSeq: frame.cursor, beforeSeq: Math.min(...seqs), maxMessages: 100 }, AbortSignal.any([caller, controller.signal]))
          records = page.records
          found = scan(records)
          more = page.hasMore
        }
        return found
      }
      return 0
    } finally { controller.abort() }
  }

  return {
    id: agent => agent.session.id,
    parent: agent => ctx.agents.list().find(candidate => candidate !== agent && ctx.agents.isOwnedBy(agent.session.id, candidate)),
    isLive: agent => ctx.agents.get(agent.session.id) === agent,
    isRunning: agent => agent.status === 'running',
    history: agent => [...agent.session.ownEvents()].map(event => ({
      seq: event.seq, time: event.time, user: event.type === 'user/message', run: eventRun(event, serverName),
    })),
    canReturnResult: agent => !!ctx.tools.get('structured_output', agent),
    goal: agent => goals?.get(agent),
    suspendGoal(agent, input) {
      const goal = goals?.get(agent)
      if (goals && goal?.phase === 'active' && goal.activation === 'armed' && (!input.goalId || input.goalId === goal.id)) {
        goals.block(agent, goal, { code: goalReason(input.runId, goal.revision + 1, input.continuation === 'stopped'),
          message: input.continuation === 'awaiting_executor' ? 'LazyMind is executing this workflow step.' : 'This LazyMind workflow needs user action before automatic work can continue.' })
      }
    },
    resumeGoal(agent, runId) {
      const goal = goals?.get(agent)
      if (goals && goal?.phase === 'blocked' && [goalReason(runId, goal.revision), goalReason(runId, goal.revision, true)].includes(goal.blockedReason?.code ?? '')) goals.resume(agent, goal)
    },
    async resolve(sessionId) {
      const result = await ctx.sessionController.resolveAgent(sessionId as SessionId)
      return 'error' in result ? { error: result.error.message } : { agent: result.agent }
    },
    async prompt(agent, input, signal) {
      await ctx.sessionController.prompt({ sessionId: agent.session.id,
        requestId: input.actionId as Parameters<typeof ctx.sessionController.prompt>[0]['requestId'],
        mode: 'queue', content: [{ type: 'text', text: input.message }],
      }, signal)
      return inputSeq(agent.session.snapshotEvents(), input.actionId)
    },
    cancel: agent => { ctx.sessionController.cancel({ sessionId: agent.session.id }) },
    eventSeq: agent => Math.max(0, agent.session.seq - 1),
    reconcile,
    warn: message => ctx.logger.warn(message),
  }
}
