import { act, cleanup, fireEvent, render as renderComponent, screen, waitFor, within } from '@testing-library/react';
import type { ReactElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { load } from 'js-yaml';
import { ConfigProvider, message } from 'antd';
import NewWorkflowModal from './index';
import type { SkillWorkflowPreflightResponse } from '../../workflowDraftApi';

const api = vi.hoisted(() => ({
  createWorkflowDraft: vi.fn(),
  aiGenerateWorkflowDraft: vi.fn(),
  updateWorkflowDraftContent: vi.fn(),
  preflightSkillWorkflowConversion: vi.fn(),
  listWorkflowDrafts: vi.fn(),
}));
const { listSkillAssetsPage, navigate, translate } = vi.hoisted(() => ({
  listSkillAssetsPage: vi.fn(),
  navigate: vi.fn(),
  translate: (key: string, values?: { name?: string; number?: number; count?: number }) => {
    if (key === 'selfEvolutionRun.newWorkflowSuggestedName') return `${values?.name} Workflow`;
    if (key === 'selfEvolutionRun.newWorkflowSuggestedNameCopy') return `（${values?.number}）`;
    if (key === 'selfEvolutionRun.newWorkflowViewLinked') return `newWorkflowViewLinked ${values?.count}`;
    return key.replace('selfEvolutionRun.', '');
  },
}));

vi.mock('../../workflowDraftApi', () => api);
vi.mock('@/modules/memory/skillApi', () => ({ listSkillAssetsPage }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }));
vi.mock('react-router-dom', async (importOriginal) => ({
  ...await importOriginal<typeof import('react-router-dom')>(),
  useNavigate: () => navigate,
}));

const initialSkill = { id: 'source_skill', name: 'Source Skill' };
const getComputedStyle = window.getComputedStyle.bind(window);
function preflight(skillId = initialSkill.id, status = 'pass'): SkillWorkflowPreflightResponse {
  return { skill_id: skillId, skill_name: skillId, revision_id: 'test_revision', revision_no: 1, tree_hash: 'test_hash', status, summary: `${skillId}: ${status}`, checks: [], file_count: 1, skill_md_len: 160 };
}

function linkedDraft(id: string, name: string, sourceSkillId = initialSkill.id) {
  return { id, name, source_type: 'skill', source_skill_id: sourceSkillId, source_skill_name: sourceSkillId, published: false, published_workflow_ref: '', generate_status: 'done' };
}
type DraftList = { records: ReturnType<typeof linkedDraft>[]; total: number };

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function render(component: ReactElement) {
  return renderComponent(component, {
    // jsdom does not complete CSS animations; retain the real Modal and its visibility semantics.
    wrapper: ({ children }) => <ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider>,
  });
}

function getId() {
  const input = screen.getByPlaceholderText('newWorkflowFieldWorkflowIdPlaceholder');
  const details = input.closest('details');
  if (details && !details.open) fireEvent.click(details.querySelector('summary')!);
  return input;
}
function getName() { return screen.getByRole('textbox', { name: /^newWorkflowFieldDisplayName/ }); }
function createButton() { return screen.getByRole('button', { name: /^(newWorkflowCreateBtn|newWorkflowStartConversion|linkedSkillReconvert)$/ }); }

