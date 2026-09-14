import type { ComponentProps } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SkillAssetRecord } from '@/modules/memory/skillApi';
import type { SkillLinkedWorkflow, SkillLinkedWorkflowsResponse } from '../../workflowDraftApi';
import type NewWorkflowModal from '../NewWorkflowModal';
import LinkedSkillButton from './index';

const api = vi.hoisted(() => ({
  getSkillAssetDetail: vi.fn(),
  listBuiltinSkills: vi.fn(),
  listSkillLinkedWorkflows: vi.fn(),
  createWorkflowDraft: vi.fn(),
  updateWorkflowDraftContent: vi.fn(),
  aiGenerateWorkflowDraft: vi.fn(),
}));

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/modules/memory/skillApi', () => ({
  getSkillAssetDetail: api.getSkillAssetDetail,
  listBuiltinSkills: api.listBuiltinSkills,
}));
vi.mock('../../workflowDraftApi', () => ({
  listSkillLinkedWorkflows: api.listSkillLinkedWorkflows,
  createWorkflowDraft: api.createWorkflowDraft,
  updateWorkflowDraftContent: api.updateWorkflowDraftContent,
  aiGenerateWorkflowDraft: api.aiGenerateWorkflowDraft,
}));
vi.mock('../NewWorkflowModal', () => ({
  default: ({ open, initialSkill, onCancel, onCreated }: ComponentProps<typeof NewWorkflowModal>) => open && (
    <div role="dialog" aria-label="New workflow">
      <span data-testid="conversion-skill-id">{initialSkill?.id}</span>
      <span>{initialSkill?.name}</span>
      <button type="button" onClick={onCancel}>Cancel conversion</button>
      <button type="button" onClick={() => onCreated('new-workflow-draft')}>Finish conversion</button>
    </div>
  ),
}));

const skill: SkillAssetRecord = {
  id: 'skill-report',
  skillId: 'report',
  name: 'Report skill',
  skillName: 'Report skill',
  description: 'Summarize supplied source material.',
  category: 'Writing',
  tags: [],
  content: '',
  headRevisionId: 'skill-revision-2',
  draft: { hasUncommittedDraft: false, taskId: '', version: 0 },
  autoEvo: false,
  isEnabled: true,
};

const draft: ComponentProps<typeof LinkedSkillButton>['draft'] = {
  id: 'current-draft',
  name: 'Current report workflow',
  source_skill_id: 'skill-report',
  source_skill_name: 'Old source name',
  published: false,
  published_workflow_ref: '',
};

function linkedWorkflow(overrides: Partial<SkillLinkedWorkflow> = {}): SkillLinkedWorkflow {
  return {
    workflow_ref: 'user:another-report',
    workflow_id: 'another-report',
    name: 'Another report workflow',
    description: '',
    when_to_use: '',
    status: 'published',
    enabled: true,
    call_mode: 'auto',
    revision_id: 'workflow-revision-1',
    revision_no: 1,
    tree_hash: 'test-workflow-hash',
    source_skill_id: skill.id,
    source_skill_name: skill.name,
    source_skill_revision_id: skill.headRevisionId,
    source_skill_revision_no: 2,
    source_skill_tree_hash: 'test-skill-hash',
    current_skill_revision_id: skill.headRevisionId,
    current_skill_revision_no: 2,
    current_skill_tree_hash: 'test-skill-hash',
    source_is_current: true,
    available: true,
    unavailable_reason: '',
    ...overrides,
  };
}

