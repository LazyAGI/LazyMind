import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeAll, beforeEach, expect, it, vi } from 'vitest';
import MessageArtifactLinks from './MessageArtifactLinks';
import { ArtifactPreviewContext } from './context';
import { useTaskCenterStore, type ConversationArtifact } from '../../store/taskCenter';
import { useWorkflowStore, type WorkflowSession } from '../../store/workflowPanel';
import { buildChatMessageListFromHistory } from '../../utils/message';
import type { ChatExport } from '@/api/generated/core-client';

vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: () => null }));

// Finish module loading before timing DOM updates; keep the real preview renderer.
beforeAll(async () => { await import('./index'); }, 10_000);

const open = vi.fn();
const artifact = (id: string, extra: Partial<ConversationArtifact> = {}): ConversationArtifact => ({
  artifact_id: id, revision: 1, conversation_id: 'c1', history_id: 'h1', name: id,
  slot: id, filename: `${id}.md`, content_type: 'text', seq: 1, value: { text: `content ${id}` },
  source_type: 'main_chat', producer_type: 'main_agent', created_at: '', ...extra,
});
beforeEach(() => {
  open.mockClear();
  useTaskCenterStore.setState({ deliveriesByConversation: { c1: [] }, artifactsByConversation: {} });
  useWorkflowStore.setState({ sessionByConversation: {} });
});
function show(exports?: any[]) {
  return render(<ArtifactPreviewContext.Provider value={{ open, close: vi.fn(), target: null, onVersions: undefined }}>
    <MessageArtifactLinks conversationId="c1" historyId="h1" content="# Report" exports={exports} />
  </ArtifactPreviewContext.Provider>);
}
it('shows this reply outputs and opens their delivered revision, excluding uploads, drafts, workflow and other turns', () => {
  useTaskCenterStore.setState({ deliveriesByConversation: { c1: [
    artifact('report', { revision: 2 }), artifact('future', { history_id: 'h2' }), artifact('upload', { source_type: 'user_upload' }),
    artifact('draft', { publication_status: 'draft' }), artifact('workflow', { source_type: 'workflow' }),
  ] }, artifactsByConversation: { c1: [artifact('report', { revision: 9 })] } });
  show();
  expect(screen.getAllByRole('button')).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: '预览 report.md' }));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ key: 'c1:report:2', content: 'content report' }));
});
it('previews a declared inline export without requiring a save or duplicating a saved export', () => {
  const { unmount } = show([{ export_id: 'report', filename: 'report.md', start: 0, end: 8 }]);
  fireEvent.click(screen.getByRole('button', { name: '预览 report.md' }));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ content: '# Report' }));
  unmount();
  useTaskCenterStore.setState({ deliveriesByConversation: { c1: [artifact('report')] } });
  show([{ export_id: 'report', filename: 'report.md', start: 0, end: 8 }]);
  expect(screen.getAllByRole('button')).toHaveLength(1);
});
it('does not append a second artifact area to workflow replies', () => {
  useWorkflowStore.setState({ sessionByConversation: { c1: { session_id: 's1', ordinary_tasks: [{ trigger_history_id: 'h1' }] } as WorkflowSession } });
  useTaskCenterStore.setState({ deliveriesByConversation: { c1: [artifact('report')] } });
  show([{ export_id: 'report', filename: 'report.md', start: 0, end: 8 }]);
  expect(screen.queryByRole('list')).not.toBeInTheDocument();
});

it('prefers HTML for explicitly grouped formats and keeps SVG links', async () => {
  const html = '<html><body><svg><title>Team diagram</title></svg></body></html>';
  useTaskCenterStore.setState({ deliveriesByConversation: { c1: [
    artifact('svg', { filename: 'diagram.svg', value: { text: '<svg />', preview_group: 'team-architecture' } }),
    artifact('html', { filename: 'diagram.html', value: { text: html, preview_group: 'team-architecture' } }),
    artifact('draft', { filename: 'draft.html', publication_status: 'draft' }),
    artifact('future', { filename: 'future.html', history_id: 'h2' }),
    artifact('upload', { filename: 'upload.html', source_type: 'user_upload' }),
  ] } });
  const view = show();
  await waitFor(() => expect(view.container.querySelector('iframe')).toHaveAttribute('srcdoc', html));
  expect(view.container.querySelectorAll('.message-artifact-preview')).toHaveLength(1);
  expect(screen.getByRole('button', { name: '预览 diagram.svg' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '预览 diagram.html' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '关闭预览' })).not.toBeInTheDocument();
  expect(open).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: '预览 diagram.svg' }));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ filename: 'diagram.svg' }));
});

