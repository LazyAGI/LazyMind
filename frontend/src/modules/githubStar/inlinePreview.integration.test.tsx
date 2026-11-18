import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ArtifactPreview from '@/modules/chat/components/ArtifactPreview';
import MessageArtifactLinks from '@/modules/chat/components/ArtifactPreview/MessageArtifactLinks';
import { ArtifactPreviewContext, useArtifactPreviewController } from '@/modules/chat/components/ArtifactPreview/context';
import { useTaskCenterStore, type ConversationArtifact } from '@/modules/chat/store/taskCenter';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';
import i18n from '@/i18n';
import GitHubStarPrompt from './GitHubStarPrompt';
import * as api from './api';

const mocks = vi.hoisted(() => ({ read: vi.fn() }));
vi.mock('@/modules/chat/components/ArtifactPreview/target', async original => ({
  ...await original<typeof import('@/modules/chat/components/ArtifactPreview/target')>(), readPreviewFile: mocks.read,
}));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: () => <div>Loaded image result</div> }));
vi.mock('./api', async original => ({ ...await original<typeof import('./api')>(),
  readGitHubStarAccounts: vi.fn(), claimGitHubStarPrompt: vi.fn(), writeGitHubStar: vi.fn(),
  markGitHubStarSupported: vi.fn(), findGitHubStarOpportunity: vi.fn(),
}));

const opportunity: api.GitHubStarOpportunity = {
  task_id: 'completed-root', conversation_id: 'conv', status: 'succeeded', result_ready: true, source: 'final_result',
};

beforeEach(async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('zh-CN');
  mocks.read.mockResolvedValue(new TextEncoder().encode('<main>Published result</main>').buffer);
  vi.mocked(api.findGitHubStarOpportunity).mockResolvedValue(opportunity);
  vi.mocked(api.readGitHubStarAccounts).mockResolvedValue({ repository: 'LazyAGI/LazyMind', can_star: true, accounts: [] });
  vi.mocked(api.claimGitHubStarPrompt).mockResolvedValue({ show: true, reason: 'eligible', task_id: opportunity.task_id,
    min_successful_tasks: 3, cooldown_days: 7, max_prompts: 3 });
  useWorkflowStore.setState({ sessionByConversation: {} });
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  // jsdom has no layout; keep the real preview/dialog DOM visible to the production guard.
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockImplementation(function (this: HTMLElement) {
    return this.matches('.artifact-preview, [role="dialog"]') ? [{}] as unknown as DOMRectList : [] as unknown as DOMRectList;
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

function Result() {
  const preview = useArtifactPreviewController('conv');
  return <ArtifactPreviewContext.Provider value={preview}>
    <MessageArtifactLinks conversationId='conv' historyId='final-history' content='Completed Work' />
    {preview.target && <ArtifactPreview target={preview.target} onClose={preview.close} modal />}
    <GitHubStarPrompt userId='owner' conversationId='conv' blocked={false} returnTo='/task-center' />
  </ArtifactPreviewContext.Provider>;
}

async function mountInline(extension: 'html' | 'png') {
  const artifact: ConversationArtifact = {
    artifact_id: 'result', revision: 1, conversation_id: 'conv', history_id: 'final-history',
    name: 'Published result', slot: 'result', filename: `result.${extension}`, content_type: 'file', seq: 1,
    value: { url: `https://example.test/result.${extension}` }, source_type: 'main_chat',
    producer_type: 'main_agent', publication_status: 'published', created_at: '',
  };
  useTaskCenterStore.setState({ deliveriesByConversation: { conv: [artifact] }, artifactsByConversation: {} });
  const view = render(<ConfigProvider theme={{ token: { motion: false } }}>
    <MemoryRouter><Result /></MemoryRouter>
  </ConfigProvider>);
  await waitFor(() => expect(view.container.querySelector('.message-artifact-preview .artifact-preview')).toBeInTheDocument());
  return view;
}

async function completeWork() {
  await act(async () => { window.dispatchEvent(new CustomEvent(api.GITHUB_STAR_OPPORTUNITY_EVENT, { detail: opportunity })); });
}

it.each(['html', 'png'] as const)('a completed Work can invite while its published %s result stays inline', async extension => {
  const view = await mountInline(extension);
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
  await completeWork();
  expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(1);
  expect(await screen.findByRole('dialog', { name: '支持 LazyMind' })).toBeInTheDocument();
  expect(view.container.querySelector('.message-artifact-preview .artifact-preview')).toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});

it('an opened preview dialog delays the invitation until closed while the inline result remains', async () => {
  const view = await mountInline('html');
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: '预览 result.html' })); });
  expect(within(screen.getByRole('dialog')).getByRole('region', { name: '预览 result.html' })).toBeInTheDocument();
  await completeWork();
  expect(screen.queryByRole('dialog', { name: '支持 LazyMind' })).not.toBeInTheDocument();
  expect(api.readGitHubStarAccounts).not.toHaveBeenCalled();
  expect(api.claimGitHubStarPrompt).not.toHaveBeenCalled();
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: '关闭预览' })); });
  expect(api.readGitHubStarAccounts).toHaveBeenCalledTimes(1);
  expect(await screen.findByRole('dialog', { name: '支持 LazyMind' })).toBeInTheDocument();
  expect(view.container.querySelector('.message-artifact-preview .artifact-preview')).toBeInTheDocument();
  expect(api.claimGitHubStarPrompt).toHaveBeenCalledTimes(1);
  expect(api.writeGitHubStar).not.toHaveBeenCalled();
});
