import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const root = path.resolve(import.meta.dirname, '../..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

describe('Workflow Panel live update surface', () => {
  it('uses one conversation stream and no dedicated Workflow stream', () => {
    const panel = read('frontend/src/modules/chat/components/WorkflowPanel/index.tsx');
    const hook = read('frontend/src/modules/chat/hooks/useWorkflow.ts');
    const taskStore = read('frontend/src/modules/chat/store/taskCenter.ts');
    expect(panel).not.toContain('pollIntervalMs');
    expect(panel).not.toMatch(/setInterval\s*\(\s*refresh/);
    expect(hook).not.toContain('subscribeWorkflowSession');
    expect(taskStore).toContain('_convStream: SSE | null');
    expect(taskStore).not.toContain('_convStreams:');
    expect(taskStore).not.toContain('subscribeWorkflowEventStream');
  });

  it('coalesces Workflow invalidations before reading authoritative state', () => {
    const taskStore = read('frontend/src/modules/chat/store/taskCenter.ts');
    expect(taskStore).toContain('workflowRefreshTimer');
    expect(taskStore).toContain('scheduleWorkflowSessionRefresh(conversationId');
    expect(taskStore).toContain("type === 'workflow_completed' ? 800 : 100");
    expect(taskStore).not.toContain('window.setTimeout(refreshActiveWorkflowSession');
  });

  it('reconciles from server state when the conversation stream reconnects', () => {
    const taskStore = read('frontend/src/modules/chat/store/taskCenter.ts');
    const chatLayout = read('frontend/src/modules/chat/pages/chatLayout/index.tsx');
    expect(taskStore).toContain(
      'void get().refreshConversationExecution(conversationId)',
    );
    expect(chatLayout).toContain('subscribeConvEvents(sessionId)');
    expect(chatLayout).toContain('unsubscribeConvEvents(sessionId)');
  });
});