beforeEach(() => {
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
  vi.spyOn(message, 'warning').mockImplementation(vi.fn());
  api.createWorkflowDraft.mockReset().mockResolvedValue({ id: 'new_draft', version: 4 });
  api.updateWorkflowDraftContent.mockReset().mockResolvedValue({});
  api.aiGenerateWorkflowDraft.mockReset().mockResolvedValue({});
  api.preflightSkillWorkflowConversion.mockReset().mockImplementation(async (id: string) => preflight(id));
  api.listWorkflowDrafts.mockReset().mockResolvedValue({ records: [], total: 0 });
  listSkillAssetsPage.mockReset().mockResolvedValue({ records: [], total: 0 });
  navigate.mockReset();
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('NewWorkflowModal skill pagination', () => {
  function page(first: number, count: number, total: number) {
    return { records: Array.from({ length: count }, (_, index) => ({ id: `skill-${first + index}`, name: `Skill ${first + index}` })), total };
  }

  function openPicker(preset?: typeof initialSkill) {
    // Keep real Select events while exposing all loaded options in jsdom.
    const props = { onCancel: vi.fn(), onCreated: vi.fn() };
    const view = renderComponent(<NewWorkflowModal open initialSkill={preset} {...props} />, {
      wrapper: ({ children }) => <ConfigProvider virtual={false} theme={{ token: { motion: false } }}>{children}</ConfigProvider>,
    });
    if (!preset) fireEvent.click(screen.getByText('newWorkflowModeSkillTitle').closest('button')!);
    const input = screen.getByRole('combobox');
    fireEvent.focus(input);
    fireEvent.mouseDown(input);
    return { ...view, input, props };
  }

  function scrollToBottom() {
    const list = document.querySelector('.rc-virtual-list-holder')!;
    expect(list).not.toBeNull();
    Object.defineProperties(list, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 256 },
    });
    fireEvent.scroll(list, { target: { scrollTop: 744 } });
  }

  it('appends pages on scroll, keeps earlier skills selectable, and stops at the total', async () => {
    listSkillAssetsPage.mockImplementation(async ({ page: number }) => page((number - 1) * 20 + 1, number === 3 ? 5 : 20, 45));
    openPicker();
    await screen.findByRole('option', { name: 'Skill 20', exact: true });
    expect(listSkillAssetsPage).toHaveBeenCalledTimes(1);
    scrollToBottom();
    await screen.findByRole('option', { name: 'Skill 40', exact: true });
    expect(screen.getByRole('option', { name: 'Skill 1', exact: true })).toBeInTheDocument();
    scrollToBottom();
    const last = await screen.findByRole('option', { name: 'Skill 45', exact: true });
    expect(screen.getByText('newWorkflowSkillsAllLoaded')).toBeInTheDocument();
    scrollToBottom();
    expect(listSkillAssetsPage.mock.calls.map(([args]) => [args.page, args.pageSize])).toEqual([[1, 20], [2, 20], [3, 20]]);
    fireEvent.click(last);
    expect(getName()).toHaveValue('Skill 45 Workflow');
    await waitFor(() => expect(api.preflightSkillWorkflowConversion).toHaveBeenCalledWith('skill-45'));
  });

  it('ignores repeated bottom events while loading and deduplicates overlapping pages', async () => {
    const next = deferred<ReturnType<typeof page>>();
    listSkillAssetsPage.mockResolvedValueOnce(page(1, 20, 40)).mockReturnValueOnce(next.promise);
    openPicker();
    await screen.findByRole('option', { name: 'Skill 20', exact: true });
    scrollToBottom();
    scrollToBottom();
    expect(listSkillAssetsPage).toHaveBeenCalledTimes(2);
    await act(async () => { next.resolve(page(20, 20, 40)); });
    expect(screen.getAllByRole('option', { name: 'Skill 20', exact: true })).toHaveLength(1);
    expect(screen.getByRole('option', { name: 'Skill 39', exact: true })).toBeInTheDocument();
    scrollToBottom();
    expect(listSkillAssetsPage).toHaveBeenCalledTimes(2);
  });

  it('resets pagination for searches and clearing, ignoring a late previous page', async () => {
    const oldPage = deferred<ReturnType<typeof page>>();
    listSkillAssetsPage.mockResolvedValueOnce(page(1, 20, 60)).mockReturnValueOnce(oldPage.promise)
      .mockResolvedValueOnce(page(90, 1, 1)).mockResolvedValueOnce(page(1, 20, 60));
    const { input } = openPicker();
    await screen.findByRole('option', { name: 'Skill 20', exact: true });
    scrollToBottom();
    fireEvent.change(input, { target: { value: 'find' } });
    await screen.findByRole('option', { name: 'Skill 90', exact: true });
    await act(async () => { oldPage.resolve(page(21, 20, 60)); });
    expect(screen.queryByRole('option', { name: 'Skill 21', exact: true })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Skill 1', exact: true })).not.toBeInTheDocument();
    fireEvent.change(input, { target: { value: '' } });
    await screen.findByRole('option', { name: 'Skill 1', exact: true });
    expect(listSkillAssetsPage.mock.calls.map(([args]) => [args.keyword, args.page])).toEqual([['', 1], ['', 2], ['find', 1], ['', 1]]);
  });

  it('keeps loaded options after failure and retries the same page', async () => {
    listSkillAssetsPage.mockResolvedValueOnce(page(1, 20, 21)).mockRejectedValueOnce(new Error('test page failure')).mockResolvedValueOnce(page(21, 1, 21));
    openPicker();
    await screen.findByRole('option', { name: 'Skill 20', exact: true });
    scrollToBottom();
    await screen.findByText('newWorkflowSkillLoadFailed');
    expect(screen.getByRole('option', { name: 'Skill 1', exact: true })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    await screen.findByRole('option', { name: 'Skill 21', exact: true });
    expect(listSkillAssetsPage.mock.calls.map(([args]) => args.page)).toEqual([1, 2, 2]);
    expect(screen.queryByText('newWorkflowSkillLoadFailed')).not.toBeInTheDocument();
  });

  it('restores the full list after selecting a search result', async () => {
    listSkillAssetsPage.mockResolvedValueOnce(page(1, 20, 60)).mockResolvedValueOnce(page(90, 1, 1)).mockResolvedValueOnce(page(1, 20, 60));
    const { input } = openPicker();
    await screen.findByRole('option', { name: 'Skill 20', exact: true });
    fireEvent.change(input, { target: { value: 'find' } });
    fireEvent.click(await screen.findByRole('option', { name: 'Skill 90', exact: true }));
    fireEvent.mouseDown(input);
    await screen.findByRole('option', { name: 'Skill 1', exact: true });
    expect(screen.getByRole('option', { name: 'Skill 90', exact: true })).toBeInTheDocument();
    expect(getName()).toHaveValue('Skill 90 Workflow');
    expect(listSkillAssetsPage.mock.calls.map(([args]) => [args.keyword, args.page])).toEqual([['', 1], ['find', 1], ['', 1]]);
  });

  it('retries an initial failure and clearly reports an empty result without loading more', async () => {
    listSkillAssetsPage.mockRejectedValueOnce(new Error('test initial failure')).mockResolvedValueOnce(page(1, 0, 0));
    const { input } = openPicker();
    await screen.findByText('newWorkflowSkillLoadFailed');
    expect(screen.queryByText('newWorkflowSkillsEmpty')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    await screen.findByText('newWorkflowSkillsEmpty');
    expect(screen.queryByRole('button', { name: 'newWorkflowSkillsLoadMore' })).not.toBeInTheDocument();
    fireEvent.keyDown(input, { key: 'Escape', keyCode: 27 });
    fireEvent.mouseDown(input);
    expect(listSkillAssetsPage.mock.calls.map(([args]) => args.page)).toEqual([1, 1]);
  });

  it('loads the first page even with a preset skill and preserves its selected label', async () => {
    listSkillAssetsPage.mockResolvedValue(page(1, 20, 20));
    openPicker(initialSkill);
    await screen.findByRole('option', { name: 'Skill 1', exact: true });
    expect(screen.getByRole('option', { name: 'Source Skill', exact: true })).toBeInTheDocument();
    expect(getName()).toHaveValue('Source Skill Workflow');
    expect(listSkillAssetsPage).toHaveBeenCalledTimes(1);
  });

  it('does not merge a late page after switching creation modes', async () => {
    const pending = deferred<ReturnType<typeof page>>();
    listSkillAssetsPage.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(page(90, 1, 1));
    openPicker();
    fireEvent.click(screen.getByText('newWorkflowModeBlankTitle').closest('button')!);
    await act(async () => { pending.resolve(page(1, 20, 20)); });
    fireEvent.click(screen.getByText('newWorkflowModeSkillTitle').closest('button')!);
    fireEvent.mouseDown(screen.getByRole('combobox'));
    await screen.findByRole('option', { name: 'Skill 90', exact: true });
    expect(screen.queryByRole('option', { name: 'Skill 1', exact: true })).not.toBeInTheDocument();
  });
});

describe('NewWorkflowModal initial skill', () => {
  it('suggests a stable new ID for each initial-skill session and only performs reads', async () => {
    const props = { onCancel: vi.fn(), onCreated: vi.fn() };
    const { rerender } = render(<NewWorkflowModal open initialSkill={initialSkill} {...props} />);
    expect(screen.getByText('Source Skill')).toBeInTheDocument();
    expect(screen.getByText('newWorkflowModeSkillTitle').closest('button')).toHaveClass('npm-mode-card--active');
    const suggestedId = (getId() as HTMLInputElement).value;
    expect(suggestedId).toMatch(/^source-skill-[a-f0-9]{8}$/);
    expect(getName()).toHaveValue('Source Skill Workflow');
    expect(screen.queryByPlaceholderText('newWorkflowAiPlaceholder')).not.toBeInTheDocument();
    await waitFor(() => expect(api.preflightSkillWorkflowConversion).toHaveBeenCalledWith('source_skill'));
    expect(api.listWorkflowDrafts).toHaveBeenCalledWith(expect.objectContaining({ page: 1, pageSize: 100 }));
    rerender(<NewWorkflowModal open initialSkill={{ ...initialSkill }} {...props} />);
    expect(getId()).toHaveValue(suggestedId);
    rerender(<NewWorkflowModal open initialSkill={{ id: 'chinese_skill', name: '中文技能助手' }} {...props} />);
    expect((getId() as HTMLInputElement).value).toMatch(/^workflow-[a-f0-9]{8}$/);
    expect(getName()).toHaveValue('中文技能 Workflow');
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
    expect(api.listWorkflowDrafts).not.toHaveBeenCalled();
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
    expect(getName()).toHaveValue('Source Skill Workflow');

    rerender(<NewWorkflowModal open initialSkill={{ id: 'another_skill', name: 'Another Skill' }} onCancel={onCancel} onCreated={onCreated} />);
    expect((getId() as HTMLInputElement).value).toMatch(/^another-skill-[a-f0-9]{8}$/);
    expect(getName()).toHaveValue('Another Skill Workflow');
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
    expect(getName()).toHaveValue('Renamed Skill Workflow');
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
    expect((getId() as HTMLInputElement).value).toMatch(/^new-option-[a-f0-9]{8}$/);
    expect(getName()).toHaveValue('New option Workflow');
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
    expect(api.createWorkflowDraft).toHaveBeenCalledWith({ name: 'Source Skill Workflow', source_type: 'skill' });
    expect(api.updateWorkflowDraftContent).toHaveBeenCalledTimes(1);
    expect(api.updateWorkflowDraftContent).toHaveBeenCalledWith('new_draft', expect.objectContaining({ version: 4 }));
    const content = api.updateWorkflowDraftContent.mock.calls[0][1].workflow_yaml_content as string;
    expect(load(content)).toMatchObject({ id: suggestedId, name: 'Source Skill Workflow' });
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
    expect(getName()).toHaveValue('New Skill Workflow');
  });

  it('keeps blank mode creating an empty draft with the workflow ID as its fallback name', async () => {
    const onCreated = vi.fn();
    render(<NewWorkflowModal open onCancel={vi.fn()} onCreated={onCreated} />);
    fireEvent.click(screen.getByText('newWorkflowModeBlankTitle').closest('button')!);
    fireEvent.change(getId(), { target: { value: 'blank-workflow' } });
    fireEvent.click(screen.getByRole('button', { name: 'newWorkflowCreateBtn' }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('new_draft'));
    expect(api.createWorkflowDraft).toHaveBeenCalledWith({ name: 'blank-workflow', source_type: 'blank' });
    expect(api.updateWorkflowDraftContent).toHaveBeenCalledWith('new_draft', expect.objectContaining({ version: 4 }));
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
    expect(api.preflightSkillWorkflowConversion).not.toHaveBeenCalled();
    expect(api.listWorkflowDrafts).not.toHaveBeenCalled();
  });

  it.each([
    ['empty', ''],
    ['whitespace', '   '],
    ['over 60 characters', 'A'.repeat(61)],
  ])('blocks a Skill workflow name that is %s, including submission with Enter', async (_label, value) => {
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={vi.fn()} />);
    await waitFor(() => expect(createButton()).toBeEnabled());
    expect(getName()).toHaveAttribute('maxlength', '60');
    fireEvent.change(getName(), { target: { value } });
    expect(createButton()).toBeDisabled();
    fireEvent.keyDown(getName(), { key: 'Enter', keyCode: 13 });
    fireEvent.keyDown(getId(), { key: 'Enter', keyCode: 13 });
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.updateWorkflowDraftContent).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
  });

  it('shows an existing unpublished linked draft using its real draft ID and suggests a distinct name', async () => {
    api.listWorkflowDrafts.mockResolvedValue({
      records: [linkedDraft('actual_draft_uuid', 'Source Skill Workflow')], total: 1,
    });
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByText('newWorkflowAlreadyLinked');
    expect(getName()).toHaveValue('Source Skill Workflow（2）');
    expect(screen.getByRole('link')).toHaveAttribute('href', '/memory-management/workflows/actual_draft_uuid');
    expect(screen.getByRole('button', { name: 'linkedSkillReconvert' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'newWorkflowStartConversion' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('link'));
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith('/memory-management/workflows/actual_draft_uuid');
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.updateWorkflowDraftContent).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
  });

  it('waits for linked drafts before allowing conversion and uses the resolved duplicate-name suffix', async () => {
    const pendingList = deferred<DraftList>();
    api.listWorkflowDrafts.mockReturnValue(pendingList.promise);
    const onCreated = vi.fn();
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={onCreated} />);
    await screen.findByText('source_skill: pass');
    expect(getName()).toHaveValue('Source Skill Workflow');
    expect(createButton()).toBeDisabled();
    fireEvent.keyDown(getName(), { key: 'Enter', keyCode: 13 });
    fireEvent.keyDown(getId(), { key: 'Enter', keyCode: 13 });
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.updateWorkflowDraftContent).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();

    await act(async () => {
      pendingList.resolve({ records: [linkedDraft('existing_draft', 'Source Skill Workflow')], total: 1 });
    });
    expect(getName()).toHaveValue('Source Skill Workflow（2）');
    expect(createButton()).toBeEnabled();
    fireEvent.click(createButton());
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('new_draft'));
    expect(api.createWorkflowDraft).toHaveBeenCalledTimes(1);
    expect(api.createWorkflowDraft).toHaveBeenCalledWith({ name: 'Source Skill Workflow（2）', source_type: 'skill' });
    const content = api.updateWorkflowDraftContent.mock.calls[0][1].workflow_yaml_content as string;
    expect(load(content)).toMatchObject({ name: 'Source Skill Workflow（2）' });
    expect(api.aiGenerateWorkflowDraft).toHaveBeenCalledWith('new_draft', { skill_id: 'source_skill' });
  });

  it('reads all draft pages before deriving linked workflows and duplicate-name suffixes', async () => {
    const unrelated = Array.from({ length: 99 }, (_, index) => linkedDraft(`other_${index}`, `Other ${index}`, 'another_skill'));
    api.listWorkflowDrafts.mockImplementation(({ page }: { page: number }) => Promise.resolve({
      records: page === 1
        ? [...unrelated, linkedDraft('linked_first', 'Source Skill Workflow')]
        : [linkedDraft('linked_second', 'Source Skill Workflow（2）')],
      total: 101,
    }));
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={vi.fn()} />);
    await waitFor(() => expect(getName()).toHaveValue('Source Skill Workflow（3）'));
    expect(api.listWorkflowDrafts).toHaveBeenCalledTimes(2);
    expect(api.listWorkflowDrafts).toHaveBeenNthCalledWith(1, { page: 1, pageSize: 100 });
    expect(api.listWorkflowDrafts).toHaveBeenNthCalledWith(2, { page: 2, pageSize: 100 });
    fireEvent.click(screen.getByRole('button', { name: 'newWorkflowViewLinked 2' }));
    expect(screen.getByRole('link', { name: 'Source Skill Workflow' })).toHaveAttribute('href', '/memory-management/workflows/linked_first');
    expect(screen.getByRole('link', { name: 'Source Skill Workflow（2）' })).toHaveAttribute('href', '/memory-management/workflows/linked_second');
    expect(screen.queryByRole('link', { name: 'Other 0' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('link', { name: 'Source Skill Workflow（2）' }));
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith('/memory-management/workflows/linked_second');
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.updateWorkflowDraftContent).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
  });

  it('ignores an old draft-list response after selecting another skill', async () => {
    const previousList = deferred<DraftList>();
    api.listWorkflowDrafts.mockReturnValueOnce(previousList.promise).mockResolvedValue({ records: [], total: 0 });
    listSkillAssetsPage.mockResolvedValue({ records: [{ id: 'new_skill', name: 'New Skill' }], total: 1 });
    const props = { onCancel: vi.fn(), onCreated: vi.fn() };
    render(<NewWorkflowModal open initialSkill={initialSkill} {...props} />);
    await waitFor(() => expect(api.listWorkflowDrafts).toHaveBeenCalledTimes(1));
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'New' } });
    fireEvent.click(await screen.findByText('New Skill'));
    await waitFor(() => expect(api.listWorkflowDrafts).toHaveBeenCalledTimes(2));
    await screen.findByText('new_skill: pass');
    await act(async () => {
      previousList.resolve({ records: [linkedDraft('stale_draft', 'New Skill Workflow', 'new_skill')], total: 1 });
    });
    expect(getName()).toHaveValue('New Skill Workflow');
    expect(screen.queryByText('newWorkflowAlreadyLinked')).not.toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'newWorkflowStartConversion' })).toBeEnabled();
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
  });

  it('preserves a name the user entered while the draft list was loading', async () => {
    const pendingList = deferred<DraftList>();
    api.listWorkflowDrafts.mockReturnValue(pendingList.promise);
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={vi.fn()} />);
    fireEvent.change(getName(), { target: { value: 'My chosen workflow' } });
    await act(async () => {
      pendingList.resolve({ records: [linkedDraft('linked_draft', 'Source Skill Workflow')], total: 1 });
    });
    await screen.findByText('newWorkflowAlreadyLinked');
    expect(getName()).toHaveValue('My chosen workflow');
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
  });

  it('shows real preflight warnings, toggles their details, and still permits conversion', async () => {
    api.preflightSkillWorkflowConversion.mockResolvedValue({
      ...preflight(initialSkill.id, 'warning'),
      summary: 'A referenced resource needs attention.',
      checks: [{
        code: 'DEPENDENCY_RESOURCE_MISSING', severity: 'warning',
        message: 'The referenced file is missing.', path: 'references/input.md',
        suggestion: 'Review this reference after conversion.',
      }],
    });
    const onCreated = vi.fn();
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={onCreated} />);
    await waitFor(() => expect(screen.getByText('A referenced resource needs attention.')).toBeVisible());
    expect(createButton()).toBeEnabled();
    expect(screen.getByText('The referenced file is missing.')).toBeVisible();
    expect(screen.queryByRole('region', { name: 'newWorkflowCheckDetails' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /^newWorkflowShowChecks/ }));
    const details = screen.getByRole('region', { name: 'newWorkflowCheckDetails' });
    expect(within(details).getByText('The referenced file is missing.')).toBeVisible();
    expect(within(details).getByText('Review this reference after conversion.')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /^newWorkflowHideChecks/ }));
    expect(details).not.toBeVisible();
    expect(screen.getByRole('button', { name: /^newWorkflowShowChecks/ })).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(createButton());
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('new_draft'));
    expect(api.aiGenerateWorkflowDraft).toHaveBeenCalledWith('new_draft', { skill_id: 'source_skill' });
  });

  it('keeps a blocked response with missing optional summary and count fields visible and non-submittable', async () => {
    api.preflightSkillWorkflowConversion.mockResolvedValue({
      skill_id: initialSkill.id, status: 'blocked',
      checks: [{ code: 'SKILL_NOT_AVAILABLE', severity: 'error', message: 'The selected Skill is unavailable.', suggestion: 'Choose a published Skill.' }],
    });
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={vi.fn()} />);
    await waitFor(() => expect(screen.getByText('The selected Skill is unavailable.')).toBeVisible());
    expect(createButton()).toBeDisabled();
    fireEvent.keyDown(getName(), { key: 'Enter', keyCode: 13 });
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
  });

  it('accepts a successful preflight with null checks and keeps details collapsed initially', async () => {
    api.preflightSkillWorkflowConversion.mockResolvedValue({
      ...preflight(), summary: 'The selected Skill is ready for conversion.', checks: null,
    });
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={vi.fn()} />);
    await waitFor(() => expect(screen.getByText('newWorkflowPreflightPassedTitle')).toBeVisible());
    expect(screen.getByText('The selected Skill is ready for conversion.')).toBeVisible();
    expect(screen.queryByRole('region', { name: 'newWorkflowCheckDetails' })).not.toBeInTheDocument();
    const showChecks = screen.getByRole('button', { name: /^newWorkflowShowChecks/ });
    expect(showChecks).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(showChecks);
    expect(screen.getByRole('button', { name: /^newWorkflowHideChecks/ })).toHaveAttribute('aria-expanded', 'true');
    const details = screen.getByRole('region', { name: 'newWorkflowCheckDetails' });
    expect(within(details).getByText('The selected Skill is ready for conversion.')).toBeVisible();
    expect(createButton()).toBeEnabled();
  });

  it('offers a retry after preflight transport failure while preserving the option to convert', async () => {
    const retryResult = deferred<SkillWorkflowPreflightResponse>();
    api.preflightSkillWorkflowConversion.mockRejectedValueOnce(new Error('test preflight unavailable')).mockReturnValueOnce(retryResult.promise);
    const onCreated = vi.fn();
    render(<NewWorkflowModal open initialSkill={initialSkill} onCancel={vi.fn()} onCreated={onCreated} />);
    await screen.findByText('newWorkflowPreflightFailed');
    expect(createButton()).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    await waitFor(() => expect(api.preflightSkillWorkflowConversion).toHaveBeenCalledTimes(2));
    expect(api.preflightSkillWorkflowConversion).toHaveBeenNthCalledWith(2, initialSkill.id);
    expect(createButton()).toBeDisabled();
    await act(async () => { retryResult.resolve(preflight()); });
    await screen.findByText('source_skill: pass');
    expect(screen.queryByText('newWorkflowPreflightFailed')).not.toBeInTheDocument();
    fireEvent.click(createButton());
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('new_draft'));
    expect(api.aiGenerateWorkflowDraft).toHaveBeenCalledWith('new_draft', { skill_id: 'source_skill' });
  });
});
