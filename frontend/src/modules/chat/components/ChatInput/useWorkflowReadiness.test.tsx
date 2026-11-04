import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider, message } from 'antd';
import { useState } from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useWorkflowReadiness } from './useWorkflowReadiness';
import WorkflowRepairReturn from './WorkflowRepairReturn';
import type { WorkflowReadiness } from '@/modules/workflow/workflowDraftApi';

const mocks = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn(), preserve: vi.fn() }));
vi.mock('@/modules/workflow/workflowDraftApi', () => ({ getWorkflowReadiness: mocks.get }));
vi.mock('react-i18next', async original => ({
  ...await original<typeof import('react-i18next')>(),
  useTranslation: () => ({ t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key }),
}));
const mention = { mention_id: 'w', type: 'workflow' as const, resource_id: 'user:owner:workflow', display_name: 'Workflow' };
const report: WorkflowReadiness = { ready: false, workflow_ref: mention.resource_id, name: 'Workflow', revision_id: 'revision-1', revision_no: 1,
  editable: true, repair_url: '/memory-management/workflows/draft-1',
  issues: [{ code: 'TEST_CONFIG_ERROR', message: '缺少必需配置' }] };
function Composer() {
  const [text, setText] = useState('Workflow 保留原文');
  const [conversation, setConversation] = useState('conversation-1');
  const readiness = useWorkflowReadiness(conversation, () => mocks.preserve({ text, mentions: [mention], files: ['report.pdf'] }));
  return <><input aria-label="draft" value={text} onChange={event => setText(event.target.value)} />
    <span>report.pdf</span><span>Workflow reference</span>
    <button disabled={readiness.busy} onClick={async () => { if (await readiness.check([mention])) mocks.send(); }}>send</button>
    <button onClick={() => setConversation('conversation-2')}>switch conversation</button>{readiness.dialog}</>;
}
function Destination() { const location = useLocation(); return <><output>{location.pathname}</output><WorkflowRepairReturn /></>; }
function mount() {
  return render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={['/agent/chat/home/conversation-1?mode=work']}><Routes>
    <Route path="/agent/chat/home/:id" element={<Composer />} /><Route path="/memory-management/workflows/:id" element={<Destination />} />
    <Route path="/settings" element={<Destination />} />
  </Routes></MemoryRouter></ConfigProvider>);
}
beforeEach(() => { vi.clearAllMocks(); mocks.get.mockResolvedValue(report); });

describe('Workflow admission dialog', () => {
  it.each(['cancel', 'close'] as const)('keeps the complete draft and does not submit when using %s', async action => {
    mount(); fireEvent.click(screen.getByText('send'));
    await screen.findByRole('dialog');
    await waitFor(() => expect(screen.getByText('缺少必需配置')).toBeVisible());
    expect(mocks.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: action === 'cancel' ? 'common.cancel' : 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.getByRole('textbox')).toHaveValue('Workflow 保留原文');
    expect(screen.getByText('report.pdf')).toBeVisible();
    expect(screen.getByText('Workflow reference')).toBeVisible();
    expect(mocks.preserve).not.toHaveBeenCalled();
    mocks.get.mockResolvedValue({ ...report, ready: true, issues: [] });
    fireEvent.click(screen.getByText('send'));
    await waitFor(() => expect(mocks.send).toHaveBeenCalledTimes(1));
    expect(mocks.get).toHaveBeenCalledTimes(2);
  });
  it('preserves the draft before opening the matching editor and provides a return action', async () => {
    mount(); fireEvent.click(screen.getByText('send')); await screen.findByRole('dialog');
    fireEvent.click(screen.getByRole('button', { name: 'chat.workflowReadinessRepair' }));
    expect(mocks.preserve).toHaveBeenCalledWith({ text: 'Workflow 保留原文', files: ['report.pdf'], mentions: [mention] });
    expect(await screen.findByText('/memory-management/workflows/draft-1')).toBeVisible();
    expect(mocks.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'chat.workflowReadinessReturn' }));
    expect(await screen.findByRole('textbox')).toHaveValue('Workflow 保留原文');
  });
  it('opens configuration settings for a missing model or tool instead of the draft editor', async () => {
    mocks.get.mockResolvedValue({ ...report, issues: [{ code: 'CONFIG', message: '未配置模型', repair_url: '/settings?section=models' }] });
    mount(); fireEvent.click(screen.getByText('send')); await screen.findByRole('dialog');
    await waitFor(() => expect(screen.getByText('chat.workflowReadinessSettingsHint')).toBeVisible());
    fireEvent.click(screen.getByRole('button', { name: 'chat.workflowReadinessRepair' }));
    expect(await screen.findByText('/settings')).toBeVisible();
  });
  it('reports a failed check without calling it a configuration error', async () => {
    const error = vi.spyOn(message, 'error').mockImplementation(() => (() => undefined) as never);
    mocks.get.mockRejectedValue(new Error('network'));
    mount(); fireEvent.click(screen.getByText('send'));
    await waitFor(() => expect(error).toHaveBeenCalledWith('chat.workflowReadinessCheckFailed'));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(mocks.send).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox')).toHaveValue('Workflow 保留原文'); error.mockRestore();
  });
  it('ignores a late failed check after switching conversations', async () => {
    let finish!: (value: WorkflowReadiness) => void;
    mocks.get.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    mount(); fireEvent.click(screen.getByText('send'));
    fireEvent.click(screen.getByText('switch conversation'));
    await act(async () => { finish(report); });
    expect(screen.queryByRole('dialog')).toBeNull(); expect(mocks.send).not.toHaveBeenCalled();
  });
});
