import { createElement, type ComponentType } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { WriterProviderChoice, SlotRenderer, SlotEditingContext } from './SlotComponents';
import type { SlotFooterAction } from './slotEditingContext';

it('offers a newly registered Provider from supplied capabilities without adding a switch', () => {
  const onChange = vi.fn();
  const Choice = WriterProviderChoice as ComponentType<{
    initialProvider: string; githubEnabled: boolean; onChange: (id: string) => void;
    providers: Array<{ id: string; capabilities: string[] }>;
  }>;
  render(createElement(Choice, {
    initialProvider: 'future-provider', githubEnabled: false, onChange,
    providers: [{ id: 'future-provider', capabilities: ['create', 'replace'] }],
  }));
  const option = screen.getByRole('radio', { name: 'future-provider' });
  expect(option).toBeEnabled();
  fireEvent.click(option);
  expect(screen.queryByRole('radio', { name: /Feishu|飞书/ })).not.toBeInTheDocument();
});

vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));
vi.mock('@/modules/chat/components/MarkdownViewer', () => ({ default: ({ children }: { children: string }) => createElement('div', null, children) }));
const publicationApi = vi.hoisted(() => ({
  getPublicationForArtifact: vi.fn(async () => ({ data: { data: {} } })),
  listDocumentProviders: vi.fn(async () => ({ data: { data: { providers: [{ id: 'future-provider', capabilities: ['create', 'replace'] }] } } })),
  publishDocument: vi.fn(async (..._args: unknown[]) => ({ data: { data: { artifact_id: 'saved-artifact', revision: 4, draft_version: 1, provider: 'future-provider', provider_synced: true, artifact_saved: true } } })),
}));
const confirm = vi.hoisted(() => vi.fn());
vi.mock('@/modules/chat/utils/request', async (original) => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => publicationApi,
}));
vi.mock('antd', async (original) => {
  const actual = await original<typeof import('antd')>();
  return { ...actual, Modal: { ...actual.Modal, confirm } };
});

it('connects an arbitrary slot to the generic publication API using the server descriptor', async () => {
  publicationApi.publishDocument.mockClear(); confirm.mockClear();
  let action: SlotFooterAction | undefined;
  const registerFooterAction = (_key: string, next: SlotFooterAction | null) => {
    if (next?.icon === 'write-back') action = next;
    return () => {};
  };
  const slot = { artifact_id: 'unknown-artifact', slot_id: 'unknown-slot', slot: 'unknown-slot',
    revision: 3, draft_version: 7, selected: true, validity: 'effective', created_at: '2026-09-12T00:00:00Z',
    content_type: 'text/markdown', artifact_value: { text: '# Unknown workflow document' },
    document: { representation: 'markdown', schema: 'text/markdown', editable: true, capabilities: ['save', 'publish_document'] } };
  render(createElement(SlotEditingContext.Provider, { value: { setEditing: vi.fn(), registerFlush: () => () => {}, registerFooterAction } },
    createElement(SlotRenderer, { slot, sessionId: 'unknown-session', onRefresh: vi.fn() })));
  await waitFor(() => expect(action?.disabled).toBe(false));
  expect(action?.flushBeforeAction).toBe(true);
  act(() => action!.onClick());
  expect(confirm).not.toHaveBeenCalled();
  await waitFor(() => expect(publicationApi.publishDocument).toHaveBeenCalledTimes(1));
  const sent = publicationApi.publishDocument.mock.calls[0];
  expect(sent[0]).toBe('unknown-artifact');
  expect(sent[1]).toEqual(expect.objectContaining({
    action: 'publish_document', base_revision: 3, base_draft_version: 7,
    input: expect.objectContaining({ provider: 'future-provider', idempotency_key: expect.any(String) }),
  }));
});
