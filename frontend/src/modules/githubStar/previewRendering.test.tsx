import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { PreviewContent } from '@/modules/chat/components/ArtifactPreview';
import { useArtifactPreviewController } from '@/modules/chat/components/ArtifactPreview/context';
import { TaskArtifactList } from '@/modules/chat/components/TaskCenter/TaskArtifactList';
import RenderMarkdown from '@/modules/knowledge/components/FileViewer/renderers/render-markdown';
import GitHubStarPrompt from './GitHubStarPrompt';
import * as api from './api';
import i18n from '@/i18n';

const mocks = vi.hoisted(() => ({ read: vi.fn() }));
vi.mock('@/modules/chat/components/ArtifactPreview/target', async original => ({
  ...await original<typeof import('@/modules/chat/components/ArtifactPreview/target')>(), readPreviewFile: mocks.read,
}));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: ({ data }: { data: ArrayBuffer }) => <RenderMarkdown fileData={data} /> }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(),
  readGitHubStarAccounts: vi.fn(), claimGitHubStarPrompt: vi.fn(), writeGitHubStar: vi.fn(),
  findGitHubStarOpportunity: vi.fn(),
}));
const opportunity: api.GitHubStarOpportunity = { task_id: 'final-root', conversation_id: 'conv',
  status: 'succeeded', result_ready: true, source: 'final_result' };
const target = { key: 'final', filename: '报告', content: 'Complete result' };
const bytes = () => new TextEncoder().encode('Complete result').buffer;
beforeEach(async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('zh-CN');
  mocks.read.mockResolvedValue(bytes());
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(opportunity);
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', can_star: true, accounts: [] });
  vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({ show: true, reason: 'eligible', task_id: opportunity.task_id,
    min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 });
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockImplementation(function (this: HTMLElement) {
    return this.matches('.artifact-preview, [role="dialog"]') ? [{}] as unknown as DOMRectList : [] as unknown as DOMRectList;
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });
function Result({ mode = 'loaded', userId = 'owner' }: { mode?: string; userId?: string }) {
  const preview = useArtifactPreviewController('conv');
  return <><button onClick={() => preview.open({ ...target,
    filename: mode === 'unsupported' ? 'archive.zip' : mode === 'markdown' ? 'report.md' : target.filename,
    content: mode === 'unsupported' ? undefined : target.content,
  })}>查看最终结果</button>
    {preview.target && <PreviewContent target={preview.target} close={preview.close} />}
    <GitHubStarPrompt userId={userId} blocked={false} returnTo='/task-center' /></>;
}
function mount(mode = 'loaded', userId = 'owner') {
  return render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><Result mode={mode} userId={userId} /></MemoryRouter></ConfigProvider>);
}
const open = () => fireEvent.click(screen.getByRole('button', { name: '查看最终结果' }));
const close = () => fireEvent.click(screen.getByRole('button', { name: '关闭预览' }));
async function complete() {
  await act(async () => { window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: opportunity })); });
}

it.each(['loading', 'loaded', 'failed', 'unsupported', 'markdown'])('preview close alone never invites (%s)', async mode => {
  if (mode === 'loading') mocks.read.mockReturnValue(new Promise(() => {}));
  if (mode === 'failed') mocks.read.mockRejectedValue(new Error('network'));
  if (mode === 'markdown') vi.useFakeTimers();
  const received = vi.fn();
  window.addEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    mount(mode);
    await act(async () => open());
    if (mode === 'loaded') await screen.findByText('Complete result');
    if (mode === 'failed') await screen.findByRole('alert');
    if (mode === 'markdown') expect(screen.queryByText('Complete result')).not.toBeInTheDocument();
    await act(async () => close());
    expect(received).not.toHaveBeenCalled();
    expect(api.readGitHubStarAccounts).not.toHaveBeenCalled();
    expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
    expect(api.writeGitHubStar).not.toHaveBeenCalled();
  } finally { window.removeEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});

it('a completed Work opportunity waits for the preview to close without consuming quota early', async () => {
  mount(); await act(async () => open()); await complete();
  expect(api.readGitHubStarAccounts).not.toHaveBeenCalled();
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  await act(async () => close());
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it('a granted claim waits for a newly opened preview, then displays without a second claim', async () => {
  let finish!: (result: api.GitHubStarClaim) => void;
  vi.mocked(api.claimGitHubStarPrompt).mockReturnValue(new Promise(resolve => { finish = resolve; }));
  mount(); await complete();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  await act(async () => open());
  await act(async () => finish({ show: true, reason: 'eligible', task_id: opportunity.task_id,
    min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  await act(async () => close());
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(2);
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it('switching users discards the opportunity waiting behind a preview', async () => {
  const view = mount(); await act(async () => open()); await complete();
  view.rerender(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><Result userId='other-owner' /></MemoryRouter></ConfigProvider>);
  await act(async () => close());
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
});

it('a withdrawn Work success cannot display an invitation after a delayed granted claim', async () => {
  let finish!: (result: api.GitHubStarClaim) => void;
  vi.mocked(api.claimGitHubStarPrompt).mockReturnValue(new Promise(resolve => { finish = resolve; }));
  mount(); await complete(); await act(async () => open());
  await act(async () => finish({ show: true, reason: 'eligible', task_id: opportunity.task_id,
    min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 }));
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(null);
  await act(async () => close());
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it('the fallback final artifact drawer also closes normally without creating an opportunity', async () => {
  const received = vi.fn();
  window.addEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received);
  try {
    render(<TaskArtifactList final artifacts={[{
      artifact_id: 'fallback', revision: 1, producer_display_key: 'root', name: 'fallback.md',
      state: 'ready', size_bytes: null, content_type: 'text/markdown', preview_kind: 'text',
      capabilities: { preview: true, open: false, download: false }, created_at: '2026-11-04T09:00:00Z',
      inline_content: 'Complete result',
    }]} />);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'fallback.md' })));
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    close();
    expect(received).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  } finally { window.removeEventListener(api.GITHUB_STAR_OPPORTUNITY_EVENT, received); }
});
