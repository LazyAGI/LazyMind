import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ConfigProvider } from 'antd';
import i18n from '@/i18n';
import type { DocumentPublicationGroupState } from '@/api/generated/core-client';
import { DocumentPublicationDialog } from './DocumentPublicationDialog';
import type { ProviderAvailability, useWriterProviderAvailability } from './useWriterProviderAvailability';

const availability = vi.hoisted(() => ({ states: { github: 'ready', notion: 'ready' } as Record<string,ProviderAvailability>, accounts: {
  github: [{ connection_id: 'github-account', display_name: 'GitHub fixture' }],
  notion: [{ connection_id: 'notion-account', display_name: 'Notion fixture' }],
}, obsidian: { provider: 'obsidian', state: 'ready', vaults: [{ id: 'vault-fixture', name: 'Fixture', writable: true }] }, refresh: vi.fn<ReturnType<typeof useWriterProviderAvailability>['refresh']>() }));
const api = vi.hoisted(() => ({ getPublicationTargets: vi.fn(), preflightPublication: vi.fn(), createPublicationGroup: vi.fn(), recoverPublication: vi.fn(), retryPublicationLocal: vi.fn(), cancelPublication: vi.fn() }));
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => api }));
const providers = [{ id: 'github', capabilities: ['create','replace'] }, { id: 'notion', capabilities: ['create','replace'] }];
let state: DocumentPublicationGroupState;
let beforePublish: ReturnType<typeof vi.fn>;
const close = vi.fn();
beforeEach(async () => {
  // Popover alignment needs non-zero layout dimensions, which jsdom does not calculate.
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 320, 180));
  await i18n.changeLanguage('en-US');
  state = { current_hash: 'content-one', preferred_providers: [], targets: [
    { provider: 'github', connection_id: 'github-account', bound: true, target_url: 'https://github.com/fixture/repo/pull/7' },
    { provider: 'notion', connection_id: 'notion-account', bound: true, target_url: 'https://www.notion.so/fixture' },
  ] };
  availability.obsidian = { provider: 'obsidian', state: 'ready', vaults: [{ id: 'vault-fixture', name: 'Fixture', writable: true }] };
  api.preflightPublication.mockResolvedValue({ data: { data: { targets: [] } } });
  beforePublish = vi.fn(async () => ({ id: 'latest-draft', revision: 4, draft: 8 }));
  api.getPublicationTargets.mockImplementation(async () => ({ data: { data: structuredClone(state) } }));
  api.createPublicationGroup.mockImplementation(async () => ({ data: { data: structuredClone(state) } }));
  availability.states = { github: 'ready', notion: 'ready' };
  availability.accounts.github = [{ connection_id: 'github-account', display_name: 'GitHub fixture' }];
  availability.accounts.notion = [{ connection_id: 'notion-account', display_name: 'Notion fixture' }];
});
afterEach(() => { cleanup(); vi.resetAllMocks(); vi.restoreAllMocks(); });
function view(extra = {}) {
  const providerAvailability: ReturnType<typeof useWriterProviderAvailability> = {
    ...availability,
    accounts: Object.fromEntries(Object.entries(availability.accounts).map(([provider, items]) => [provider, items.map(item => ({
      tenant_id: 'fixture-tenant', provider, auth_mode: 'oauth', status: 'ACTIVE',
      created_at: '2026-10-21T00:00:00Z', can_use_chat: true, ...item,
    }))])),
  };
  return <ConfigProvider theme={{ token: { motion: false } }}><DocumentPublicationDialog artifactId='draft' sourceSignature='draft:4:1' providers={providers} availability={providerAvailability} open dirty={false} onClose={close} beforePublish={beforePublish} {...extra} /></ConfigProvider>;
}
async function pickBoth() {
  fireEvent.click(await screen.findByRole('checkbox', { name: 'GitHub' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Notion' }));
}

it('starts without a default platform and cancel performs no save, write, or preference storage', async () => {
  const storage = vi.spyOn(Storage.prototype, 'setItem');
  render(view());
  expect(await screen.findByRole('checkbox', { name: 'GitHub' })).not.toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'Notion' })).not.toBeChecked();
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(close).toHaveBeenCalledOnce();
  expect(beforePublish).not.toHaveBeenCalled();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  expect(storage).not.toHaveBeenCalled();
  storage.mockRestore();
});

