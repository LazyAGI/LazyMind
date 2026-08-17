import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('new workflow modal layout', () => {
  it('reserves enough label width without forcing the input column to overflow', () => {
    const styles = readFileSync(
      new URL(
        '../../frontend/src/modules/workflow/components/NewWorkflowModal/index.scss',
        import.meta.url,
      ),
      'utf8',
    );

    expect(styles).toContain('grid-template-columns: 112px minmax(0, 1fr);');
  });
});
