import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { PptPromptDocument, splitPptPrompt } from './PptPromptDocument';
import { DocumentActionScope } from './DocumentActions';

beforeEach(async () => { await i18n.changeLanguage('zh-CN'); });

it('uses one card title and moves editing into the enclosing header', async () => {
  const onEdit = vi.fn();
  const view = (editable: boolean) => <DocumentActionScope inline label='每页设计稿'>
    <PptPromptDocument text={'## 风格\n浅色背景'} onEdit={editable ? onEdit : undefined} maxHeight={320} />
  </DocumentActionScope>;
  const { container, rerender } = render(view(true));
  expect(screen.getAllByText('每页设计稿')).toHaveLength(1);
  expect(container.querySelector('.ppt-prompt-document__toolbar')).toBeNull();
  expect(container.querySelector('.ppt-prompt-document--embedded')).not.toBeNull();
  const header = container.querySelector('.workflow-panel__document-item-header') as HTMLElement;
  fireEvent.click(await within(header).findByRole('button', { name: '编辑文本' }));
  expect(onEdit).toHaveBeenCalledTimes(1);
  expect(screen.getByText('浅色背景')).toBeVisible();
  expect(container.querySelector('.ppt-prompt-document__body')).toHaveStyle({ '--workflow-ppt-prompt-max-height': '320px' });
  rerender(view(false));
  await waitFor(() => expect(screen.queryByRole('button', { name: '编辑文本' })).not.toBeInTheDocument());
});

it('recognizes legacy labels and Markdown headings without dropping unrecognized prose', () => {
  expect(splitPptPrompt('前置说明\n\n配色：蓝白\n补充说明\n\n**布局：** 左右双栏\n\n## 设计要求\n所有文字保留'))
    .toEqual([
      { title: '', body: '前置说明\n' },
      { title: '配色', body: '蓝白\n补充说明\n' },
      { title: '布局', body: '左右双栏\n' },
      { title: '设计要求', body: '所有文字保留' },
    ]);
});

it('keeps code fences and literal HTML intact without a duplicate original-prompt view', () => {
  const text = '设计要求：保留 HTML <i>/<sub>/<sup>\n```html\n标题：代码里的标签\n<div>示例</div>\n```';
  expect(splitPptPrompt(text)).toHaveLength(1);
  const { container } = render(<PptPromptDocument text={text} />);
  expect(container.querySelector('i, sub, sup')).toBeNull();
  expect(container.querySelector('.ppt-prompt-document__text')?.textContent).toContain('<div>示例</div>');
  expect(screen.queryByRole('button', { name: '原始提示词' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '编辑文本' })).not.toBeInTheDocument();
});


it('shows one marked page document with style before the page prompt', () => {
  const text = '<!-- ppt-page-prompt:v1 -->\n\n## 风格\n浅色背景\n\n## 布局\n左右双栏\n\n## 页面正文\n正文';
  const { container } = render(<PptPromptDocument text={text} />);
  expect([...container.querySelectorAll('summary')].map(item => item.textContent)).toEqual(['风格', '布局', '页面正文']);
  expect(container.textContent).not.toContain('ppt-page-prompt:v1');
  expect(container.textContent).toContain('浅色背景');
});
