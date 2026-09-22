import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SlotRevision, SlotWidgetConfig } from '@/modules/chat/store/workflowPanel';
import { SlotDownloadContext, SlotRenderer } from './SlotComponents';

vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));

beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  vi.stubGlobal('Image', class {
    onload?: () => void;
    set src(_: string) { queueMicrotask(() => this.onload?.()); }
  });
});
afterEach(() => vi.unstubAllGlobals());

const markdown = '# 标题\n\n7. 原始编号\n8. 下一项\n';
const json = { columns: ['名称', '数量'], rows: [['项目', 0]], enabled: false };
const html = '<!doctype html><html><body><h1>页面</h1></body></html>';
const slide = { layout: 'title', title: '幻灯片', notes: '保留原始字段', custom: { raw: true } };
const cases: { label: string; type: string; value: unknown; expected: unknown;
  expectedType?: 'text' | 'file'; widget?: SlotWidgetConfig; fileText?: string }[] = [
  { label: 'plain text', type: 'text', value: { text: '原始文本' }, expected: '原始文本' },
  { label: 'read-only Markdown', type: 'text', value: { text: markdown }, expected: markdown, widget: { widgetType: 'text-markdown' } },
  { label: 'JSON block', type: 'json', value: { data: json }, expected: json, widget: { widgetType: 'json-block' } },
  { label: 'structured table', type: 'json', value: { data: json }, expected: json, expectedType: 'text' },
  { label: 'JSON file', type: 'file', value: { url: '/fixture.json', filename: 'fixture.json' }, expected: json, expectedType: 'text', fileText: JSON.stringify(json) },
  { label: 'Markdown file', type: 'file', value: { url: '/fixture.md', filename: 'fixture.md' }, expected: markdown, expectedType: 'file', fileText: markdown },
  { label: 'HTML preview', type: 'file', value: { text: html }, expected: html, widget: { widgetType: 'html-preview' } },
  { label: 'CSV file', type: 'file', value: { url: '/fixture.csv', filename: 'fixture.csv' }, expected: 'name,count\nexample,7\n', fileText: 'name,count\nexample,7\n' },
  { label: 'binary file', type: 'file', value: { url: '/fixture.pdf', filename: 'fixture.pdf' }, expected: { url: '/fixture.pdf', filename: 'fixture.pdf' } },
  { label: 'image', type: 'image', value: { url: 'https://example.test/image.png', filename: 'image.png' }, expected: { url: 'https://example.test/image.png', filename: 'image.png' } },
  { label: 'video', type: 'file', value: { url: 'https://example.test/video.mp4', filename: 'video.mp4' }, expected: { url: 'https://example.test/video.mp4', filename: 'video.mp4' } },
  { label: 'JSON slide', type: 'json', value: { data: slide }, expected: slide, widget: { widgetType: 'html-slide' } },
  { label: 'HTML slide', type: 'text', value: { text: html }, expected: html, widget: { widgetType: 'html-slide' } },
];

it.each(cases)('exposes source for $label, including read-only data', async ({ type, value, expected, expectedType, widget, fileText }) => {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, text: async () => fileText ?? '', json: async () => JSON.parse(fileText ?? '{}') })));
  const slot: SlotRevision = { slot_id: 'source-fixture', slot: 'source-fixture', selected: true,
    revision: 1, created_at: '2026-09-16', content_type: type, artifact_value: value };
  render(<SlotRenderer slot={slot} readOnly expectedType={expectedType} widget={widget} />);
  fireEvent.click(await screen.findByRole('button', { name: '展示源码' }));
  expect(await screen.findByRole('textbox', { name: '源码' })).toHaveValue(
    typeof expected === 'string' ? expected : JSON.stringify(expected, null, 2));
  expect(screen.getByRole('textbox', { name: '源码' })).toHaveAttribute('readonly');
});

it('keeps the source accessible without revealing a disabled Markdown download action', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, text: async () => markdown })));
  const slot: SlotRevision = { slot_id: 'preview', slot: 'preview', selected: true,
    revision: 1, created_at: '2026-09-16', content_type: 'file',
    artifact_value: { filename: 'preview.md', url: '/preview.md' } };
  render(<SlotDownloadContext.Provider value={false}><SlotRenderer slot={slot} expectedType='file' /></SlotDownloadContext.Provider>);
  expect(await screen.findByRole('button', { name: '展示源码' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /下载/ })).not.toBeInTheDocument();
});
