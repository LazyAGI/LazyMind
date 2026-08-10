import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const root = path.resolve(import.meta.dirname, '../..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

describe('Workflow Panel live update surface', () => {
  it('uses the shared projection store and has no normal-refresh polling', () => {
    const panel = read('frontend/src/modules/chat/components/WorkflowPanel/index.tsx');
    const hook = read('frontend/src/modules/chat/hooks/useWorkflow.ts');
    expect(panel).not.toContain('pollIntervalMs');
    expect(panel).not.toMatch(/setInterval\s*\(\s*refresh/);
    expect(hook).toContain('subscribeWorkflowSession');
  });

  it('does not turn task events into per-event Workflow state refetches', () => {
    const taskStore = read('frontend/src/modules/chat/store/taskCenter.ts');
    expect(taskStore).toContain('if (!workflowState.sessionByConversation[conversationId])');
    const intentUpdatedBranch = taskStore.match(
      /type === 'intent_updated'[\s\S]*?\} else if \(type === 'workflow_artifact_updated'/,
    )?.[0] || '';
    expect(intentUpdatedBranch).not.toContain('loadActiveSession(conversationId)');
  });

  it('reconnects with Last-Event-ID through one Workflow Event Stream', () => {
    const stream = read('frontend/src/modules/chat/utils/workflowEventStream.ts');
    expect(stream).toContain("headers['Last-Event-ID']");
    expect(stream).toContain('/events`');
    expect(stream).toContain("'EVENT_CURSOR_EXPIRED'");
  });
});
