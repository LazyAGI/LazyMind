import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { SlotRevision, WorkflowSession, WorkflowTabAction } from '@/modules/chat/store/workflowPanel';
import { PresentationExportAction } from './ppt/PresentationExportAction';

const mocks = vi.hoisted(() => ({
  post: vi.fn(), fetch: vi.fn(), sign: vi.fn(), raster: vi.fn(), pdf: vi.fn(),
}));
vi.mock('@/components/request', () => ({ axiosInstance: { post: mocks.post }, BASE_URL: '' }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, args?: { page?: number }) => `${key}${args?.page ?? ''}` }) }));
vi.mock('./ppt/slideAssets', () => ({ embedSlideAssetsForExport: async (html: string) => html }));
vi.mock('./ppt/exportHtmlToPptx', async (original) => ({
  ...await original<typeof import('./ppt/exportHtmlToPptx')>(),
  exportHtmlSlidesAsRasterPptx: mocks.raster, exportHtmlSlidesAsRasterPdf: mocks.pdf,
}));
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({
  resolveCoreAssetUrl: (url: string) => url, resolveMarkdownImageUrlAsync: mocks.sign,
  isExpiredSignedUrl: (url: string) => url.includes('expired'),
}));

const html = (text: string) => `<!doctype html><html><body><div id="root">${text}</div></body></html>`;
const file = (text: string) => ({ storage: 'inline_base64', name: 'page.html', content_base64: btoa(String.fromCharCode(...new TextEncoder().encode(text))) });
const revision = (slot: string, order: number, value: unknown, overrides = {}): SlotRevision => ({
  slot_id: slot, slot, sort_order: order, revision: 2, selected: true,
  step_id: 'generate', created_at: '', content_type: 'text', artifact_value: value, ...overrides,
});
function mount(slots: SlotRevision[], format = 'editable-pptx', rows = [1, 2]) {
  render(<PresentationExportAction
    action={{ id: 'export', type: 'export', provider: 'html-presentation', inputs: { pages: 'pages', notes: 'notes' }, formats: [format] } as WorkflowTabAction}
    tab={{ id: 'preview', label: 'Preview', step_id: 'generate', slots: [{ id: 'pages', label: 'Pages', type: 'text' }] }}
    session={{ session_id: 'run', slots } as WorkflowSession} rows={rows}
  />);
}
async function exportAs(format = 'editable-pptx') {
  fireEvent.click(screen.getByText('chat.workflowExportPptx'));
  const suffix = format === 'pdf' ? 'Pdf' : format === 'raster-pptx' ? 'Raster' : 'Editable';
  fireEvent.click(await screen.findByText(`chat.workflowExportMode${suffix}`));
}
beforeEach(() => {
  mocks.post.mockResolvedValue({ data: new Blob(['pptx']) });
  mocks.sign.mockImplementation(async path => '/signed/' + path);
  vi.stubGlobal('fetch', mocks.fetch);
  vi.stubGlobal('URL', Object.assign(class extends URL {}, { createObjectURL: vi.fn(() => 'blob:test'), revokeObjectURL: vi.fn() }));
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.resetAllMocks(); });

it.each(['editable-pptx', 'raster-pptx', 'pdf'])('exports revised UTF-8 HTML and notes in visible order as %s', async format => {
  mount([
    revision('pages', 1, { text: html('旧版') }, { selected: false, revision: 1 }),
    revision('pages', 1, file(html('新版配色 🟦'))),
    revision('pages', 2, { text: html('第二页') }),
    revision('notes', 1, file('新版中文讲稿')),
    revision('notes', 2, { text: '第二页讲稿' }),
  ], format, [2, 1]);
  await exportAs(format);
  const expected = [{ html: html('第二页'), notes: '第二页讲稿' }, { html: html('新版配色 🟦'), notes: '新版中文讲稿' }];
  if (format === 'editable-pptx') {
    await waitFor(() => expect(mocks.post).toHaveBeenCalled());
    expect(mocks.post.mock.calls[0][1].pages).toEqual(expected);
  } else {
    const exporter = format === 'pdf' ? mocks.pdf : mocks.raster;
    await waitFor(() => expect(exporter).toHaveBeenCalled());
    expect(exporter.mock.calls[0][0]).toEqual(expected.map((page, index) => ({ ...page, pageNo: index + 1 })));
  }
  expect(mocks.fetch).not.toHaveBeenCalled();
});

it('loads managed HTML and notes through renewed signed links', async () => {
  mocks.fetch.mockImplementation(async (url: string) => ({ ok: true, text: async () => url.endsWith('notes.txt') ? '文件讲稿' : '```html\n' + html('文件新版') + '\n```' }));
  mount([
    revision('pages', 1, { storage: 'managed_file', type: 'file', path: 'page.html', url: '/expired/page.html' }),
    revision('notes', 1, { storage: 'managed_file', path: 'notes.txt' }),
  ], 'editable-pptx', [1]);
  await exportAs();
  await waitFor(() => expect(mocks.post).toHaveBeenCalled());
  expect(mocks.post.mock.calls[0][1].pages).toEqual([{ html: html('文件新版'), notes: '文件讲稿' }]);
  expect(mocks.fetch.mock.calls.every(([url]) => url.startsWith('/signed/'))).toBe(true);
});

it.each([
  undefined, { text: 'missing HTML' }, file('not a slide'),
  { storage: 'inline_base64', content_base64: 'invalid!' },
  { text: '<html><head><script type="module" src="/assets/index.js"></script></head><body><div id="root"></div></body></html>' },
])('rejects an unreadable page without silently exporting a partial deck: %j', async value => {
  mount([revision('pages', 1, html('第一页')), revision('pages', 2, value)]);
  await exportAs();
  expect(await screen.findByText(/chat.workflowExportPageFailed2/)).toBeVisible();
  expect(mocks.post).not.toHaveBeenCalled();
  expect(mocks.raster).not.toHaveBeenCalled();
  expect(mocks.pdf).not.toHaveBeenCalled();
});

it('reports an inaccessible revised file and allows a successful retry', async () => {
  mocks.fetch.mockResolvedValue({ ok: false, status: 403 });
  mount([revision('pages', 1, { storage: 'managed_file', path: 'page.html' })], 'editable-pptx', [1]);
  await exportAs();
  expect(await screen.findByText(/chat.workflowExportPageFailed1/)).toBeVisible();
  expect(mocks.post).not.toHaveBeenCalled();
  mocks.fetch.mockResolvedValue({ ok: true, text: async () => html('恢复后的页面') });
  await exportAs();
  await waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(1));
  expect(mocks.post.mock.calls[0][1].pages[0].html).toBe(html('恢复后的页面'));
});
