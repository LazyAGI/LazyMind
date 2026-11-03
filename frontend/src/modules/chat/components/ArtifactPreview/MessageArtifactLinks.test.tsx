import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import MessageArtifactLinks from './MessageArtifactLinks';
import { ArtifactPreviewContext } from './context';
import { useTaskCenterStore, type ConversationArtifact } from '../../store/taskCenter';
import { useWorkflowStore, type WorkflowSession } from '../../store/workflowPanel';

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
