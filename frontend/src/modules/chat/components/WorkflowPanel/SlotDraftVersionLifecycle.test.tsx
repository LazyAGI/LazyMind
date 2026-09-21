import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { SlotEditingContext, type SlotFooterAction } from './slotEditingContext';

const workflowApi = vi.hoisted(() => ({
  getSlots: vi.fn(),
  syncWriterDocument: vi.fn(),
  convertDocument: vi.fn(),
}));

vi.mock('@/modules/chat/utils/request', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => workflowApi,
}));

vi.mock('./WriterIRControl', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./WriterIRControl')>();
  return {
    ...actual,
    WriterIRControl: (props: any) => (
      <button
        type='button'
        onClick={() => void props.onSave(
          props.document,
          props.document,
          props.sourceRevision,
          'draft',
        )}
      >save provider draft</button>
    ),
  };
});

vi.mock('@/modules/chat/components/MarkdownViewer', () => ({
  default: ({ children }: { children: string }) => <div>{children}</div>,
}));

vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));
vi.mock('./WriterDownloadFormat', () => ({
  WriterDownloadFormatButton: () => null,
  WriterDownloadFormatDialog: () => null,
  writerDownloadCacheKey: () => '',
  writerDownloadFilename: () => '',
  writerMarkdownTitle: () => '',
}));

import { SlotRenderer } from './SlotComponents';

const writerDocument = {
  document_id: 'document-1',
  stage: 'draft',
  title: 'Draft',
  blocks: [],
  provider_binding: {
    provider: 'notion',
    document_id: 'page-1',
    uri: 'https://notion.so/page-1',
  },
};

function syncResponse(revision: number, draftVersion: number) {
  return {
    data: {
      code: 0,
      data: {
        status: 'synced',
        revision,
        draft_version: draftVersion,
        provider_synced: true,
        artifact_saved: true,
        patch_result: { success: true },
        document: writerDocument,
      },
    },
  };
}

describe('inline Writer draft baseline lifecycle', () => {
  beforeEach(() => {
    workflowApi.getSlots.mockReset();
    workflowApi.getSlots.mockResolvedValue({ data: { data: { slots: [] } } });
    workflowApi.syncWriterDocument.mockReset();
    workflowApi.syncWriterDocument
      .mockResolvedValueOnce(syncResponse(2, 1))
      .mockResolvedValueOnce(syncResponse(3, 1));
  });

  it('uses the returned draft version for the next provider save on the same mount', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    workflowApi.convertDocument.mockResolvedValue({ data: { code: 0, data: { format: 'latex', content: 'converted fixture' } } });
    let copy: SlotFooterAction | undefined;
    const slot: SlotRevision = {
      slot_id: 'provider_document',
      revision: 1,
      draft_version: 1,
      selected: true,
      slot: 'provider_document',
      created_at: '2026-09-10T00:00:00Z',
      content_type: 'json',
      artifact_value: writerDocument,
      change_source: 'human',
    };
    render(
      <SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFlush: () => () => {},
        registerFooterAction: (_key, action) => { if (action?.icon === 'copy') copy = action; return () => {}; },
      }}><SlotRenderer
        slot={slot}
        expectedType='text'
        sessionId='writer-session'
        slotId='provider_document'
      /></SlotEditingContext.Provider>,
    );

    const save = await screen.findByRole('button', { name: 'save provider draft' });
    await waitFor(() => expect(copy).toBeDefined());
    act(() => copy!.menu!.find(item => item.key === 'latex')!.onClick());
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    expect(workflowApi.convertDocument).toHaveBeenLastCalledWith('writer-session', 'provider_document', -1, 1, 'latex', expect.any(Object), 1);
    fireEvent.click(save);
    await waitFor(() => expect(workflowApi.syncWriterDocument).toHaveBeenNthCalledWith(
      1,
      'writer-session',
      'provider_document',
      -1,
      expect.objectContaining({ base_revision: 1, base_draft_version: 1 }),
      { silentError: true },
    ));

    act(() => copy!.onClick());
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(2));
    expect(workflowApi.convertDocument).toHaveBeenLastCalledWith('writer-session', 'provider_document', -1, 2, 'latex', expect.any(Object), 1);
    fireEvent.click(save);
    await waitFor(() => expect(workflowApi.syncWriterDocument).toHaveBeenNthCalledWith(
      2,
      'writer-session',
      'provider_document',
      -1,
      expect.objectContaining({ base_revision: 2, base_draft_version: 1 }),
      { silentError: true },
    ));
  });
});
