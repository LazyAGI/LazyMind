import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ComponentProps } from 'react';
import type StateGraphEditor from '../../components/StateGraphEditor';
import type { WorkflowDraftRecord } from '../../workflowDraftApi';
import WorkflowDetailPage from './index';

const mocks = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), generate: vi.fn(), props: {} as ComponentProps<typeof StateGraphEditor>, t: (key: string) => key }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('react-router-dom', () => ({ useParams: () => ({ workflowId: 'draft-test' }), useNavigate: () => vi.fn(), useOutletContext: () => ({}) }));
vi.mock('@/components/request', () => ({ localizeErrorCode: () => 'Request failed' }));
vi.mock('../../components/LinkedSkillButton', () => ({ default: () => null }));
vi.mock('../../components/StateGraphEditor', () => ({ default: (props: ComponentProps<typeof StateGraphEditor>) => {
  mocks.props = props;
  return <div role="region" aria-label="Editor" data-readonly={props.readonly}>{props.workflowName}</div>;
} }));
vi.mock('../../workflowDraftApi', () => ({
  getWorkflowDraft: mocks.get, updateWorkflowDraftContent: mocks.save, aiGenerateWorkflowDraft: mocks.generate,
  listWorkflowDrafts: vi.fn(async () => ({ records: [], total: 0 })), listWorkflowVersions: vi.fn(async () => []),
}));

const draft = {
  id: 'draft-test', name: 'My research workflow', version: 3, source_type: 'ai', generate_status: 'done',
  workflow_yaml_content: 'id: research-v2\nname: My research workflow\nsteps: [{id: research}]\nslots: [{id: report}]',
  state_yaml_content: 'steps: [{id: research}]\ntransitions: {__start__: [{to: research}], research: [{to: __end__}]}',
  design_brief_content: 'Research brief', scenario_content: 'Research documentation',
} as WorkflowDraftRecord;
function show(value: Partial<WorkflowDraftRecord> = {}) {
  mocks.get.mockResolvedValue({ ...draft, ...value });
  return render(<ConfigProvider theme={{ token: { motion: false } }}><WorkflowDetailPage /></ConfigProvider>);
}
beforeEach(() => { mocks.save.mockReset(); mocks.generate.mockReset(); localStorage.clear(); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('workflow draft lifecycle', () => {
  it.each(['analyzing', 'generating', 'brief_done', 'skeleton_done', 'state_done'])('prevents edits during %s', async (generate_status) => {
    show({ generate_status });
    expect(await screen.findByRole('region', { name: 'Editor' })).toHaveAttribute('data-readonly', 'true');
  });

  it('shows the same display name before and during editing and persists a rename', async () => {
    show();
    const button = await screen.findByRole('button', { name: draft.name });
    fireEvent.click(button);
    const input = screen.getByDisplayValue(draft.name);
    mocks.save.mockResolvedValue({ ...draft, name: 'Renamed research', version: 4 });
    fireEvent.change(input, { target: { value: 'Renamed research' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    await waitFor(() => expect(mocks.save).toHaveBeenCalledWith(draft.id, { name: 'Renamed research', version: 3 }));
    expect(await screen.findByRole('button', { name: 'Renamed research' })).toBeInTheDocument();
  });

  it('renders structured generation failures instead of crashing the recovery page', async () => {
    show({ generate_status: 'failed', generate_error: JSON.stringify({ phase: 'state_machine', code: 'GENERATION_CANCELED', message: 'Stopped by user', suggestions: ['Retry from the last checkpoint.'] }) });
    const details = await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' });
    expect(details).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('GENERATION_CANCELED')).not.toBeVisible();
    fireEvent.click(details);
    expect(details).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('Retry from the last checkpoint.')).toBeVisible();
    expect(screen.getByText('Stopped by user')).toBeVisible();
    fireEvent.click(details);
    expect(screen.getByText('GENERATION_CANCELED')).not.toBeVisible();
    expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRegenerate' })).toBeEnabled();
  });

  it('keeps recovery available without error details and only starts generation after confirmation', async () => {
    show({ generate_status: 'failed', generate_error: '' });
    fireEvent.click(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailRegenerate' }));
    expect(screen.queryByRole('button', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('dialog', { name: '选择重新生成起点' })).toBeVisible());
    expect(mocks.generate).not.toHaveBeenCalled();
  });

  it('keeps the required-action hint visible for failures that cannot be retried immediately', async () => {
    show({ generate_status: 'failed', generate_error: JSON.stringify({ phase: 'validation', recoverable: false, message: 'Invalid workflow configuration' }) });
    expect(await screen.findByText('selfEvolutionRun.workflowDetailFailureNeedsAttention')).toBeVisible();
    expect(screen.getByText('Invalid workflow configuration')).not.toBeVisible();
  });

  it('dismisses only the failure notice and preserves the editor and its error log', async () => {
    show({ generate_status: 'failed', generate_error: 'phase-1 analysis: upstream error' });
    fireEvent.click(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailDismissFailure' }));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Editor' })).toBeVisible();
    expect(mocks.props.generationLog).toBe('phase-1 analysis: upstream error');
    expect(JSON.parse(localStorage.getItem('workflow_banners_dismissed:draft-test')!)).toContain('failed');
  });

  it('keeps optional generation advice collapsed and preserves the original log', async () => {
    const warning = 'phase3 scenario_scripts failed: temporary provider error';
    show({ generate_warning: warning });
    const details = await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailShowSuggestions' });
    expect(screen.getByRole('status')).toHaveTextContent('selfEvolutionRun.workflowDetailCompletedTitle');
    expect(details).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('selfEvolutionRun.workflowDetailWarningLogHint')).not.toBeVisible();
    expect(screen.queryByText('Request failed')).not.toBeInTheDocument();
    fireEvent.click(details);
    expect(details).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('region', { name: 'selfEvolutionRun.workflowDetailShowSuggestions' })).toHaveAttribute('id', details.getAttribute('aria-controls'));
    expect(screen.getByText('selfEvolutionRun.workflowDetailWarningLogHint')).toBeVisible();
    fireEvent.click(details);
    expect(screen.getByText('selfEvolutionRun.workflowDetailWarningLogHint')).not.toBeVisible();
    expect(mocks.props.generationLog).toContain(warning);
    expect(mocks.generate).not.toHaveBeenCalled();
  });

  it('persists dismissals for the same advice but shows new advice', async () => {
    const first = show({ generate_warning: 'First warning' });
    fireEvent.click(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailDismissNotice' }));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Editor' })).toBeVisible();
    expect(mocks.props.generationLog).toContain('First warning');
    first.unmount();
    const second = show({ generate_warning: 'First warning' });
    await screen.findByRole('region', { name: 'Editor' });
    expect(screen.queryByRole('button', { name: 'selfEvolutionRun.workflowDetailShowSuggestions' })).not.toBeInTheDocument();
    second.unmount();
    show({ generate_warning: 'A different warning' });
    expect(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailShowSuggestions' })).toHaveAttribute('aria-expanded', 'false');
  });

  it('keeps repair failures distinct from optional generation advice', async () => {
    show({ generate_warning: '[修复失败] temporary provider error' });
    expect(await screen.findByRole('alert')).toHaveTextContent('selfEvolutionRun.workflowDetailRepairFailedBanner');
    expect(screen.queryByText('selfEvolutionRun.workflowDetailCompletedTitle')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' }));
    expect(screen.getByRole('region', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' })).toBeVisible();
  });

});
