import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ArtifactSourceButton } from './ArtifactSourceButton';

beforeEach(async () => { await i18n.changeLanguage('zh-CN'); });
afterEach(() => vi.unstubAllGlobals());

it.each([
  ['Markdown', '# 标题\n\n7. 第一项\n8. 第二项\n'],
  ['HTML', '<script>alert("test")</script><h1>标题</h1>'],
  ['JSON', { title: '结构化数据', rows: [{ amount: 0, enabled: false }] }],
  ['empty string', ''],
  ['null', null],
  ['boolean', false],
])('shows %s literally in a read-only source view', async (_, value) => {
  render(<ArtifactSourceButton value={value} />);
  fireEvent.click(screen.getByRole('button', { name: '展示源码' }));
  const source = await screen.findByRole('textbox', { name: '源码' });
  expect(source).toHaveValue(typeof value === 'string' ? value : JSON.stringify(value, null, 2));
  expect(source).toHaveAttribute('readonly');
  expect(document.querySelector('script')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: '返回内容' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  expect(screen.getByRole('button', { name: '展示源码' })).toHaveFocus();
});

it('updates an open source view when the displayed data changes', async () => {
  const { rerender } = render(<ArtifactSourceButton value={{ revision: 1 }} />);
  fireEvent.click(screen.getByRole('button', { name: '展示源码' }));
  rerender(<ArtifactSourceButton value={{ revision: 2 }} />);
  expect(await screen.findByRole('textbox', { name: '源码' })).toHaveValue('{\n  "revision": 2\n}');
});

it('explains binary file records instead of presenting them as file contents', async () => {
  render(<ArtifactSourceButton value={{ path: 'image.png' }} fileRecord />);
  fireEvent.click(screen.getByRole('button', { name: '展示源码' }));
  expect(await screen.findByText('以下展示文件的原始数据记录，不包含二进制内容。')).toBeInTheDocument();
  expect(screen.getByRole('textbox', { name: '源码' })).toHaveValue('{\n  "path": "image.png"\n}');
});

it('loads text files on demand and offers retry after a failure', async () => {
  const fetcher = vi.fn().mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValueOnce({ ok: true, text: async () => 'name,count\nexample,7\n' });
  vi.stubGlobal('fetch', fetcher);
  render(<ArtifactSourceButton value={{ path: 'table.csv' }} sourceUrl='/table.csv' />);
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: '展示源码' }));
  expect(await screen.findByRole('alert')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
  expect(await screen.findByRole('textbox', { name: '源码' })).toHaveValue('name,count\nexample,7\n');
});

it('ignores an old file response after the displayed source changes', async () => {
  let finishOld!: (value: unknown) => void;
  const fetcher = vi.fn().mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; }))
    .mockResolvedValueOnce({ ok: true, text: async () => 'new file' });
  vi.stubGlobal('fetch', fetcher);
  const { rerender } = render(<ArtifactSourceButton value={null} sourceUrl='/old.txt' />);
  fireEvent.click(screen.getByRole('button', { name: '展示源码' }));
  rerender(<ArtifactSourceButton value={null} sourceUrl='/new.txt' />);
  expect(await screen.findByRole('textbox', { name: '源码' })).toHaveValue('new file');
  expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
  finishOld({ ok: true, text: async () => 'old file' });
  await waitFor(() => expect(screen.getByRole('textbox', { name: '源码' })).toHaveValue('new file'));
});
