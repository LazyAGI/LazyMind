import { describe, expect, it } from 'vitest';

import { isTaskCenterVisibleTask } from './taskCenter';

describe('isTaskCenterVisibleTask', () => {
  it('hides workflow execution tasks from the standalone task panel', () => {
    expect(isTaskCenterVisibleTask({ agent_type: 'workflow_step' })).toBe(false);
  });

  it('keeps ordinary subagent tasks visible', () => {
    expect(isTaskCenterVisibleTask({ agent_type: 'subagent' })).toBe(true);
  });
});
