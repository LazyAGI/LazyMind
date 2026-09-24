import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';

const workflowApi = vi.hoisted(() => ({ executeArtifactAction: vi.fn() }));
const dialogRender = vi.hoisted(() => vi.fn());

vi.mock('@/modules/chat/utils/request', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => workflowApi,
}));

vi.mock('../ArtifactRewriteDialog', () => ({
  ArtifactRewriteDialog: (props: any) => {
    dialogRender(props);
    return (
      <button
        type='button'
        onClick={() => props.onPreviewReady({
          status: 'ready',
          action: 'rewrite_selection',
          base_revision: props.baseRevision,
          base_draft_version: props.baseDraftVersion,
          representation: 'ppt_html',
          target: { type: 'block', block_type: 'text' },
          preview: { old_text: 'Old', new_text: 'New' },
          patch: { type: 'string_replace_set', payload: {} },
          artifact: { content_type: 'text', value: '<div data-el="title">New</div>' },
          commit: { token: 'commit-1' },
          candidate_html: '<div data-el="title">New</div>',
        })}
      >
        apply slide rewrite
      </button>
    );
  },
}));

import { SlotHtmlSlide } from './SlotHtmlSlide';

describe('SlotHtmlSlide mutation baseline', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      disconnect() {}
    });
    dialogRender.mockReset();
    workflowApi.executeArtifactAction.mockReset();
    workflowApi.executeArtifactAction.mockResolvedValue({
      data: {
        code: 0,
        data: { status: 'applied', revision: 2, draft_version: 1 },
      },
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  it('uses the applied revision and draft version for the next rewrite on the same mount', async () => {
    const slot: SlotRevision = {
      slot_id: 'slides',
      revision: 1,
      draft_version: 4,
      list_index: 0,
      selected: true,
      slot: 'slides',
      created_at: '2026-09-10T00:00:00Z',
      artifact_value: '<html><body><div data-el="title">Old</div></body></html>',
      content_type: 'text',
      change_source: 'human',
    };
    render(
      <SlotHtmlSlide
        slot={slot}
        sessionId='ppt-session'
        slotId='slides'
      />,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'apply slide rewrite' }));

    await waitFor(() => expect(workflowApi.executeArtifactAction).toHaveBeenCalledWith(
      'ppt-session',
      'slides',
      0,
      expect.objectContaining({ base_revision: 1, base_draft_version: 4 }),
      { silentError: true },
    ));
    await waitFor(() => {
      const latest = dialogRender.mock.calls[dialogRender.mock.calls.length - 1]?.[0];
      expect(latest.baseRevision).toBe(2);
      expect(latest.baseDraftVersion).toBe(1);
    });
  });
  it('keeps enlarged navigation open across pages and disables the end buttons', async () => {
    const slot = {
      slot_id: 'slides', slot: 'slides', revision: 1, list_index: 0,
      selected: true, created_at: '', content_type: 'text', change_source: 'human',
      artifact_value: '<html><body>Page one</body></html>',
    } as SlotRevision;
    const navigation = {
      index: 0, total: 2, expanded: true,
      onExpandedChange: vi.fn(), onChange: vi.fn(),
    };
    const { rerender } = render(<SlotHtmlSlide slot={slot} navigation={navigation} />);
    expect(screen.getByRole('button', { name: '上一页幻灯片' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '下一页幻灯片' }));
    expect(navigation.onChange).toHaveBeenCalledWith(1);
    rerender(<SlotHtmlSlide slot={{ ...slot, list_index: 1,
      artifact_value: '<html><body>Page two</body></html>',
    }} navigation={{ ...navigation, index: 1 }} />);
    expect(screen.getByRole('dialog', { name: '放大幻灯片预览' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '下一页幻灯片' })).toBeDisabled();
    expect(screen.getByText('2 / 2')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTitle('放大预览-2').getAttribute('srcdoc')).toContain('Page two'));
    fireEvent.click(screen.getByRole('button', { name: '上一页幻灯片' }));
    expect(navigation.onChange).toHaveBeenLastCalledWith(0);
    fireEvent.click(screen.getByRole('button', { name: '关闭放大预览' }));
    expect(navigation.onExpandedChange).toHaveBeenCalledWith(false);
  });

});
