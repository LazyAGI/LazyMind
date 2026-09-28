import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { SlotRenderer } from '../SlotComponents';

const api = vi.hoisted(() => ({ getSlotItemVersions: vi.fn(), rollbackSlotItem: vi.fn() }));
vi.mock('@/modules/chat/utils/request', async original => ({
  ...await original<typeof import('@/modules/chat/utils/request')>(), WorkflowSessionApi: () => api,
}));
vi.mock('../ArtifactRewriteDialog', () => ({ ArtifactRewriteDialog: () => null }));
vi.mock('../FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));

function slotFor(page = 0, revision = 2, json = false): SlotRevision {
  return { slot_id: 'preview_html', slot: 'preview_html', list_index: page,
    revision, selected: true, created_at: '2026-09-23T00:00:00Z', change_source: 'ai',
    content_type: json ? 'json' : 'text', artifact_value: json
      ? { data: { layout: 'title', title: `Page ${page + 1} version ${revision}` } }
      : { text: `<html><body>Page ${page + 1} version ${revision}</body></html>` } };
}

beforeEach(async () => {
  vi.clearAllMocks();
  await i18n.changeLanguage('zh-CN');
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  api.getSlotItemVersions.mockImplementation(async (_session, _slot, page) => ({ data: { data: {
    versions: [1, 2].map(revision => ({ revision, selected: revision === 2, change_source: 'ai',
      created_at: '2026-09-23T00:00:00Z', content_snapshot: slotFor(page, revision).artifact_value })),
  } } }));
  api.rollbackSlotItem.mockResolvedValue({ data: { code: 0 } });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const props = { sessionId: 'ppt-history', slotId: 'preview_html', revisionCount: 2,
  widget: { widgetType: 'html-slide' as const } };

it.each([false, true])('shows version history for a slide (JSON=%s), scoped to the selected page', async json => {
  const view = render(<SlotRenderer {...props} slot={slotFor(0, 2, json)} />);
  const trigger = screen.getByRole('button', { name: /版本历史/ });
  expect(trigger).toHaveTextContent('v2');
  fireEvent.click(trigger);
  expect(await screen.findByRole('dialog')).toBeInTheDocument();
  expect(api.getSlotItemVersions).toHaveBeenLastCalledWith('ppt-history', 'preview_html', 0);
  fireEvent.click(screen.getByRole('button', { name: '关闭版本历史' }));
  view.rerender(<SlotRenderer {...props} slot={slotFor(1, 1, json)} revisionCount={1} />);
  expect(screen.getByRole('button', { name: /版本历史/ })).toHaveTextContent('v1');
  fireEvent.click(screen.getByRole('button', { name: /版本历史/ }));
  await screen.findByRole('dialog');
  expect(api.getSlotItemVersions).toHaveBeenLastCalledWith('ppt-history', 'preview_html', 1);
});

it('refreshes the slide after applying a historical version and displays its selected revision', async () => {
  const onRefresh = vi.fn();
  const view = render(<SlotRenderer {...props} slot={slotFor()} onRefresh={onRefresh} />);
  fireEvent.click(screen.getByRole('button', { name: /版本历史/ }));
  const dialog = await screen.findByRole('dialog');
  fireEvent.click(within(dialog).getByRole('option', { name: /v1/ }));
  fireEvent.click(within(dialog).getByRole('button', { name: /应用.*v1/ }));
  await waitFor(() => expect(onRefresh).toHaveBeenCalledTimes(1));
  expect(api.rollbackSlotItem).toHaveBeenCalledWith('ppt-history', 'preview_html', 0, 1);
  view.rerender(<SlotRenderer {...props} slot={slotFor(0, 1)} onRefresh={onRefresh} />);
  expect(screen.getByRole('button', { name: /版本历史/ })).toHaveTextContent('v1');
  await waitFor(() => expect(screen.getByTitle('slide-1').getAttribute('srcdoc')).toContain('Page 1 version 1'));
});

it('allows read-only history inspection but disables rollback', async () => {
  render(<SlotRenderer {...props} slot={slotFor()} readOnly />);
  fireEvent.click(screen.getByRole('button', { name: /版本历史/ }));
  const dialog = await screen.findByRole('dialog');
  fireEvent.click(within(dialog).getByRole('option', { name: /v1/ }));
  expect(within(dialog).getByRole('button', { name: /应用.*v1/ })).toBeDisabled();
  expect(api.rollbackSlotItem).not.toHaveBeenCalled();
});

it('preserves the current slide when history fails and allows retry', async () => {
  api.getSlotItemVersions.mockRejectedValueOnce(new Error('fixture history unavailable'));
  render(<SlotRenderer {...props} slot={slotFor()} />);
  fireEvent.click(screen.getByRole('button', { name: /版本历史/ }));
  expect(await screen.findByRole('alert')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: /版本历史/ })).toHaveTextContent('v2');
  expect(api.rollbackSlotItem).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: /版本历史/ }));
  expect(await screen.findByRole('dialog')).toBeInTheDocument();
});
