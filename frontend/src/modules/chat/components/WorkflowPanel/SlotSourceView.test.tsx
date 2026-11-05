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

it('displays PPT prompt text literally even with a legacy Markdown descriptor', async () => {
  const prompt = 'HTML <i>/<sub>/<sup>，参数 {"color":"#FFFFFF"}';
  const slot = { slot_id: 'slide_outline', slot: 'slide_outline', selected: false,
    revision: 1, created_at: '2026-10-29', content_type: 'text', artifact_value: { text: prompt },
    artifact_id: 'legacy-prompt', document: { representation: 'markdown' },
  } as SlotRevision;
  const { container } = render(<SlotRenderer slot={slot} readOnly widget={{ widgetType: 'ppt-prompt' }} />);
  expect(await screen.findByText(prompt)).toBeVisible();
  expect(container.querySelector('.writer-markdown-editor')).toBeNull();
  expect(container.querySelector('i, sub, sup')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: '展示源码' }));
  expect(await screen.findByRole('textbox', { name: '源码' })).toHaveValue(prompt);
});

it.each(['academic_brief', 'deck_outline'])('renders %s as Markdown and preserves comments in editable source', async (slotName) => {
  const text = '<!-- 汇报共 8 页 -->\n\n## 方法与证据\n\n**复杂度**：$O(n)$\n\n| 指标 | 数值 |\n| --- | --- |\n| 准确率 | 95% |';
  const slot = { slot_id: slotName, slot: slotName, selected: true,
    revision: 1, created_at: '', content_type: 'text', artifact_value: { text },
    artifact_id: 'legacy-markdown', document: { representation: 'markdown', editable: true, capabilities: ['save'] },
  } as SlotRevision;
  const { container } = render(<SlotRenderer slot={slot} sessionId='ppt-session' widget={{ widgetType: 'text-markdown' }} />);
  expect(await screen.findByRole('heading', { name: '方法与证据' })).toBeVisible();
  expect(screen.getByRole('table')).toHaveTextContent('95%');
  expect(screen.queryByText(/文档格式暂时无法解析/)).not.toBeInTheDocument();
  expect(container.querySelector('.writer-markdown-editor')).toBeNull();
  fireEvent.click(container.querySelector('.workflow-slot__text--markdown')!);
  expect(await screen.findByRole('textbox', { name: '编辑文本' })).toHaveValue(text);
});

it('loads historical offloaded research analysis as Markdown without requiring a widget', async () => {
  const text = '## 科研内容分析\n\n- **论文 A**：方法、公式与实验证据';
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, text: async () => text })));
  const slot = { slot_id: 'academic_brief', slot: 'academic_brief', selected: true,
    revision: 1, created_at: '', content_type: 'text',
    artifact_value: { type: 'text', path: '/artifacts/research.txt', url: '/research.txt' },
  } as SlotRevision;
  render(<SlotRenderer slot={slot} sessionId='ppt-session' readOnly />);
  expect(await screen.findByRole('heading', { name: '科研内容分析' })).toBeVisible();
  fireEvent.doubleClick(screen.getByRole('heading', { name: '科研内容分析' }));
  expect(screen.queryByRole('textbox', { name: '编辑文本' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '展示源码' }));
  expect(await screen.findByRole('textbox', { name: '源码' })).toHaveValue(text);
});
