// Compatibility entry point; native DSH events are kept in this adapter.
export * from '../../workflow-agent-core/src/protocol'
export { eventRun, workflowOperation } from './events'
