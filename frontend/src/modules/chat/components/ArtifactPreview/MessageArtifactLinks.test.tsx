import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import MessageArtifactLinks from './MessageArtifactLinks';
import { ArtifactPreviewContext } from './context';
import { useTaskCenterStore, type ConversationArtifact } from '../../store/taskCenter';
import { useWorkflowStore, type WorkflowSession } from '../../store/workflowPanel';
import { buildChatMessageListFromHistory } from '../../utils/message';
import type { ChatExport } from '@/api/generated/core-client';

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
    <MessageArtifactLinks conversationId='c1' historyId='h1' content={answer.delta} exports={answer.exports} />
  </ArtifactPreviewContext.Provider>);
  fireEvent.click(screen.getByRole('button', { name: '预览 trip.md' }));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ content: document }));
  expect(answer.raw_delta).toBe(raw);
});
