import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('workflow editor restore-button safe area', () => {
  it('moves the workflow top bar clear of the sidebar restore button', () => {
    const layout = readFileSync(
      new URL('../../frontend/src/layouts/index.scss', import.meta.url),
      'utf8',
    );
    const mainLayout = readFileSync(
      new URL('../../frontend/src/layouts/MainLayout.tsx', import.meta.url),
      'utf8',
    );

    expect(mainLayout).toContain('pathname.startsWith("/memory-management")');
    expect(layout).toContain('.state-graph-editor > .sge-topbar');
    expect(layout).toContain('padding-left: 56px;');
  });
});
