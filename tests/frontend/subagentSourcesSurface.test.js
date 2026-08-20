import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

const root = resolve(import.meta.dirname, '../..');
const read = (path) => readFileSync(resolve(root, path), 'utf8');

describe('subagent source surface', () => {
  it('keeps sources on the current task and renders only searched entries', () => {
    const store = read('frontend/src/modules/chat/store/taskCenter.ts');
    const panel = read('frontend/src/modules/chat/components/TaskCenter/index.tsx');

    expect(store).toMatch(/sources:\s*ChatSource\[\]/);
    expect(store).toMatch(/case "sources":[\s\S]*?task\.sources =/);
    expect(store).toMatch(/sources:\s*t\.sources \?\? \[\]/);
    expect(panel).toMatch(/getSearchSources\(sources\)/);
    expect(panel).toMatch(/<ReferenceSources sources=\{task\.sources\} \/>/);
  });
});