it('preserves the loaded preview and selected view across unrelated deliveries and snapshot refreshes', async () => {
  const html = artifact('html', { filename: 'diagram.html', content_type: 'file',
    value: { url: 'https://example.test/diagram.html' } });
  const fetchFile = vi.fn().mockResolvedValue({ ok: true, status: 200,
    arrayBuffer: async () => new TextEncoder().encode('<main>Original diagram</main>').buffer });
  vi.stubGlobal('fetch', fetchFile);
  try {
    useTaskCenterStore.setState({ deliveriesByConversation: { c1: [html] } });
    const view = show();
    await waitFor(() => expect(view.container.querySelector('iframe')).toHaveAttribute('srcdoc', expect.stringContaining('Original diagram')));
    fireEvent.click(screen.getByRole('button', { name: '源码' }));
    act(() => useTaskCenterStore.setState({ deliveriesByConversation: { c1: [
      { ...html, value: { ...html.value } }, artifact('other', { history_id: 'h2' }),
    ] } }));
    await act(async () => {});
    expect(fetchFile).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: '源码' })).toHaveAttribute('aria-pressed', 'true');
  } finally { vi.unstubAllGlobals(); }
});

it('renders independent same-named HTML files and keeps their download identities', async () => {
  useTaskCenterStore.setState({ deliveriesByConversation: { c1: [
    artifact('diagram-a', { filename: 'diagram.html', value: { text: '<main>Diagram A</main>', path: '/a/diagram.html' } }),
    artifact('diagram-b', { filename: 'diagram.html', value: { text: '<main>Diagram B</main>', path: '/b/diagram.html' } }),
  ] } });
  const view = show();
  await waitFor(() => expect(view.container.querySelectorAll('iframe')).toHaveLength(2));
  const frames = view.container.querySelectorAll('iframe');
  expect(frames[0]).toHaveAttribute('srcdoc', expect.stringContaining('Diagram A'));
  expect(frames[1]).toHaveAttribute('srcdoc', expect.stringContaining('Diagram B'));
  const links = screen.getAllByRole('button', { name: '预览 diagram.html' });
  links.forEach(link => fireEvent.click(link));
  expect(open.mock.calls.map(([target]) => target.key)).toEqual(['c1:diagram-a:1', 'c1:diagram-b:1']);
});

it('shows diagrams arriving after the reply and updates the delivered content', async () => {
  const view = show();
  const publish = (text: string, revision: number) => act(() => {
    useTaskCenterStore.setState({ deliveriesByConversation: { c1: [
      artifact('html', { filename: 'diagram.html', revision, value: { text } }),
    ] } });
  });
  publish('<main>First diagram</main>', 1);
  await waitFor(() => expect(view.container.querySelector('iframe')).toHaveAttribute('srcdoc', expect.stringContaining('First diagram')));
  publish('<main>Updated diagram</main>', 2);
  await waitFor(() => expect(view.container.querySelector('iframe')).toHaveAttribute('srcdoc', expect.stringContaining('Updated diagram')));
  expect(view.container.querySelectorAll('.message-artifact-preview')).toHaveLength(1);
  expect(open).not.toHaveBeenCalled();
});

