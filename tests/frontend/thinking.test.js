import { describe, expect, it } from 'vitest';

import {
  formatThinkingForDisplay,
  splitThinkingContent,
} from '../../frontend/src/modules/chat/utils/thinking.ts';


describe('tool payload display', () => {
  const preview = '插件启动检查已完成，结果是 not_applicable，原因是任务不适用。';
  const raw = [
    `<trp id="call-writer">${preview}</trp>`,
    '<tool_result>{"decision":"not_applicable","reason":"raw reason"}</tool_result>',
  ].join('');

  it('keeps the human-readable preview and removes raw tool JSON before splitting', () => {
    const split = splitThinkingContent(raw);

    expect(split.reasoning_content).toContain(preview);
    expect(split.reasoning_content).not.toContain('"decision"');
    expect(split.content).toBe('');
  });

  it('removes structured tool payloads from formatted thinking text', () => {
    const display = formatThinkingForDisplay(raw);

    expect(display).toBe(preview);
    expect(display).not.toContain('tool_result');
  });
});
