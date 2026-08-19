import { describe, expect, it } from 'vitest';

import type { TabDef } from '@/modules/chat/store/workflowPanel';
import { resolveCompletedWriterContinueStep } from './workflowContinue';

const outlineTab: TabDef = {
  id: 'outline',
  step_id: 'outline',
  label: 'Outline',
  slots: [],
};

describe('resolveCompletedWriterContinueStep', () => {
  it('continues a completed Writer outline into final document generation', () => {
    expect(resolveCompletedWriterContinueStep({
      status: 'completed',
      workflow_id: 'writer-workflow',
    }, outlineTab)).toBe('write_document');
  });

  it('does not add the completed continuation to other tabs or workflows', () => {
    expect(resolveCompletedWriterContinueStep({
      status: 'completed',
      workflow_id: 'writer-workflow',
    }, { ...outlineTab, id: 'result', step_id: 'write_document' })).toBeUndefined();
    expect(resolveCompletedWriterContinueStep({
      status: 'completed',
      workflow_id: 'another-workflow',
    }, outlineTab)).toBeUndefined();
  });
});