it('uses remembered choices, but requires confirmation even for a menu-selected existing target', async () => {
  state.preferred_providers = ['github','notion'];
  const { unmount } = render(view());
  expect(await screen.findByRole('checkbox', { name: 'GitHub' })).toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'Notion' })).toBeChecked();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  unmount(); render(view({ initialProvider: 'notion' }));
  expect(await screen.findByRole('checkbox', { name: 'Notion' })).toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'GitHub' })).not.toBeChecked();
  expect(beforePublish).not.toHaveBeenCalled();
});

it('flushes before one atomic multi-platform confirmation and blocks double clicks', async () => {
  let finish!: (value: { id: string; revision: number; draft: number }) => void;
  beforePublish.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  render(view()); await pickBoth();
  const confirm = screen.getByRole('button', { name: 'Publish to GitHub、Notion' });
  fireEvent.click(confirm); fireEvent.click(confirm);
  expect(beforePublish).toHaveBeenCalledOnce();
  expect(beforePublish).toHaveBeenCalledWith(['github', 'notion']);
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  await act(async () => finish({ id: 'saved-latest', revision: 9, draft: 12 }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup).toHaveBeenCalledWith('saved-latest', expect.objectContaining({ base_revision: 9, base_draft_version: 12, targets: [
    expect.objectContaining({ provider: 'github', connection_id: 'github-account' }),
    expect.objectContaining({ provider: 'notion', connection_id: 'notion-account' }),
  ] }), expect.anything());
  expect(close).toHaveBeenCalledOnce();
});

it('a failed draft save prevents publication and keeps the confirmation open', async () => {
  beforePublish.mockRejectedValue(new Error('Save failed'));
  render(view()); await pickBoth();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to GitHub、Notion' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Save failed');
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  expect(close).not.toHaveBeenCalled();
});

it('lost acceptance is retried with the identical saved body and key despite new edits', async () => {
  api.createPublicationGroup.mockRejectedValueOnce(new Error('network disconnected'));
  const { rerender } = render(view()); await pickBoth();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to GitHub、Notion' }));
  const again = await screen.findByRole('button', { name: /Check this publication request/ });
  const first = api.createPublicationGroup.mock.calls[0];
  availability.states = { github: 'authorize', notion: 'authorize' };
  rerender(view({ dirty: true }));
  fireEvent.click(again);
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledTimes(2));
  expect(api.createPublicationGroup.mock.calls[1]).toEqual(first);
  expect(beforePublish).toHaveBeenCalledOnce();
});

