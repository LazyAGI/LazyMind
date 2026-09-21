import { expect, it } from 'vitest'
import { workflowOperation } from '../src/protocol'
it('recognizes published DSH MCP names and rejects unrelated tools', () => {
  expect(workflowOperation('mcp__lazymind__workflow_start_301c35d7c4b6', 'lazymind')).toBe('start')
  expect(workflowOperation('mcp__lazymind__workflow_session_list_0ec5ad8e526f', 'lazymind')).toBe('session_list')
  expect(workflowOperation('mcp__lazymind__workflow_start_other', 'lazymind')).toBeNull()
  expect(workflowOperation('mcp__other__workflow_start_301c35d7c4b6', 'lazymind')).toBeNull()
})
