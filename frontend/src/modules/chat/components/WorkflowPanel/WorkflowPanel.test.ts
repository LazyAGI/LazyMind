import { describe, expect, it } from 'vitest';

import type { TabDef } from '@/modules/chat/store/workflowPanel';
import { resolveCompletedContinueStep } from './workflowContinue';

const outlineTab: TabDef = {
  id: 'outline',
  step_id: 'outline',
  label: 'Outline',
  slots: [],
  completed_continue_step: 'write_document',
};

describe('resolveCompletedContinueStep', () => {
  it('uses the workflow-declared completed continuation', () => {
    expect(resolveCompletedContinueStep({
      status: 'completed',
    }, outlineTab)).toBe('write_document');
  });

  it('does not invent a continuation for undeclared or active tabs', () => {
    expect(resolveCompletedContinueStep({
      status: 'completed',
    }, { ...outlineTab, completed_continue_step: undefined })).toBeUndefined();
    expect(resolveCompletedContinueStep({ status: 'active' }, outlineTab)).toBeUndefined();
  });
});
