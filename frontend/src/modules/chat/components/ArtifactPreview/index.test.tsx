import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { ConfigProvider, message } from 'antd';
import ArtifactPreview from './index';
import { ArtifactPreviewContext, useArtifactPreviewController } from './context';
import ArtifactCollectorCard from '../ArtifactCollectorCard';
import { useTaskCenterStore, type ConversationArtifact } from '../../store/taskCenter';
import { useWorkflowStore } from '../../store/workflowPanel';

const mocks = vi.hoisted(() => ({ read: vi.fn(), download: vi.fn() }));
vi.mock('./target', async original => ({ ...await original<typeof import('./target')>(), readPreviewFile: mocks.read }));
vi.mock('../../utils/download', () => ({ downloadStream: mocks.download }));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: ({ data, fileName, onPreviewError }: { data?: ArrayBuffer; fileName: string; onPreviewError?: () => void }) =>
  <div><span>{fileName}: {new TextDecoder().decode(data)}</span><button onClick={onPreviewError}>Parser failed</button></div> }));
vi.mock('../MarkdownViewer/HtmlBlock', () => ({ default: ({ code }: { code: string }) => <div>{code}</div> }));

const a = { key: 'a', filename: 'a.txt', url: '/static-files/a.txt' };
const b = { key: 'b', filename: 'b.txt', url: '/static-files/b.txt' };
const bytes = (text: string) => new TextEncoder().encode(text).buffer;
beforeEach(() => { mocks.read.mockReset(); mocks.download.mockReset(); });

it('does not send an unavailable workflow output into the ordinary sidebar', () => {
  const info = vi.spyOn(message, 'info').mockImplementation(() => (() => {}) as ReturnType<typeof message.info>);
  useWorkflowStore.setState({ sessionByConversation: {} });
  function MissingWorkflow() {
    const preview = useArtifactPreviewController('conv');
    return <><button onClick={() => preview.open({ ...a, workflow: { sessionId: 'old-session', stepId: 'prepare' } })}>Preview old workflow</button>
      {preview.target && <span>Sidebar opened</span>}</>;
  }
  try {
    render(<MissingWorkflow />);
    fireEvent.click(screen.getByRole('button', { name: 'Preview old workflow' }));
    expect(info).toHaveBeenCalled();
    expect(screen.queryByText('Sidebar opened')).not.toBeInTheDocument();
    expect(mocks.read).not.toHaveBeenCalled();
  } finally { info.mockRestore(); }
});

it('ignores the old result after switching to another file', async () => {
  let finishA!: (value: ArrayBuffer) => void;
  mocks.read.mockImplementation(target => target.key === 'a' ? new Promise(done => { finishA = done; }) : Promise.resolve(bytes('new')));
  const view = render(<ArtifactPreview target={a} onClose={vi.fn()} />);
  view.rerender(<ArtifactPreview target={b} onClose={vi.fn()} />);
  await screen.findByText('b.txt: new');
  await act(async () => finishA(bytes('stale')));
  expect(screen.queryByText(/stale/)).not.toBeInTheDocument();
});

it('offers a read-only retry after fetch failure', async () => {
  mocks.read.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce(bytes('recovered'));
  render(<ArtifactPreview target={a} onClose={vi.fn()} />);
  fireEvent.click(await screen.findByRole('button', { name: '重新加载' }));
  await screen.findByText('a.txt: recovered');
  expect(mocks.download).not.toHaveBeenCalled();
});

it('offers retry after a renderer failure without showing parser details', async () => {
  mocks.read.mockResolvedValue(bytes('corrupt'));
  render(<ArtifactPreview target={a} onClose={vi.fn()} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Parser failed' }));
  expect(screen.getByRole('alert')).toHaveTextContent('暂时无法预览');
  fireEvent.click(screen.getByRole('button', { name: '重新加载' }));
  await screen.findByText('a.txt: corrupt');
});

it('allows unsupported files to be downloaded without trying to preview them', async () => {
  mocks.read.mockResolvedValue(bytes('archive'));
  render(<ArtifactPreview target={{ ...a, filename: 'a.zip' }} onClose={vi.fn()} />);
  expect(screen.getByRole('status')).toHaveTextContent('暂不支持');
  expect(mocks.read).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: '下载 a.zip' }));
  await waitFor(() => expect(mocks.download).toHaveBeenCalledWith(expect.any(Blob), 'a.zip'));
});

it('disables downloading when the file is no longer accessible', async () => {
  mocks.read.mockRejectedValue(new Error('unavailable'));
  render(<ArtifactPreview target={a} onClose={vi.fn()} />);
  await screen.findByRole('alert');
  expect(screen.getByRole('button', { name: '下载 a.txt' })).toBeDisabled();
});