const links = (workflows: SkillLinkedWorkflow[] = []): SkillLinkedWorkflowsResponse => ({
  skill_id: skill.id,
  skill_name: skill.name,
  workflows,
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

const openDetails = () => fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.linkedSkillButton' }));
const reconvertButton = () => screen.getByRole('button', { name: /selfEvolutionRun.linkedSkillReconvert$/ });
const detailDialog = () => screen.getByRole('dialog', { name: 'admin.memorySkillDetailTitle' });

beforeEach(() => {
  vi.resetAllMocks();
  api.getSkillAssetDetail.mockResolvedValue(skill);
  api.listSkillLinkedWorkflows.mockResolvedValue(links());
  api.listBuiltinSkills.mockResolvedValue([]);
});

afterEach(cleanup);

describe('LinkedSkillButton', () => {
  it('hides itself and makes no requests when there is no source Skill', () => {
    render(<LinkedSkillButton draft={{ ...draft, source_skill_id: '' }} onCreated={vi.fn()} />);

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(api.getSkillAssetDetail).not.toHaveBeenCalled();
    expect(api.listSkillLinkedWorkflows).not.toHaveBeenCalled();
    expect(api.listBuiltinSkills).not.toHaveBeenCalled();
  });

  it('loads metadata only after opening and displays the live name, description, category, and unpublished draft', async () => {
    const loading = deferred<SkillAssetRecord>();
    api.getSkillAssetDetail.mockReturnValueOnce(loading.promise);
    api.listSkillLinkedWorkflows.mockResolvedValue(links([linkedWorkflow()]));
    render(<LinkedSkillButton draft={draft} onCreated={vi.fn()} />);

    expect(api.getSkillAssetDetail).not.toHaveBeenCalled();
    expect(api.listSkillLinkedWorkflows).not.toHaveBeenCalled();
    openDetails();
    expect(api.getSkillAssetDetail).toHaveBeenCalledWith('skill-report', { loadContent: false });
    expect(api.listSkillLinkedWorkflows).toHaveBeenCalledWith('skill-report');
    expect(screen.getByRole('status')).toHaveTextContent('common.loading');
    expect(screen.queryByRole('button', { name: /selfEvolutionRun.linkedSkillReconvert$/ })).not.toBeInTheDocument();

    await act(async () => { loading.resolve(skill); });
    const dialog = detailDialog();
    expect(within(dialog).getByText(skill.name)).toBeInTheDocument();
    expect(within(dialog).getByText(skill.description)).toBeInTheDocument();
    expect(within(dialog).getByText(skill.category)).toBeInTheDocument();
    expect(within(dialog).queryByText(draft.source_skill_name)).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: draft.name })).toBeInTheDocument();
    expect(within(dialog).getByText('selfEvolutionRun.linkedSkillDraft')).toBeInTheDocument();
    expect(within(dialog).getByText('Another report workflow')).toBeInTheDocument();
    expect(api.listBuiltinSkills).not.toHaveBeenCalled();
  });

  it.each([
    { isEnabled: true, deletedAt: undefined, status: 'linkedSkillEnabled' },
    { isEnabled: false, deletedAt: undefined, status: 'linkedSkillDisabled' },
    { isEnabled: false, deletedAt: '2026-09-11T08:00:00Z', status: 'linkedSkillDeleted' },
  ])('shows the source Skill status $status', async ({ isEnabled, deletedAt, status }) => {
    api.getSkillAssetDetail.mockResolvedValue({ ...skill, isEnabled, deletedAt });
    render(<LinkedSkillButton draft={draft} onCreated={vi.fn()} />);
    openDetails();

    expect(await screen.findByText(`selfEvolutionRun.${status}`)).toBeInTheDocument();
    if (deletedAt) expect(reconvertButton()).toBeDisabled();
  });

  it('lists the published current workflow once and preserves each linked workflow availability', async () => {
    const publishedRef = 'user:published-report';
    api.listSkillLinkedWorkflows.mockResolvedValue(links([
      linkedWorkflow({ workflow_ref: publishedRef, name: draft.name }),
      linkedWorkflow({ workflow_ref: 'user:paused', name: 'Paused workflow', enabled: false }),
      linkedWorkflow({ workflow_ref: 'user:unavailable', name: 'Unavailable workflow', available: false }),
    ]));
    render(<LinkedSkillButton draft={{ ...draft, published: true, published_workflow_ref: publishedRef }} onCreated={vi.fn()} />);
    openDetails();
    await screen.findByText(skill.name);

    const dialog = detailDialog();
    expect(within(dialog).getAllByText(draft.name)).toHaveLength(1);
    expect(within(dialog).queryByText('selfEvolutionRun.linkedSkillPublished')).not.toBeInTheDocument();
    expect(within(dialog).getByText('Paused workflow').parentElement).toHaveTextContent('selfEvolutionRun.linkedSkillDisabled');
    expect(within(dialog).getByText('Unavailable workflow').parentElement).toHaveTextContent('selfEvolutionRun.linkedSkillUnavailable');
    expect(within(dialog).getByRole('button', { name: draft.name }).parentElement).toHaveTextContent('selfEvolutionRun.linkedSkillEnabled');
  });

  it.each(['request rejection', 'missing Skill'] as const)('offers retry without conversion after %s', async (failure) => {
    if (failure === 'request rejection') api.getSkillAssetDetail.mockRejectedValueOnce(new Error('private provider response'));
    else api.getSkillAssetDetail.mockResolvedValueOnce(null);
    render(<LinkedSkillButton draft={draft} onCreated={vi.fn()} />);
    openDetails();

    expect(await screen.findByText('selfEvolutionRun.linkedSkillLoadFailed')).toBeInTheDocument();
    expect(screen.queryByText('private provider response')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /selfEvolutionRun.linkedSkillReconvert$/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    expect(await screen.findByText(skill.name)).toBeInTheDocument();
    expect(api.getSkillAssetDetail).toHaveBeenCalledTimes(2);
    expect(api.listSkillLinkedWorkflows).toHaveBeenCalledTimes(2);
    expect(reconvertButton()).toBeEnabled();
  });

  it('also treats the linked-workflow request failure as retryable', async () => {
    api.listSkillLinkedWorkflows.mockRejectedValueOnce(new Error('linked workflows unavailable'));
    render(<LinkedSkillButton draft={draft} onCreated={vi.fn()} />);
    openDetails();

    await screen.findByText('selfEvolutionRun.linkedSkillLoadFailed');
    expect(screen.queryByRole('button', { name: /selfEvolutionRun.linkedSkillReconvert$/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    expect(await screen.findByText(skill.name)).toBeInTheDocument();
  });

  it.each(['success', 'failure'] as const)('ignores a late %s from a closed detail session after reopening', async (outcome) => {
    const staleSkill = deferred<SkillAssetRecord>();
    const staleLinks = deferred<SkillLinkedWorkflowsResponse>();
    api.getSkillAssetDetail.mockReturnValueOnce(staleSkill.promise).mockResolvedValueOnce({ ...skill, name: 'Fresh skill name' });
    api.listSkillLinkedWorkflows.mockReturnValueOnce(staleLinks.promise).mockResolvedValueOnce(links([
      linkedWorkflow({ name: 'Fresh linked workflow' }),
    ]));
    render(<LinkedSkillButton draft={draft} onCreated={vi.fn()} />);
    openDetails();
    fireEvent.click(screen.getByRole('button', { name: 'common.close' }));
    openDetails();
    expect(await screen.findByText('Fresh skill name')).toBeInTheDocument();

    await act(async () => {
      if (outcome === 'success') staleSkill.resolve({ ...skill, name: 'Stale skill name' });
      else staleSkill.reject(new Error('stale request failed'));
      staleLinks.resolve(links([linkedWorkflow({ name: 'Stale linked workflow' })]));
    });

    expect(screen.getByText('Fresh skill name')).toBeInTheDocument();
    expect(screen.getByText('Fresh linked workflow')).toBeInTheDocument();
    expect(screen.queryByText('Stale skill name')).not.toBeInTheDocument();
    expect(screen.queryByText('Stale linked workflow')).not.toBeInTheDocument();
    expect(screen.queryByText('selfEvolutionRun.linkedSkillLoadFailed')).not.toBeInTheDocument();
    expect(api.getSkillAssetDetail).toHaveBeenCalledTimes(2);
  });

  it('matches builtin metadata by its unprefixed ID and preserves the full conversion source', async () => {
    api.listBuiltinSkills.mockResolvedValue([
      { ...skill, id: 'other-builtin', name: 'Other builtin' },
      { ...skill, id: 'report-builtin', name: 'Builtin report skill' },
    ]);
    render(<LinkedSkillButton draft={{ ...draft, source_skill_id: 'builtin:report-builtin' }} onCreated={vi.fn()} />);
    expect(api.listBuiltinSkills).not.toHaveBeenCalled();
    openDetails();

    expect(await screen.findByText('Builtin report skill')).toBeInTheDocument();
    expect(screen.getByText('selfEvolutionRun.linkedSkillBuiltin')).toBeInTheDocument();
    expect(screen.queryByText('Other builtin')).not.toBeInTheDocument();
    expect(api.listBuiltinSkills).toHaveBeenCalledTimes(1);
    expect(api.getSkillAssetDetail).not.toHaveBeenCalled();
    expect(api.listSkillLinkedWorkflows).toHaveBeenCalledWith('builtin:report-builtin');
    fireEvent.click(reconvertButton());
    expect(screen.getByTestId('conversion-skill-id')).toHaveTextContent('builtin:report-builtin');
  });

  it('allows viewing while conversion is disabled', async () => {
    render(<LinkedSkillButton draft={draft} conversionDisabled onCreated={vi.fn()} />);
    openDetails();
    expect(await screen.findByText(skill.name)).toBeInTheDocument();
    expect(reconvertButton()).toBeDisabled();
    fireEvent.click(reconvertButton());
    expect(screen.queryByRole('dialog', { name: 'New workflow' })).not.toBeInTheDocument();
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.updateWorkflowDraftContent).not.toHaveBeenCalled();
  });

  it('opens a separate conversion preselected with the live source and forwards creation without writing the current draft', async () => {
    const onCreated = vi.fn();
    const currentDraft = { ...draft };
    render(<LinkedSkillButton draft={currentDraft} onCreated={onCreated} />);
    openDetails();
    await screen.findByText(skill.name);
    fireEvent.click(reconvertButton());

    const conversion = screen.getByRole('dialog', { name: 'New workflow' });
    expect(within(conversion).getByTestId('conversion-skill-id')).toHaveTextContent(draft.source_skill_id);
    expect(within(conversion).getByText(skill.name)).toBeInTheDocument();
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(api.updateWorkflowDraftContent).not.toHaveBeenCalled();
    expect(api.aiGenerateWorkflowDraft).not.toHaveBeenCalled();
    expect(onCreated).not.toHaveBeenCalled();
    expect(currentDraft).toEqual(draft);

    fireEvent.click(within(conversion).getByRole('button', { name: 'Finish conversion' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'New workflow' })).not.toBeInTheDocument());
    expect(onCreated).toHaveBeenCalledTimes(1);
    expect(onCreated).toHaveBeenCalledWith('new-workflow-draft');
    expect(currentDraft).toEqual(draft);
  });
});
