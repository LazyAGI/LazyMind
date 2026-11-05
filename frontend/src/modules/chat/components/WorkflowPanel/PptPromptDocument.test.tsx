import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it } from 'vitest';
import i18n from '@/i18n';
import { PptPromptDocument, splitPptPrompt } from './PptPromptDocument';

beforeEach(async () => { await i18n.changeLanguage('zh-CN'); });

it('recognizes legacy labels and Markdown headings without dropping unrecognized prose', () => {
  expect(splitPptPrompt('前置说明\n\n配色：蓝白\n补充说明\n\n**布局：** 左右双栏\n\n## 设计要求\n所有文字保留'))
    .toEqual([
      { title: '', body: '前置说明\n' },
      { title: '配色', body: '蓝白\n补充说明\n' },
      { title: '布局', body: '左右双栏\n' },
      { title: '设计要求', body: '所有文字保留' },
    ]);
});

it('keeps code fences and literal HTML intact and exposes the exact original prompt', () => {
  const text = '设计要求：保留 HTML <i>/<sub>/<sup>\n```html\n标题：代码里的标签\n<div>示例</div>\n```';
  expect(splitPptPrompt(text)).toHaveLength(1);
  const { container } = render(<PptPromptDocument text={text} />);
  expect(container.querySelector('i, sub, sup')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: '原始提示词' }));
  expect(container.querySelector('pre')?.textContent).toBe(text);
  expect(screen.queryByRole('button', { name: '编辑文本' })).not.toBeInTheDocument();
});