it.each([true, false])('uses a modal for an expanded workflow (%s) or a narrow viewport', async modal => {
  const media = vi.spyOn(window, 'matchMedia').mockReturnValue({
    matches: !modal, addEventListener: vi.fn(), removeEventListener: vi.fn(),
  } as unknown as MediaQueryList);
  try {
    render(<ConfigProvider theme={{ token: { motion: false } }}>
      <ArtifactPreview modal={modal} target={{ ...a, filename: 'archive.zip', canDownload: false }} onClose={vi.fn()} />
    </ConfigProvider>);
    await waitFor(() => expect(screen.getByRole('dialog')).toBeVisible());
    expect(document.querySelector('.artifact-preview-pane')).toBeNull();
    expect(screen.getByRole('button', { name: '下载 archive.zip' })).toBeDisabled();
  } finally { media.mockRestore(); }
});

it('downloads through the dedicated URL instead of exporting the preview-only content', async () => {
  mocks.read.mockResolvedValue(bytes('downloaded'));
  render(<ArtifactPreview target={{ ...a, content: 'preview', downloadUrl: '/static-files/original.txt' }} onClose={vi.fn()} />);
  await screen.findByText('a.txt: downloaded');
  fireEvent.click(screen.getByRole('button', { name: '下载 a.txt' }));
  await waitFor(() => expect(mocks.read).toHaveBeenLastCalledWith(expect.objectContaining({
    url: '/static-files/original.txt', content: undefined,
  }), expect.any(AbortSignal)));
});

it('allows another download after a timeout instead of leaving the button busy', async () => {
  vi.useFakeTimers();
  const error = vi.spyOn(message, 'error').mockImplementation(() => (() => {}) as ReturnType<typeof message.error>);
  mocks.read.mockImplementation(() => new Promise(() => {}));
  try {
    render(<ArtifactPreview target={{ ...a, filename: 'archive.zip' }} onClose={vi.fn()} />);
    const download = screen.getByRole('button', { name: '下载 archive.zip' });
    fireEvent.click(download);
    await act(async () => vi.advanceTimersByTime(60_000));
    expect(error).toHaveBeenCalled();
    fireEvent.click(download);
    expect(mocks.read).toHaveBeenCalledTimes(2);
  } finally { vi.useRealTimers(); error.mockRestore(); }
});

function Harness({ conversation = 'conv' }: { conversation?: string }) {
  const preview = useArtifactPreviewController(conversation);
  return <ArtifactPreviewContext.Provider value={preview}>
    <button onClick={() => preview.open(a)}>Open A</button>
    <button onClick={() => preview.open(b)}>Open B</button>
    <ArtifactCollectorCard sessionId={conversation} historyId="turn" />
    {preview.target && <ArtifactPreview target={preview.target} onClose={preview.close} />}
  </ArtifactPreviewContext.Provider>;
}

it('uses one preview across the collector and other entries, and clears it on conversation change', async () => {
  mocks.read.mockImplementation(async target => bytes(target.filename));
  useTaskCenterStore.setState({ loadConversationArtifacts: vi.fn(async () => {}), artifactsByConversation: { conv: [{
    artifact_id: 'file', conversation_id: 'conv', history_id: 'turn', producer_type: 'main_agent',
    content_type: 'file', slot: 'file', seq: 1, filename: 'report.pdf', value: { url: '/static-files/report.pdf' },
  } as ConversationArtifact] } });
  const view = render(<Harness />);
  fireEvent.click(screen.getByRole('button', { name: 'report.pdf' }));
  await screen.findByText('report.pdf: report.pdf');
  fireEvent.click(screen.getByRole('button', { name: 'Open A' }));
  await screen.findByText('a.txt: a.txt');
  expect(screen.queryByText('report.pdf: report.pdf')).not.toBeInTheDocument();
  expect(document.querySelectorAll('.artifact-preview')).toHaveLength(1);
  view.rerender(<Harness conversation="other" />);
  expect(document.querySelector('.artifact-preview')).toBeNull();
});

it('closes with Escape and restores focus to the opening control', async () => {
  mocks.read.mockResolvedValue(bytes('text'));
  useTaskCenterStore.setState({ artifactsByConversation: {}, loadConversationArtifacts: vi.fn(async () => {}) });
  render(<Harness />);
  const button = screen.getByRole('button', { name: 'Open A' });
  button.focus(); fireEvent.click(button);
  await screen.findByText('a.txt: text');
  fireEvent.keyDown(document, { key: 'Escape' });
  await waitFor(() => expect(document.querySelector('.artifact-preview')).toBeNull());
  await waitFor(() => expect(button).toHaveFocus());
});
