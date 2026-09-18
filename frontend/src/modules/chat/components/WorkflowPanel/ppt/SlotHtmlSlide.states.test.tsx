import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { ArtifactPendingContext } from '../artifactPendingContext';
import { SlotHtmlSlide } from './SlotHtmlSlide';

const { t } = vi.hoisted(() => ({ t: (key: string) => key }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t }) }));
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: vi.fn() }));
vi.mock('../ArtifactRewriteDialog', () => ({ ArtifactRewriteDialog: () => null }));
vi.mock('./echartsInline', () => ({ htmlWithInlinedEcharts: async (html: string) => html }));
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({
  resolveCoreAssetUrl: (url: string) => url,
  resolveMarkdownImageUrlAsync: async () => '/slide.html',
  isExpiredSignedUrl: () => false,
}));
beforeEach(() => vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} }));
afterEach(() => vi.unstubAllGlobals());

const revision = (value: unknown) => ({ slot_id: 'html', slot: 'html', revision: 1, selected: true, created_at: '', artifact_value: value }) as SlotRevision;
const view = (value: unknown, pending = true) => <ArtifactPendingContext.Provider value={pending}><SlotHtmlSlide slot={revision(value)} /></ArtifactPendingContext.Provider>;

describe('slide publication states', () => {
  it('waits for an empty slide while the step runs, then renders its HTML', async () => {
    const { rerender, container } = render(view({ text: '' }));
    expect(await screen.findByText('chat.workflowSlideWaiting')).toBeInTheDocument();
    expect(container.querySelector('.slot-html-slide--error')).toBeNull();
    rerender(view({ text: '<!doctype html><html><body>slide</body></html>' }));
    await waitFor(() => expect(container.querySelector('iframe')).not.toBeNull());
  });
  it('does not hide malformed published content behind waiting', async () => {
    render(view({ text: 'this is not HTML' }));
    expect(await screen.findByText('chat.workflowSlideInvalid')).toBeInTheDocument();
  });
  it('reports an empty completed output as invalid', async () => {
    render(view({ text: '' }, false));
    expect(await screen.findByText('chat.workflowSlideInvalid')).toBeInTheDocument();
  });
  it('shows loading while reading a file, then a real load error', async () => {
    let finish!: (value: unknown) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise(resolve => { finish = resolve; })));
    try {
      render(view({ path: 'slide.html', type: 'file' }));
      expect(screen.getByText('chat.workflowSlideLoading')).toBeInTheDocument();
      await waitFor(() => expect(finish).toBeDefined());
      finish({ ok: false });
      expect(await screen.findByText('chat.workflowSlideLoadFailed')).toBeInTheDocument();
    } finally { vi.unstubAllGlobals(); }
  });
});