it('previews the complete restored final segment using the original UTF-16 export ranges', () => {
  const prefix = '前言😀\n';
  const document = '# 两天旅行\n## 一、预算\n1500元\n## 二、行程\n天津\n## 三、取舍\n保留美食\n## 四、最终建议\n选择天津。\n> 票价以实时数据为准。';
  const body = prefix + document;
  const raw = '<tp>计算预算</tp><trp>1500</trp>' + body;
  const exports: ChatExport[] = [{ export_id: 'trip', filename: 'trip.md', title: '旅行方案', index: 0,
    content_type: 'text/markdown', start: prefix.length, end: body.length }];
  const list = buildChatMessageListFromHistory([{
    id: 'h1', query: '比较城市', result: '之前的部分回复' + raw,
    model_output: { version: 1 } as any, assistant_remainder: raw, exports,
    steering_messages: [{ id: 'guide', text: '只看天津', assistant_segment: '之前的部分回复' }],
  }]);
  const answer = list[list.length - 1];
  expect(answer.delta).toBe(body);
  render(<ArtifactPreviewContext.Provider value={{ open, close: vi.fn(), target: null, onVersions: undefined }}>
    <MessageArtifactLinks conversationId='c1' historyId='h1' content={answer.delta}
      rawContent={answer.raw_delta} exports={answer.exports} />
  </ArtifactPreviewContext.Provider>);
  fireEvent.click(screen.getByRole('button', { name: '预览 trip.md' }));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ content: document }));
  expect(answer.raw_delta).toBe(raw);
});

it.each([false, true])('previews the entire export after commentary and tool rounds (steering=%s)', steering => {
  const commentary = '我会按1人总预算3000元以内规划，把往返交通、两晚住宿和餐饮都算进去。';
  const document = '# 旅行计划😀\n' + '完整内容\n'.repeat(200) + '**第三天漫步五大道后回武汉。保留完整体验，而不是尽量多打卡。**';
  const raw = commentary + '<tp>核算预算</tp><trp>已核算</trp><tool_result>{}</tool_result>' + document;
  const exports: ChatExport[] = [{ export_id: 'trip', filename: 'trip.md', title: '旅行计划', index: 0,
    content_type: 'text/markdown', start: 0, end: document.length }];
  const list = buildChatMessageListFromHistory([{
    id: 'h1', query: '制定旅行计划', result: (steering ? '此前的回复' : '') + raw,
    model_output: { version: 1 } as any, exports,
    ...(steering ? { assistant_remainder: raw,
      steering_messages: [{ id: 'budget', text: '预算3000以内', assistant_segment: '此前的回复' }] } : {}),
  }]);
  const answer = list[list.length - 1];
  expect(answer.delta).toBe(commentary + document);
  render(<ArtifactPreviewContext.Provider value={{ open, close: vi.fn(), target: null, onVersions: undefined }}>
    <MessageArtifactLinks conversationId='c1' historyId='h1' content={answer.delta}
      rawContent={answer.raw_delta} exports={answer.exports} />
  </ArtifactPreviewContext.Provider>);
  fireEvent.click(screen.getByRole('button', { name: '预览 trip.md' }));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ content: document }));
});

it.each([undefined, 'different-work'])('keeps same-named formats separate without a matching group (%s)', async group => {
  useTaskCenterStore.setState({ deliveriesByConversation: { c1: [
    artifact('html', { filename: 'diagram.html', value: { text: '<main>HTML</main>', preview_group: 'work-a' } }),
    artifact('svg', { filename: 'diagram.svg', value: { text: '<svg xmlns="http://www.w3.org/2000/svg" />', preview_group: group } }),
  ] } });
  const view = show();
  await waitFor(() => expect(view.container.querySelectorAll('iframe')).toHaveLength(2));
});

it('replaces a grouped SVG preview when its HTML alternative arrives, regardless of filenames', async () => {
  const svg = artifact('svg', { filename: 'source.svg', value: { text: '<svg xmlns="http://www.w3.org/2000/svg" />', preview_group: 'work-a' } });
  useTaskCenterStore.setState({ deliveriesByConversation: { c1: [svg] } });
  const view = show();
  await waitFor(() => expect(view.container.querySelector('iframe')).toHaveAttribute('srcdoc', expect.stringContaining('data:image/svg+xml')));
  act(() => useTaskCenterStore.setState({ deliveriesByConversation: { c1: [svg,
    artifact('html', { filename: 'interactive.html', value: { text: '<main>Interactive diagram</main>', preview_group: 'work-a' } }),
  ] } }));
  await waitFor(() => expect(view.container.querySelector('iframe')).toHaveAttribute('srcdoc', expect.stringContaining('Interactive diagram')));
  expect(view.container.querySelectorAll('.message-artifact-preview')).toHaveLength(1);
  expect(screen.getByRole('button', { name: '预览 source.svg' })).toBeInTheDocument();
});
