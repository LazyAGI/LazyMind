import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const readTaskCenterSource = (path) => readFileSync(
  new URL(`../../frontend/src/modules/taskCenter/${path}`, import.meta.url),
  'utf8',
);

describe('task-center task list layout', () => {
  it('keeps desktop columns fixed and exposes truncated text in tooltips', () => {
    const taskList = readTaskCenterSource('TaskList.tsx');
    const styles = readTaskCenterSource('index.scss');

    expect(taskList).toContain("width: '56%'");
    expect(taskList).toContain("width: '24%'");
    expect(taskList).toContain("width: '14%'");
    expect(taskList).toContain("tableLayout='fixed'");
    expect(taskList).toContain('<Tooltip title={source}>');
    expect(taskList).toContain('<Tooltip title={stateCopy}>');
    expect(taskList).toContain('<Tooltip title={createdAt}>');
    expect(taskList).toContain('task.conversation_title || task.title || task.schedule_name');
    expect(taskList).toContain('task.conversation_title || task.title || t(\'taskCenter.noDescription\')');
    expect(styles).toContain('.task-list-state-copy {');
    expect(styles).toContain('text-overflow: ellipsis;');
    expect(styles).toContain('white-space: nowrap;');
  });
});
