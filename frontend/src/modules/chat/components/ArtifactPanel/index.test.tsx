import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ConversationArtifact } from '@/modules/chat/store/taskCenter';
import ArtifactPanel from './index';

const artifacts: ConversationArtifact[] = [];

const artifactApi = vi.hoisted(() => ({
  listRevisions: vi.fn(),
  downloadRevisionUrl: vi.fn(),
  moveHead: vi.fn(),
  diffRevisions: vi.fn(),
}));

vi.mock('@/modules/chat/store/taskCenter', () => ({
  useTaskCenterStore: (selector: (state: {
    artifactsByConversation: Record<string, ConversationArtifact[]>;
    loadConversationArtifacts: () => void;
  }) => unknown) => selector({
    artifactsByConversation: artifacts.length ? { 'conv-1': artifacts } : {},
    loadConversationArtifacts: vi.fn(),
  }),
}));

vi.mock('@/modules/chat/utils/request', () => ({
  ArtifactV2Api: () => artifactApi,
}));

vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd');
  return {
    ...actual,
    Modal: {
      ...actual.Modal,
      confirm: ({ onOk }: { onOk?: () => void | Promise<void> }) => {
        void onOk?.();
      },
    },
    message: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() },
  };
});

vi.mock('@/modules/knowledge/components/FileViewer', () => ({
  default: ({ file, fileName }: { file?: string; fileName: string }) => (
    <div data-testid="file-viewer" data-file={file} data-file-name={fileName} />
  ),
}));

function seed(items: ConversationArtifact[]) {
  artifacts.splice(0, artifacts.length, ...items);
}

