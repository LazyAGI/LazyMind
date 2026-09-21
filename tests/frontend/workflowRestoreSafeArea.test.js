import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('workflow editor navigation safe area', () => {
  it('keeps the expand button inside the sidebar reserved layout space', () => {
    const layout = readFileSync(
      new URL('../../frontend/src/layouts/index.scss', import.meta.url),
      'utf8',
    );
    const mainLayout = readFileSync(
      new URL('../../frontend/src/layouts/MainLayout.tsx', import.meta.url),
      'utf8',
    );

    const sidebar = mainLayout.match(/<Sider\b[\s\S]*?<\/Sider>/)?.[0];
    expect(sidebar).toContain('width={sidebarWidth}');
    expect(sidebar).toContain('className="sider-inline-toggle"');
    expect(layout).toMatch(/\.sider-bar-style\.ant-layout-sider\s*\{[^}]*position: sticky;/);
  });
});