function failedNotion() {
  state.targets[0].last_success_hash = 'content-one';
  state.targets[0].operation = { operation_id:'success', status:'succeeded', provider:'github', provider_synced:true, updated_at:'2026-10-21T00:00:00Z', actions:[], source_slot_id:'draft',item_index:-1 };
  state.targets[1].operation_hash = 'content-one';
  state.targets[1].operation = { operation_id:'failed', status:'failed_no_write', provider:'notion', provider_synced:false, updated_at:'2026-10-21T00:00:00Z', actions:[], source_slot_id:'draft',item_index:-1 };
  state.preferred_providers = ['github','notion'];
}
it('same-content retry only submits the failed target', async () => {
  failedNotion(); render(view());
  fireEvent.click(await screen.findByRole('button', { name:'Select only failed platforms to retry' }));
  fireEvent.click(screen.getByRole('button', { name:'Publish to Notion' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup.mock.calls[0][1].targets).toEqual([expect.objectContaining({ provider:'notion', retry_operation_id:'failed' })]);
});
it('changed-content retry requests a fresh confirmation including the successful platform', async () => {
  failedNotion(); render(view());
  fireEvent.click(await screen.findByRole('button', { name:'Select only failed platforms to retry' }));
  state.current_hash = 'new-content';
  fireEvent.click(screen.getByRole('button', { name:'Publish to Notion' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('The content changed');
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  expect(screen.getByRole('checkbox', { name: 'GitHub' })).toBeChecked();
});
it('renders partial results and new edits without an all-success message or unsafe link', async () => {
  failedNotion(); state.targets[1].target_url = 'javascript:alert(1)';
  render(view({ open:false, dirty:true }));
  fireEvent.click(await screen.findByRole('button', { name: 'Sync results' }));
  expect(await screen.findByText('Pull request created; check merge status')).toBeInTheDocument();
  expect(screen.getByText('Failed before writing')).toBeInTheDocument();
  expect(screen.getByText('New edits not synced')).toBeInTheDocument();
  expect(screen.getAllByRole('link')).toHaveLength(1);
  expect(screen.getByRole('link')).toHaveAttribute('href','https://github.com/fixture/repo/pull/7');
});
it('refreshes the saved content hash when the same artifact advances its draft version', async () => {
  failedNotion();
  const { rerender } = render(view({ open: false, sourceSignature: 'draft:4:1' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Sync results' }));
  await screen.findByText('Pull request created; check merge status');
  expect(screen.queryByText('New edits not synced')).not.toBeInTheDocument();
  rerender(view({ open: false, dirty: true, sourceSignature: 'draft:4:1' }));
  expect(screen.getByText('New edits not synced')).toBeInTheDocument();
  state.current_hash = 'saved-new-content';
  rerender(view({ open: false, dirty: false, sourceSignature: 'draft:4:2' }));
  await waitFor(() => expect(api.getPublicationTargets).toHaveBeenCalledTimes(2));
  expect(screen.getByText('New edits not synced')).toBeInTheDocument();
  expect(beforePublish).not.toHaveBeenCalled();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});
it('keeps results collapsed until hover and closes when the pointer leaves the popover', async () => {
  render(view({ open: false }));
  const trigger = await screen.findByRole('button', { name: 'Sync results' });
  expect(trigger).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.queryByRole('link')).not.toBeInTheDocument();
  fireEvent.mouseEnter(trigger);
  const panel = await screen.findByRole('dialog', { name: 'Publication results by platform' });
  await waitFor(() => expect(panel).toBeVisible());
  expect(trigger).toHaveAttribute('aria-expanded', 'true');
  fireEvent.mouseLeave(trigger);
  fireEvent.mouseEnter(panel);
  expect(screen.getByRole('link', { name: 'Notion · Open cloud document' })).toBeVisible();
  fireEvent.mouseLeave(panel);
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
});
it('opens results by click and keeps refresh inside the popover usable', async () => {
  render(view({ open: false }));
  fireEvent.click(await screen.findByRole('button', { name: 'Sync results' }));
  const panel = await screen.findByRole('dialog', { name: 'Publication results by platform' });
  await waitFor(() => expect(panel).toBeVisible());
  const requests = api.getPublicationTargets.mock.calls.length;
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
  await waitFor(() => expect(api.getPublicationTargets).toHaveBeenCalledTimes(requests + 1));
  expect(panel).toBeVisible();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  fireEvent.mouseDown(document.body);
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
});
it('opens results from the keyboard and returns focus on Escape', async () => {
  render(view({ open: false }));
  const trigger = await screen.findByRole('button', { name: 'Sync results' });
  trigger.focus();
  fireEvent.keyDown(trigger, { key: 'Enter' });
  const panel = await screen.findByRole('dialog', { name: 'Publication results by platform' });
  await waitFor(() => expect(panel).toHaveFocus());
  fireEvent.keyDown(panel, { key: 'Escape' });
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  expect(trigger).toHaveFocus();
  expect(trigger).toHaveAttribute('aria-expanded', 'false');
});
it('provides authorization beside a disabled platform and enables selection after refresh without auto-publishing', async () => {
  availability.states.notion = 'authorize';
  state.preferred_providers = ['github', 'notion'];
  render(view());
  const notion = await screen.findByRole('checkbox', { name: 'Notion' });
  expect(notion).toBeDisabled();
  expect(notion).not.toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'GitHub' })).toBeChecked();
  const link = screen.getByRole('link', { name: 'Authorize Notion' });
  expect(link).toHaveAttribute('href', `${window.location.origin}/cloud-documents`);
  expect(link).toHaveAttribute('target', '_blank');
  fireEvent.click(link);
  expect(notion).not.toBeChecked();
  expect(beforePublish).not.toHaveBeenCalled();
  availability.states.notion = 'ready';
  const refreshButtons = screen.getAllByRole('button', { name: 'Refresh' });
  fireEvent.click(refreshButtons[refreshButtons.length - 1]);
  await waitFor(() => expect(notion).toBeEnabled());
  expect(notion).not.toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'GitHub' })).toBeChecked();
  expect(screen.queryByRole('link', { name: 'Authorize Notion' })).not.toBeInTheDocument();
  fireEvent.click(notion);
  expect(notion).toBeChecked();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it.each(['authorize', 'chat-disabled', 'checking', 'failed'] as const)('does not select an unavailable platform from a menu or remembered choice (%s)', async status => {
  availability.states.notion = status;
  state.preferred_providers = ['notion'];
  render(view({ initialProvider: 'notion' }));
  const notion = await screen.findByRole('checkbox', { name: 'Notion' });
  expect(notion).toBeDisabled();
  expect(notion).not.toBeChecked();
  fireEvent.click(notion);
  expect(notion).not.toBeChecked();
  expect(screen.getByRole('button', { name: 'Select platforms' })).toBeDisabled();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it.each(['menu', 'preference'])('allows first Obsidian publication from %s without a preexisting link', async source => {
  availability.states.obsidian = 'ready';
  state.targets.push({ provider: 'obsidian', bound: false });
  state.preferred_providers = source === 'preference' ? ['obsidian'] : [];
  render(view({ providers: [...providers, { id: 'obsidian' }], initialProvider: source === 'menu' ? 'obsidian' : undefined }));
  const obsidian = await screen.findByRole('checkbox', { name: 'Obsidian' });
  expect(obsidian).toBeEnabled();
  expect(obsidian).toBeChecked();
  expect(screen.getByRole('link', { name: 'View Obsidian setup guide' })).toHaveAttribute('href', '/cloud-documents/docs/obsidian-setup');
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Obsidian' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.preflightPublication).toHaveBeenCalledOnce();
  expect(api.createPublicationGroup.mock.calls[0][1].targets).toEqual([
    expect.objectContaining({ provider: 'obsidian', parent_uri: 'obsidian://vault-fixture/' }),
  ]);
});

it('clears Obsidian selection when its configured vault becomes unavailable', async () => {
  availability.states.obsidian = 'ready';
  state.targets.push({ provider: 'obsidian', bound: true });
  const { rerender } = render(view({ providers: [...providers, { id: 'obsidian' }], initialProvider: 'obsidian' }));
  const obsidian = await screen.findByRole('checkbox', { name: 'Obsidian' });
  expect(obsidian).toBeChecked();
  availability.states.obsidian = 'configure';
  rerender(view({ providers: [...providers, { id: 'obsidian' }], initialProvider: 'obsidian' }));
  await waitFor(() => expect(obsidian).not.toBeChecked());
  expect(obsidian).toBeDisabled();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it('does not choose the first vault when several are configured', async () => {
  availability.states.obsidian = 'ready';
  availability.obsidian.vaults.push({ id: 'second-vault', name: 'Second', writable: true });
  render(view({ providers: [...providers, { id: 'obsidian' }], initialProvider: 'obsidian' }));
  await screen.findByRole('checkbox', { name: 'Obsidian' });
  expect(screen.getByRole('combobox')).toHaveValue('');
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Obsidian' }));
  await waitFor(() => expect(api.createPublicationGroup).not.toHaveBeenCalled());
  expect(api.preflightPublication).not.toHaveBeenCalled();
});

it('keeps choices and shows a correction field when preflight detects an existing note', async () => {
  availability.states.obsidian = 'ready';
  api.preflightPublication.mockResolvedValue({ data: { data: { targets: [{ provider: 'obsidian', state: 'blocked', code: 'OBSIDIAN_TARGET_EXISTS' }] } } });
  render(view({ providers: [...providers, { id: 'obsidian' }], initialProvider: 'obsidian' }));
  await screen.findByRole('checkbox', { name: 'Obsidian' });
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Obsidian' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('already exists');
  expect(screen.getByRole('checkbox', { name: 'Obsidian' })).toBeChecked();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  fireEvent.change(screen.getByRole('textbox'), { target: { value: '../outside.md' } });
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Obsidian' }));
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/inside the authorized vault/i));
  expect(api.preflightPublication).toHaveBeenCalledOnce();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it.each(['write_started', 'outcome_unknown'] as const)('does not preselect a bound Obsidian target with an unresolved operation: %s', async status => {
  availability.states.obsidian = 'ready';
  state.targets.push({ provider: 'obsidian', bound: true, operation: { operation_id: 'obsidian-operation', provider: 'obsidian', status,
    provider_synced: false, updated_at: '2026-10-22T00:00:00Z', actions: [], source_slot_id: 'draft', item_index: -1 } });
  render(view({ providers: [...providers, { id: 'obsidian' }], initialProvider: 'obsidian' }));
  const obsidian = await screen.findByRole('checkbox', { name: 'Obsidian' });
  expect(obsidian).toBeDisabled();
  expect(obsidian).not.toBeChecked();
  expect(screen.getByRole('button', { name: 'Select platforms' })).toBeDisabled();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it('removes a revoked authorization from the selected publication targets', async () => {
  const { rerender } = render(view());
  await pickBoth();
  availability.states = { github: 'ready', notion: 'authorize' };
  rerender(view());
  await waitFor(() => expect(screen.getByRole('checkbox', { name: 'Notion' })).not.toBeChecked());
  expect(screen.getByRole('checkbox', { name: 'Notion' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to GitHub' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup.mock.calls[0][1].targets).toEqual([
    expect.objectContaining({ provider: 'github' }),
  ]);
});

it('preserves selected platforms while authorization is being refreshed', async () => {
  const { rerender } = render(view());
  await pickBoth();
  availability.states = { github: 'checking', notion: 'checking' };
  rerender(view());
  expect(screen.getByRole('checkbox', { name: 'GitHub' })).toBeChecked();
  expect(screen.getByRole('button', { name: 'Publish to GitHub、Notion' })).toBeDisabled();
  availability.states = { github: 'ready', notion: 'ready' };
  rerender(view());
  expect(screen.getByRole('checkbox', { name: 'GitHub' })).toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'Notion' })).toBeChecked();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it('cancels only a queued target and reloads the result without submitting another publication', async () => {
  state.targets[0].operation = { operation_id:'queued', status:'preparing', provider:'github', provider_synced:false, updated_at:'2026-10-21T00:00:00Z', actions:['cancel'], source_slot_id:'draft',item_index:-1 };
  api.cancelPublication.mockImplementation(async () => { state.targets[0].operation!.status = 'canceled'; state.targets[0].operation!.actions = []; });
  render(view({ open:false }));
  fireEvent.click(await screen.findByRole('button', { name: 'Sync results' }));
  fireEvent.click(await screen.findByRole('button', { name:'Cancel this platform' }));
  await waitFor(() => expect(api.cancelPublication).toHaveBeenCalledWith('queued', { silentError:true }));
  expect(await screen.findByText('Canceled')).toBeInTheDocument();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it('publishes with the authorized account and automatic document title and destination, without form fields', async () => {
  state.targets[1] = { provider: 'notion', bound: false };
  render(view({ initialProvider: 'notion' }));
  await screen.findByRole('checkbox', { name: 'Notion' });
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Notion' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup.mock.calls[0][1].targets).toEqual([
    { provider: 'notion', connection_id: 'notion-account' },
  ]);
});

it('keeps the bound account even when another authorized account comes first', async () => {
  availability.accounts.github.unshift({ connection_id: 'another-account', display_name: 'Another account' });
  render(view({ initialProvider: 'github' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Publish to GitHub' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup.mock.calls[0][1].targets[0].connection_id).toBe('github-account');
});

async function chooseNotionAccount(name = 'Another account') {
  fireEvent.mouseDown(await screen.findByRole('combobox', { name: 'Notion publishing account' }));
  fireEvent.click(await screen.findByText(name, { selector: '.ant-select-item-option-content' }));
}

it('requires an explicit account for a new target with several eligible connections', async () => {
  state.targets[1] = { provider: 'notion', bound: false };
  availability.accounts.notion.push({ connection_id: 'another-account', display_name: 'Another account' });
  render(view({ initialProvider: 'notion' }));
  expect(await screen.findByRole('checkbox', { name: 'Notion' })).toBeEnabled();
  const confirm = await screen.findByRole('button', { name: 'Publish to Notion' });
  expect(confirm).toBeDisabled();
  fireEvent.click(confirm);
  expect(beforePublish).not.toHaveBeenCalled();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
  await chooseNotionAccount();
  expect(confirm).toBeEnabled();
  fireEvent.click(confirm);
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup.mock.calls[0][1].targets[0].connection_id).toBe('another-account');
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
});

it('preserves the selected account through refresh and connection reordering', async () => {
  state.targets[1] = { provider: 'notion', bound: false };
  availability.accounts.notion.push({ connection_id: 'another-account', display_name: 'Another account' });
  const { rerender } = render(view({ initialProvider: 'notion' }));
  await chooseNotionAccount();
  const connections = availability.accounts.notion;
  availability.states.notion = 'checking';
  availability.accounts.notion = [];
  rerender(view({ initialProvider: 'notion' }));
  expect(screen.getByRole('button', { name: 'Publish to Notion' })).toBeDisabled();
  availability.states.notion = 'ready';
  availability.accounts.notion = [...connections].reverse();
  rerender(view({ initialProvider: 'notion' }));
  expect(screen.getByText('Another account', { selector: '.ant-select-selection-item' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Notion' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup.mock.calls[0][1].targets[0].connection_id).toBe('another-account');
});

it('requires a new choice if the selected connection disappears instead of falling back to the only remaining account', async () => {
  state.targets[1] = { provider: 'notion', bound: false };
  availability.accounts.notion.push({ connection_id: 'another-account', display_name: 'Another account' });
  const { rerender } = render(view({ initialProvider: 'notion' }));
  await chooseNotionAccount();
  availability.accounts.notion = availability.accounts.notion.filter(item => item.connection_id !== 'another-account');
  rerender(view({ initialProvider: 'notion' }));
  expect(screen.getByRole('button', { name: 'Publish to Notion' })).toBeDisabled();
  await chooseNotionAccount('Notion fixture');
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Notion' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup.mock.calls[0][1].targets[0].connection_id).toBe('notion-account');
});

it('discards an unsubmitted account choice when canceled and reopened', async () => {
  state.targets[1] = { provider: 'notion', bound: false };
  availability.accounts.notion.push({ connection_id: 'another-account', display_name: 'Another account' });
  const { rerender } = render(view({ initialProvider: 'notion' }));
  await chooseNotionAccount();
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  rerender(view({ open: false }));
  rerender(view({ initialProvider: 'notion' }));
  expect(await screen.findByRole('combobox', { name: 'Notion publishing account' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Publish to Notion' })).toBeDisabled();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it('reuses the saved account binding on reopening without asking again', async () => {
  state.targets[1] = { provider: 'notion', bound: false };
  availability.accounts.notion.push({ connection_id: 'another-account', display_name: 'Another account' });
  api.createPublicationGroup.mockImplementation(async () => {
    state.targets[1] = { provider: 'notion', bound: true, connection_id: 'another-account', target_url: 'https://www.notion.so/fixture' };
    return { data: { data: structuredClone(state) } };
  });
  const { rerender } = render(view({ initialProvider: 'notion' }));
  await chooseNotionAccount();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Notion' }));
  await waitFor(() => expect(close).toHaveBeenCalledOnce());
  rerender(view({ open: false }));
  rerender(view({ initialProvider: 'notion' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Publish to Notion' })).toBeEnabled());
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Notion' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledTimes(2));
  expect(api.createPublicationGroup.mock.calls[1][1].targets[0].connection_id).toBe('another-account');
});

it('keeps the chosen connection and request intact when publication acceptance is unknown', async () => {
  state.targets[1] = { provider: 'notion', bound: false };
  availability.accounts.notion.push({ connection_id: 'another-account', display_name: 'Another account' });
  api.createPublicationGroup.mockRejectedValueOnce(new Error('network disconnected'));
  render(view({ initialProvider: 'notion' }));
  await chooseNotionAccount();
  fireEvent.click(screen.getByRole('button', { name: 'Publish to Notion' }));
  const replay = await screen.findByRole('button', { name: /Check this publication request/ });
  expect(screen.getByRole('combobox', { name: 'Notion publishing account' })).toBeDisabled();
  const request = api.createPublicationGroup.mock.calls[0];
  fireEvent.click(replay);
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledTimes(2));
  expect(api.createPublicationGroup.mock.calls[1]).toEqual(request);
});

it('does not switch to another account when the bound account is unavailable', async () => {
  availability.accounts.github = [{ connection_id: 'another-account', display_name: 'Another account' }];
  render(view({ initialProvider: 'github' }));
  expect(await screen.findByRole('checkbox', { name: 'GitHub' })).toBeDisabled();
  expect(screen.getByRole('checkbox', { name: 'GitHub' })).not.toBeChecked();
  expect(screen.getByRole('link', { name: 'Authorize GitHub' })).toBeInTheDocument();
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it('does not guess a GitHub repository when no destination is available', async () => {
  state.targets[0] = { provider: 'github', bound: false };
  render(view({ initialProvider: 'github' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Publish to GitHub' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('GitHub destination');
  expect(api.createPublicationGroup).not.toHaveBeenCalled();
});

it('reuses an existing create destination but takes the title from the latest content', async () => {
  state.targets[0] = { provider: 'github', bound: false, parent_uri: 'https://github.com/fixture/repo/tree/main/docs', title: 'Old title' };
  render(view({ initialProvider: 'github' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Publish to GitHub' }));
  await waitFor(() => expect(api.createPublicationGroup).toHaveBeenCalledOnce());
  expect(api.createPublicationGroup.mock.calls[0][1].targets).toEqual([
    { provider: 'github', connection_id: 'github-account', parent_uri: 'https://github.com/fixture/repo/tree/main/docs' },
  ]);
});
