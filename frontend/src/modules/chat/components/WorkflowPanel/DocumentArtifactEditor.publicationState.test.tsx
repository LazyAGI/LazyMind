import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ConfigProvider } from 'antd';
import i18n from '@/i18n';
import type { SlotRevision } from '../../store/workflowPanel';
import { DocumentArtifactEditor } from './DocumentArtifactEditor';
import { SlotEditingContext, type SlotFooterAction } from './slotEditingContext';

const api = vi.hoisted(() => ({
  listDocumentProviders: vi.fn(), getPublicationTargets: vi.fn(), getPublicationForArtifact: vi.fn(), saveDocumentArtifact: vi.fn(),
}));
const connections = vi.hoisted(() => vi.fn());
vi.mock('@/modules/dataSource/api/clients', () => ({ dataSourceCloudOauthApi: { listConnectionsApiAuthserviceV1CloudConnectionsGet: connections } }));
vi.mock('@/modules/chat/utils/request', async original => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(), WorkflowSessionApi: () => api,
}));
vi.mock('./MarkdownArtifactEditor', () => ({ MarkdownArtifactEditor: ({ onSave }: {
  onSave: (text: string, revision: number, mode: 'draft') => Promise<unknown>;
}) => <button onClick={() => void onSave('# Saved edits', 1, 'draft')}>Save draft</button> }));

let hash: string;
let authorized: boolean;
afterEach(() => vi.restoreAllMocks());
beforeEach(async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('en-US');
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 320, 180));
  hash = 'published'; authorized = true;
  api.listDocumentProviders.mockResolvedValue({ data: { data: { providers: [{ id: 'notion' }, { id: 'github' }] } } });
  api.getPublicationForArtifact.mockResolvedValue({ data: { data: {} } });
  api.getPublicationTargets.mockImplementation(async () => ({ data: { data: { current_hash: hash, preferred_providers: [], targets: [
    { provider: 'notion', bound: true, connection_id: 'notion-account', last_success_hash: 'published' },
  ] } } }));
  api.saveDocumentArtifact.mockImplementation(async (id, body) => {
    hash = 'saved';
    return { data: { ok: true, result: { artifact_id: id, revision: 1, draft_version: 2, value: body.value } } };
  });
  connections.mockImplementation(async ({ provider }) => ({ data: { items: authorized ? [
    { provider, connection_id: `${provider}-account`, status: 'ACTIVE', can_use_chat: true },
  ] : [] } }));
});

function mountEditor() {
  let action: SlotFooterAction | undefined;
  const slot = { artifact_id: 'same-artifact', slot_id: 'draft', revision: 1, draft_version: 1,
    artifact_value: { text: '# Published' }, document: { representation: 'markdown', editable: true, capabilities: ['save', 'publish_document'] } } as SlotRevision;
  render(<ConfigProvider theme={{ token: { motion: false } }}><SlotEditingContext.Provider value={{
    setEditing: vi.fn(), registerFlush: () => () => {},
    registerFooterAction: (_key, next) => { if (next?.icon === 'write-back') action = next; return () => {}; },
  }}><DocumentArtifactEditor slot={slot} sessionId='session' /></SlotEditingContext.Provider></ConfigProvider>);
  return () => action;
}

it('shares one account request per provider across the menu and dialog on mount, focus and manual refresh', async () => {
  const action = mountEditor();
  await waitFor(() => expect(action()?.menu?.[0].disabled).toBe(false));
  expect(connections).toHaveBeenCalledTimes(2);
  act(() => action()!.onClick());
  expect(await screen.findByRole('checkbox', { name: 'Notion' })).toBeEnabled();
  expect(connections).toHaveBeenCalledTimes(2);
  authorized = false;
  fireEvent.focus(window);
  await waitFor(() => {
    expect(screen.getByRole('checkbox', { name: 'Notion' })).toBeDisabled();
    expect(action()?.menu?.[0].disabled).toBe(true);
  });
  expect(connections).toHaveBeenCalledTimes(4);
  authorized = true;
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
  // The menu is registered in an effect after the dialog publishes its choices.
  await waitFor(() => {
    expect(screen.getByRole('checkbox', { name: 'Notion' })).toBeEnabled();
    expect(action()?.menu?.[0].disabled).toBe(false);
  });
  expect(connections).toHaveBeenCalledTimes(6);
});

it('keeps saved edits marked unsynced when a draft save retains the artifact ID', async () => {
  mountEditor();
  fireEvent.click(await screen.findByRole('button', { name: 'Sync results' }));
  await screen.findByRole('dialog', { name: 'Publication results by platform' });
  expect(screen.queryByText('New edits not synced')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Save draft' }));
  expect(await screen.findByText('New edits not synced')).toBeInTheDocument();
  expect(api.saveDocumentArtifact).toHaveBeenCalledWith('same-artifact', expect.objectContaining({ base_revision: 1, base_draft_version: 1 }), expect.anything());
  expect(api.getPublicationTargets).toHaveBeenCalledTimes(2);
});