describe('ArtifactPanel', () => {
  beforeEach(() => {
    artifactApi.listRevisions.mockReset();
    artifactApi.downloadRevisionUrl.mockReset();
    artifactApi.moveHead.mockReset();
    artifactApi.diffRevisions.mockReset();
    artifactApi.listRevisions.mockResolvedValue({
      data: {
        data: {
          revisions: [
            { artifact_id: 'v2-1', revision_id: 'r1', revision_no: 1, created_at: '2026-01-01T00:00:00Z' },
            {
              artifact_id: 'v2-1',
              revision_id: 'r2',
              revision_no: 2,
              published: true,
              head_version: 7,
              created_at: '2026-01-02T00:00:00Z',
            },
          ],
        },
      },
    });
    artifactApi.moveHead.mockResolvedValue({});
    seed([
      {
        artifact_id: 'upload-1',
        conversation_id: 'conv-1',
        history_id: 'turn-a',
        producer_type: 'user',
        source_type: 'user_upload',
        filename: 'brief.pdf',
        slot: 'brief.pdf',
        content_type: 'file',
        seq: 1,
        value: { url: '/static-files/tmp/brief.pdf' },
        created_at: '2026-01-01T00:00:00Z',
      },
      {
        artifact_id: 'chat-1',
        conversation_id: 'conv-1',
        history_id: 'turn-a',
        producer_type: 'main_agent',
        source_type: 'main_chat',
        filename: 'notes.txt',
        slot: 'notes.txt',
        content_type: 'text',
        seq: 1,
        value: { text: 'hello from chat' },
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);
  });

  it('shows an empty state when the conversation has no visible files', () => {
    seed([]);
    render(<ArtifactPanel sessionId="conv-1" />);
    expect(screen.getByText('当前会话没有可预览的产物。')).toBeInTheDocument();
  });

  it('groups uploads and published files without a batch-download control', () => {
    render(<ArtifactPanel sessionId="conv-1" />);

    expect(screen.getByText('你上传的资料')).toBeInTheDocument();
    expect(screen.getByText('已交付的文件')).toBeInTheDocument();
    expect(screen.getByRole('listitem', { name: /brief.pdf/ })).toBeInTheDocument();
    expect(screen.getByRole('listitem', { name: /notes.txt/ })).toBeInTheDocument();
    expect(screen.queryByText(/artifactPanelDownloadVisible/)).not.toBeInTheDocument();
  });

  it('opens version history for published main-chat files with revision metadata', async () => {
    seed([
      {
        artifact_id: 'chat-1',
        v2_artifact_id: 'v2-1',
        conversation_id: 'conv-1',
        history_id: 'turn-a',
        producer_type: 'main_agent',
        source_type: 'main_chat',
        filename: 'notes.txt',
        slot: 'notes.txt',
        content_type: 'text',
        seq: 1,
        revision: 2,
        revision_count: 2,
        publication_status: 'published',
        value: { text: 'hello from chat' },
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);
    render(<ArtifactPanel sessionId="conv-1" />);
    fireEvent.click(screen.getByRole('listitem', { name: /notes.txt/ }));
    expect(screen.getByText('当前 · v2')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /版本记录/ }));
    expect(await screen.findByText('v1')).toBeInTheDocument();
    expect(screen.getByText('v2 · 已发布')).toBeInTheDocument();
  });

  it('shows read-only revision history for published SubAgent files', async () => {
    seed([
      {
        artifact_id: 'subagent-1',
        v2_artifact_id: 'v2-subagent-1',
        conversation_id: 'conv-1',
        history_id: 'turn-a',
        producer_type: 'subagent',
        source_type: 'subagent',
        filename: 'research.md',
        slot: 'research.md',
        content_type: 'text',
        seq: 1,
        revision: 2,
        revision_count: 2,
        publication_status: 'published',
        value: { text: '# Research' },
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);
    render(<ArtifactPanel sessionId="conv-1" />);

    fireEvent.click(screen.getByRole('listitem', { name: /research.md/ }));
    expect(screen.getByText('当前 · v2')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /版本记录/ }));

    expect(await screen.findByText('v2 · 已发布')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '恢复为当前版本' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '与当前版比较' })).not.toBeInTheDocument();
  });

  it('restores using the published revision CAS version, not a stale row head_version', async () => {
    seed([
      {
        artifact_id: 'chat-1',
        v2_artifact_id: 'v2-1',
        conversation_id: 'conv-1',
        history_id: 'turn-a',
        producer_type: 'main_agent',
        source_type: 'main_chat',
        filename: 'notes.txt',
        slot: 'notes.txt',
        content_type: 'text',
        seq: 1,
        revision: 2,
        revision_count: 2,
        head_version: 0,
        publication_status: 'published',
        value: { text: 'hello from chat' },
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);
    render(<ArtifactPanel sessionId="conv-1" />);
    fireEvent.click(screen.getByRole('listitem', { name: /notes.txt/ }));
    fireEvent.click(screen.getByRole('button', { name: /版本记录/ }));
    fireEvent.click(await screen.findByRole('button', { name: '恢复为当前版本' }));
    expect(artifactApi.moveHead).toHaveBeenCalledWith('v2-1', 'published', {
      revision_id: 'r1',
      version: 7,
    });
  });

  it('keeps text preview and actions in the sidebar without direction controls', () => {
    render(<ArtifactPanel sessionId="conv-1" />);
    fireEvent.click(screen.getByRole('listitem', { name: /notes.txt/ }));

    expect(screen.queryByRole('button', { name: '向右展开预览' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '在侧边栏内向下展开预览' })).not.toBeInTheDocument();
    expect(screen.getByText('hello from chat')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /下载/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '返回文件' }));
    expect(screen.getByRole('listitem', { name: /notes.txt/ })).toBeInTheDocument();
  });

  it('previews uploaded files with the knowledge FileViewer', () => {
    render(<ArtifactPanel sessionId="conv-1" />);
    fireEvent.click(screen.getByRole('listitem', { name: /brief.pdf/ }));
    const viewer = screen.getByTestId('file-viewer');
    expect(viewer).toHaveAttribute('data-file-name', 'brief.pdf');
    expect(viewer.getAttribute('data-file')).toContain('brief.pdf');
  });

  it('loads a text-like uploaded file into the knowledge FileViewer', () => {
    seed([
      {
        artifact_id: 'upload-md',
        conversation_id: 'conv-1',
        history_id: 'turn-a',
        producer_type: 'user',
        source_type: 'user_upload',
        filename: 'plan.md',
        slot: 'plan.md',
        content_type: 'file',
        seq: 1,
        value: { url: '/static-files/tmp/plan.md' },
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);

    render(<ArtifactPanel sessionId="conv-1" />);
    fireEvent.click(screen.getByRole('listitem', { name: /plan.md/ }));

    expect(screen.getByTestId('file-viewer')).toHaveAttribute('data-file-name', 'plan.md');
  });
});
