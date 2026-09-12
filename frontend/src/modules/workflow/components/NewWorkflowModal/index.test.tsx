import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { load } from 'js-yaml';
import { message } from 'antd';
import NewWorkflowModal from './index';
import type { SkillWorkflowPreflightResponse } from '../../workflowDraftApi';

const api = vi.hoisted(() => ({
  createWorkflowDraft: vi.fn(),
  aiGenerateWorkflowDraft: vi.fn(),
  updateWorkflowDraftContent: vi.fn(),
  preflightSkillWorkflowConversion: vi.fn(),
}));
const { listSkillAssetsPage, translate } = vi.hoisted(() => ({
  listSkillAssetsPage: vi.fn(),
  translate: (key: string) => key.replace('selfEvolutionRun.', ''),
}));

vi.mock('../../workflowDraftApi', () => api);
vi.mock('@/modules/memory/skillApi', () => ({ listSkillAssetsPage }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }));

const initialSkill = { id: 'source_skill', name: 'Source Skill' };
const getComputedStyle = window.getComputedStyle.bind(window);
function preflight(skillId = initialSkill.id, status = 'pass'): SkillWorkflowPreflightResponse {
  return { skill_id: skillId, skill_name: skillId, revision_id: 'test_revision', revision_no: 1, tree_hash: 'test_hash', status, summary: `${skillId}: ${status}`, checks: [], file_count: 1, skill_md_len: 30 };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function getId() { return screen.getByPlaceholderText('newWorkflowFieldWorkflowIdPlaceholder'); }
function getName() { return screen.getByPlaceholderText('newWorkflowFieldDisplayNamePlaceholderWithId'); }
function createButton() { return screen.getByRole('button', { name: 'newWorkflowCreateBtn' }); }

beforeEach(() => {
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
  vi.spyOn(message, 'warning').mockImplementation(vi.fn());
  api.createWorkflowDraft.mockReset().mockResolvedValue({ id: 'new_draft', version: 4 });
  api.updateWorkflowDraftContent.mockReset().mockResolvedValue({});
  api.aiGenerateWorkflowDraft.mockReset().mockResolvedValue({});
  api.preflightSkillWorkflowConversion.mockReset().mockImplementation(async (id: string) => preflight(id));
  listSkillAssetsPage.mockReset().mockResolvedValue({ records: [], total: 0 });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('NewWorkflowModal initial skill', () => {
  it('suggests a stable new ID for each initial-skill session and only runs preflight', async () => {
    const props = { onCancel: vi.fn(), onCreated: vi.fn() };
    const { rerender } = render(<NewWorkflowModal open initialSkill={initialSkill} {...props} />);
    expect(screen.getByText('Source Skill')).toBeInTheDocument();
    expect(screen.getByText('newWorkflowModeSkillTitle').closest('button')).toHaveClass('npm-mode-card--active');
    const suggestedId = (getId() as HTMLInputElement).value;
    expect(suggestedId).toMatch(/^source-skill-[a-f0-9]{8}$/);
    expect(getName()).toHaveValue('Source Skill');
    expect(screen.queryByPlaceholderText('newWorkflowAiPlaceholder')).not.toBeInTheDocument();
    await waitFor(() => expect(api.preflightSkillWorkflowConversion).toHaveBeenCalledWith('source_skill'));
    rerender(<NewWorkflowModal open initialSkill={{ ...initialSkill }} {...props} />);
    expect(getId()).toHaveValue(suggestedId);
    rerender(<NewWorkflowModal open initialSkill={{ id: 'chinese_skill', name: '中文技能' }} {...props} />);
    expect((getId() as HTMLInputElement).value).toMatch(/^workflow-[a-f0-9]{8}$/);
    expect(getName()).toHaveValue('中文技能');
    await screen.findByText('chinese_skill: pass');
    rerender(<NewWorkflowModal open initialSkill={{ id: 'numeric_skill', name: '123 Tasks' }} {...props} />);
    expect((getId() as HTMLInputElement).value).toMatch(/^workflow-123-tasks-[a-f0-9]{8}$/);
    await screen.findByText('numeric_skill: pass');
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.updateWorkflowDraftContent).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
  });

  it('keeps the normal entry in AI mode and creates with the existing description path', async () => {
    const onCreated = vi.fn();
    render(<NewWorkflowModal open onCancel={vi.fn()} onCreated={onCreated} />);
    expect(screen.getByText('newWorkflowModeAiTitle').closest('button')).toHaveClass('npm-mode-card--active');
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(api.preflightSkillWorkflowConversion).not.toHaveBeenCalled();
    fireEvent.change(getId(), { target: { value: 'new-ai-workflow' } });
    fireEvent.change(screen.getByPlaceholderText('newWorkflowAiPlaceholder'), { target: { value: 'Create a reporting workflow' } });
    fireEvent.click(createButton());
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('new_draft'));
    expect(api.createWorkflowDraft).toHaveBeenCalledWith({ name: 'new-ai-workflow', source_type: 'ai' });
    expect(api.aiGenerateWorkflowDraft).toHaveBeenCalledWith('new_draft', { description: 'Create a reporting workflow' });
  });

  it('resets edited fields after close/reopen and when the initial skill changes', async () => {
    const onCancel = vi.fn();
    const onCreated = vi.fn();
    const { rerender } = render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={onCancel} onCreated={onCreated} />);
    await screen.findByText('source_skill: pass');
    const originalSuggestedId = (getId() as HTMLInputElement).value;
    fireEvent.change(getId(), { target: { value: 'temporary-id' } });
    fireEvent.change(getName(), { target: { value: 'Temporary name' } });
    fireEvent.click(screen.getByRole('button', { name: 'newWorkflowCancelBtn' }));
    expect(onCancel).toHaveBeenCalledOnce();
    rerender(<NewWorkflowModal open={false} initialSkill={initialSkill} onCancel={onCancel} onCreated={onCreated} />);
    rerender(<NewWorkflowModal open initialSkill={initialSkill} onCancel={onCancel} onCreated={onCreated} />);
    expect((getId() as HTMLInputElement).value).toMatch(/^source-skill-[a-f0-9]{8}$/);
    expect(getId()).not.toHaveValue(originalSuggestedId);
    expect(getName()).toHaveValue('Source Skill');

    rerender(<NewWorkflowModal open initialSkill={{ id: 'another_skill', name: 'Another Skill' }} onCancel={onCancel} onCreated={onCreated} />);
    expect((getId() as HTMLInputElement).value).toMatch(/^another-skill-[a-f0-9]{8}$/);
    expect(getName()).toHaveValue('Another Skill');
    await screen.findByText('another_skill: pass');
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
  });

  it('ignores a late preflight result after closing and opening with another skill', async () => {
    const pending = deferred<SkillWorkflowPreflightResponse>();
    api.preflightSkillWorkflowConversion.mockImplementation((id: string) => id === initialSkill.id ? pending.promise : Promise.resolve(preflight(id)));
    const props = { onCancel: vi.fn(), onCreated: vi.fn() };
    const { rerender } = render(<NewWorkflowModal open initialSkill={initialSkill} {...props} />);
    expect(createButton()).toBeDisabled();
    rerender(<NewWorkflowModal open={false} initialSkill={initialSkill} {...props} />);
    rerender(<NewWorkflowModal open initialSkill={{ id: 'new_skill', name: 'New Skill' }} {...props} />);
    await screen.findByText('new_skill: pass');
    await act(async () => { pending.resolve(preflight(initialSkill.id, 'blocked')); });
    expect(screen.queryByText('source_skill: blocked')).not.toBeInTheDocument();
    expect(createButton()).toBeEnabled();
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
  });

  it('refreshes the initial name without losing preflight when the skill ID stays the same', async () => {
    const props = { onCancel: vi.fn(), onCreated: vi.fn() };
    const { rerender } = render(<NewWorkflowModal open initialSkill={initialSkill} {...props} />);
    await screen.findByText('source_skill: pass');
    rerender(<NewWorkflowModal open initialSkill={{ id: initialSkill.id, name: 'Renamed Skill' }} {...props} />);
    expect((getId() as HTMLInputElement).value).toMatch(/^renamed-skill-[a-f0-9]{8}$/);
    expect(getName()).toHaveValue('Renamed Skill');
    await waitFor(() => expect(createButton()).toBeEnabled());
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
  });

  it('ignores a late skill search after the dialog session changes', async () => {
    const oldSearch = deferred<{ records: { id: string; name: string }[]; total: number }>();
    listSkillAssetsPage.mockImplementation(({ keyword }: { keyword: string }) => keyword === 'old'
      ? oldSearch.promise
      : Promise.resolve({ records: [{ id: 'new_option', name: 'New option' }], total: 1 }));
    const props = { onCancel: vi.fn(), onCreated: vi.fn() };
    const { rerender } = render(<NewWorkflowModal open initialSkill={initialSkill} {...props} />);
    await act(async () => { fireEvent.change(screen.getByRole('combobox'), { target: { value: 'old' } }); });
    expect(listSkillAssetsPage).toHaveBeenCalledWith(expect.objectContaining({ keyword: 'old' }));
    rerender(<NewWorkflowModal open={false} initialSkill={initialSkill} {...props} />);
    rerender(<NewWorkflowModal open initialSkill={{ id: 'new_skill', name: 'New Skill' }} {...props} />);
    await act(async () => { fireEvent.change(screen.getByRole('combobox'), { target: { value: 'new' } }); });
    await screen.findByText('New option');
    await act(async () => { oldSearch.resolve({ records: [{ id: 'old_option', name: 'Old option' }], total: 1 }); });
    expect(screen.queryByText('Old option')).not.toBeInTheDocument();
    expect(screen.getByText('New option')).toBeInTheDocument();
    expect(screen.getAllByText('New Skill').length).toBeGreaterThan(0);
    await act(async () => { fireEvent.click(screen.getByText('New option')); });
    expect(getId()).toHaveValue('new-option');
  });

  it('blocks creation after a blocked preflight, including submission with Enter', async () => {
    api.preflightSkillWorkflowConversion.mockResolvedValue(preflight(initialSkill.id, 'blocked'));
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByText('source_skill: blocked');
    expect(createButton()).toBeDisabled();
    fireEvent.keyDown(getId(), { key: 'Enter', keyCode: 13 });
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
  });

  it('creates a new draft, updates only its content, and generates from the selected skill ID', async () => {
    const onCreated = vi.fn();
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={onCreated} />);
    await waitFor(() => expect(createButton()).toBeEnabled());
    const suggestedId = (getId() as HTMLInputElement).value;
    expect(suggestedId).toMatch(/^source-skill-[a-f0-9]{8}$/);
    fireEvent.click(createButton());
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('new_draft'));
    expect(api.createWorkflowDraft).toHaveBeenCalledTimes(1);
    expect(api.createWorkflowDraft).toHaveBeenCalledWith({ name: 'Source Skill', source_type: 'skill' });
    expect(api.updateWorkflowDraftContent).toHaveBeenCalledTimes(1);
    expect(api.updateWorkflowDraftContent).toHaveBeenCalledWith('new_draft', expect.objectContaining({ version: 4 }));
    const content = api.updateWorkflowDraftContent.mock.calls[0][1].workflow_yaml_content as string;
    expect(load(content)).toMatchObject({ id: suggestedId, name: 'Source Skill' });
    expect(api.aiGenerateWorkflowDraft).toHaveBeenCalledWith('new_draft', { skill_id: 'source_skill' });
  });

  it('keeps invalid workflow IDs blocked and does not bypass the existing validation', async () => {
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByText('source_skill: pass');
    fireEvent.change(getId(), { target: { value: '123 invalid' } });
    expect(screen.getByText('newWorkflowIdErrorInvalid')).toBeInTheDocument();
    expect(createButton()).toBeDisabled();
    fireEvent.keyDown(getId(), { key: 'Enter', keyCode: 13 });
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
  });

  it('stops generation when saving the new workflow is rejected by existing validation', async () => {
    api.updateWorkflowDraftContent.mockRejectedValueOnce(new Error('test workflow ID conflict'));
    const onCreated = vi.fn();
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={onCreated} />);
    await waitFor(() => expect(createButton()).toBeEnabled());
    fireEvent.click(createButton());
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('new_draft'));
    expect(api.updateWorkflowDraftContent).toHaveBeenCalledWith('new_draft', expect.anything());
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
    expect(message.warning).toHaveBeenCalledWith('workflowDetailFailedBanner');
  });

  it('stops pending creation from changing a later dialog session', async () => {
    const pendingCreate = deferred<{ id: string; version: number }>();
    api.createWorkflowDraft.mockReturnValue(pendingCreate.promise);
    const props = { onCancel: vi.fn(), onCreated: vi.fn() };
    const { rerender } = render(<NewWorkflowModal open initialSkill={initialSkill} {...props} />);
    await waitFor(() => expect(createButton()).toBeEnabled());
    fireEvent.click(createButton());
    rerender(<NewWorkflowModal open={false} initialSkill={initialSkill} {...props} />);
    rerender(<NewWorkflowModal open initialSkill={{ id: 'new_skill', name: 'New Skill' }} {...props} />);
    await act(async () => { pendingCreate.resolve({ id: 'cancelled_draft', version: 1 }); });
    expect(api.updateWorkflowDraftContent).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
    expect(props.onCreated).not.toHaveBeenCalled();
    expect(getName()).toHaveValue('New Skill');
  });
});
