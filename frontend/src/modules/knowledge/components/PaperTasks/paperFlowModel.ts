import type { BudgetMode, Expansion, ExpansionDetail, PaperMaterialProgress } from '../../api/paperWorkflows';

const workflowStages = {
    fast: ['expand_and_screen'],
    balanced: ['expand_and_screen', 'screen_and_supplement'],
    deep: ['expand_and_screen', 'screen_and_supplement', 'analyze_relations'],
};

const stepLabels: Record<string, string> = {
    inputs: 'reviewFlow.entry.inputs',
    topic: 'reviewFlow.entry.topic',
    research: 'expansionDepth.steps.research',
    chooseDirection: 'expansionDepth.steps.chooseDirection',
    prepareDirections: 'reviewFlow.entry.prepareDirections',
    expand_and_screen: 'expansionDepth.steps.search',
    screen_and_supplement: 'expansionDepth.steps.screen',
    analyze_relations: 'expansionDepth.steps.analyze',
    prepareFulltext: 'expansionDepth.steps.prepareFulltext',
    expansion: 'reviewFlow.stage.expand',
    generateReview: 'reviewFlow.writing',
    fastScreen: 'reviewFlow.entry.screenRelated',
};

export interface PaperFlowStep {
    id: string;
    labelKey?: string;
    label?: string;
    status?: 'done' | 'running' | 'queued' | 'waiting' | 'waitingDirection' | 'waitingInfo' | 'paused' | 'failed' | 'canceled' | 'pending' | 'unrecorded' | 'unknown' | 'waitingHandoff' | 'needFulltext' | 'incompleteResearch';
}

function makeStep(id: string, kind: 'expansion' | 'review', mode: BudgetMode | 'direct', label?: string): PaperFlowStep {
    const labelKey = kind === 'review' && id === 'expand_and_screen'
        ? mode === 'fast' ? 'reviewFlow.entry.coreSearch' : 'reviewFlow.entry.search'
        : stepLabels[id];
    return { id, labelKey: labelKey && `paperTasks.${labelKey}`, label };
}

export function paperFlowSteps(kind: 'expansion' | 'review', mode: BudgetMode | 'direct', directionOnly = false) {
    if (kind === 'review') {
        return [directionOnly ? 'topic' : 'inputs', ...(mode === 'direct' ? [] : ['expansion']), 'generateReview']
            .map(id => makeStep(id, kind, mode));
    }
    return mode === 'direct' ? [] : expansionFlowSteps(mode);
}

export function expansionFlowSteps(mode: BudgetMode, forReview = false) {
    const preparation = mode === 'fast' ? [] : forReview ? ['prepareDirections'] : ['research', 'chooseDirection'];
    const stages = forReview && mode === 'fast' ? ['expand_and_screen', 'fastScreen'] : workflowStages[mode];
    return [...preparation, ...stages, ...(forReview ? [] : ['prepareFulltext'])]
        .map(id => makeStep(id, forReview ? 'review' : 'expansion', mode));
}

export function reviewGenerationStatus(entry?: { status: string; terminalStatus?: string }): PaperFlowStep['status'] {
    if (!entry || entry.status === 'unknown') return 'unknown';
    if (entry.status === 'running') return 'running';
    if (entry.status === 'idle') {
        if (entry.terminalStatus === 'completed') return 'done';
        if (entry.terminalStatus === 'failed') return 'failed';
        if (entry.terminalStatus === 'canceled') return 'canceled';
        return 'waiting';
    }
    return 'unknown';
}

export function reviewMaterialCounts(detail: ExpansionDetail) {
    const eligible = detail.contents.filter(content => content.status === 'ready' && content.content_hash && content.quality?.eligible);
    return {
        ready: new Set(eligible.map(content => content.paper_id)).size,
        distinctReady: detail.material_progress?.distinct_ready_count
            ?? new Set(eligible.map(content => `${content.source_hash || ''}:${content.content_hash}`)).size,
        processing: detail.material_progress?.processing_count
            ?? detail.contents.filter(content => ['pending', 'processing'].includes(content.status)).length,
    };
}

export function paperMaterialPhase(record: Expansion, progress: PaperMaterialProgress | undefined = record.material_progress) {
    if (record.status !== 'completed') return;
    if (record.goal === 'review' && record.auto_review_id) return 'done';
    if (!progress) return 'published';
    if (progress.processing_count > 0) return 'fulltext';
    if (record.goal !== 'review') return 'done';
    if (progress.distinct_ready_count === undefined) return 'published';
    if (progress.distinct_ready_count < 2) return 'needFulltext';
    return record.report?.research_complete === false ? 'incompleteResearch' : 'waitingHandoff';
}

export function expansionRecordIsActive(record: Expansion) {
    const phase = paperMaterialPhase(record);
    return phase === 'fulltext' || phase === 'waitingHandoff'
        || ['pending', 'running', 'active', 'preparing', 'awaiting_input'].includes(record.status);
}

const preparationStates = {
    preparing: 'running',
    awaiting_info: 'waitingInfo',
    awaiting_confirmation: 'waitingDirection',
    preparation_failed: 'failed',
} as const;

const workflowStates: Record<string, PaperFlowStep['status']> = {
    succeeded: 'done', completed: 'done', running: 'running', claimed: 'queued', queued: 'queued', pending: 'queued',
    waiting: 'waiting', failed: 'failed', interrupted: 'paused', canceled: 'canceled', cancelled: 'canceled',
};

export const stageArtifacts: Record<string, string> = {
    expand_and_screen: 'candidate_set',
    screen_and_supplement: 'screened_candidates',
    analyze_relations: 'relation_analysis',
    compose_result: 'expansion_result',
};

export function paperExpansionDetailFlow(detail: ExpansionDetail) {
    const { record, workflow_steps } = detail;
    const kind = record.goal === 'review' ? 'review' : 'expansion';
    const mode = record.budget_mode || 'balanced';
    const plan = kind === 'review'
        ? [...(mode === 'fast' ? [] : ['prepareDirections']), ...workflowStages[mode], 'prepareFulltext'].map(id => makeStep(id, kind, mode))
        : paperFlowSteps(kind, mode);
    const preparationStatus = preparationStates[record.status as keyof typeof preparationStates];
    const completed = record.status === 'completed';
    const counts = reviewMaterialCounts(detail);
    const materials = paperMaterialPhase(record, { processing_count: counts.processing, distinct_ready_count: counts.distinctReady });
    const handedOff = kind === 'review' && !!record.auto_review_id;

    // Historical records can have a different route. Prefer the pinned workflow
    // or the completed round's execution history when the route is unavailable.
    const timing = record.report?.stage_timing;
    const candidate = record.progress?.candidate_set;
    const recordedSteps = workflow_steps?.length ? workflow_steps
        : completed && timing?.length ? [...new Map(timing.map(step => [step.step_id, step])).values()]
            : undefined;
    const finalList = candidate?.list_mode === 'fast' || candidate?.list_mode === 'balanced';
    const execution = recordedSteps ?? (finalList && !record.progress?.screened_candidates
        ? [{ step_id: 'expand_and_screen', status: 'none' }]
        : workflowStages[mode].map(step_id => ({ step_id, status: 'none' })));
    const legacyWorkflow = execution.some(step => !workflowStages.deep.includes(step.step_id));
    const steps = plan.filter(step => !workflowStages.deep.includes(step.id)
        && !(legacyWorkflow && !record.preparation_revision && ['research', 'chooseDirection', 'prepareDirections'].includes(step.id)));
    const actual = execution.map((step, index): PaperFlowStep => {
        const { step_id: id, status } = step;
        const known = 'label' in step && typeof step.label === 'string' ? step.label : undefined;
        const historicalLabel = legacyWorkflow || id === 'expand_and_screen' && !execution.some(item => item.step_id === 'screen_and_supplement') && mode !== 'fast';
        const presentation = historicalLabel && known ? { id, label: known } : makeStep(id, kind, mode, known);
        const artifact = record.progress?.[stageArtifacts[id]];
        const state = workflowStates[status];
        if (state) return { ...presentation, status: state };
        if (!recordedSteps && artifact && (completed || index < execution.length - 1)) return { ...presentation, status: 'done' };
        return { ...presentation, status: completed ? 'unrecorded' : 'pending' };
    });
    const active = actual.find(step => step.status !== 'done' && step.status !== 'pending')
        ?? actual.find(step => step.status === 'pending');
    if (active && !preparationStatus) {
        if (record.status === 'awaiting_input') active.status = 'waitingInfo';
        if (record.status === 'stopped') active.status = 'paused';
        if (record.status === 'failed') active.status = 'failed';
        if (['canceled', 'cancelled'].includes(record.status)) active.status = 'canceled';
        if (record.status === 'pending' && active.status === 'pending') active.status = 'queued';
    } else if (record.status === 'failed' && actual.length) {
        actual[actual.length - 1].status = 'failed';
    }
    steps.splice(steps.findIndex(step => step.id === 'prepareFulltext'), 0, ...actual);

    const preparationDone = !!record.preparation?.confirmed_at || !!record.preparation?.selected_directions?.length && !preparationStatus;
    for (const step of steps) {
        switch (step.id) {
            case 'prepareDirections': case 'research': case 'chooseDirection':
                step.status = preparationDone ? 'done' : preparationStatus || 'unrecorded';
                if (preparationStatus === 'waitingDirection' && step.id === 'research') step.status = 'done';
                if (preparationStatus && preparationStatus !== 'waitingDirection' && step.id === 'chooseDirection') step.status = 'pending';
                break;
            case 'prepareFulltext':
                step.status = handedOff ? 'done' : !completed ? 'pending' : materials === 'fulltext' ? 'running'
                    : materials === 'needFulltext' ? 'needFulltext' : detail.contents.length ? 'done' : 'unrecorded';
                break;
        }
    }
    return steps;
}

export function reviewExpansionDetailFlow(detail: ExpansionDetail) {
    const steps = paperExpansionDetailFlow(detail).filter(step => step.id !== 'prepareFulltext');
    if (detail.record.budget_mode === 'fast') {
        const search = steps.find(step => step.id === 'expand_and_screen');
        if (search) {
            const screening = makeStep('fastScreen', 'review', 'fast');
            screening.status = search.status === 'done' ? 'done' : 'pending';
            if (detail.record.progress?.candidate_set?.screening_pending) {
                screening.status = search.status;
                search.status = 'done';
            }
            steps.splice(steps.indexOf(search) + 1, 0, screening);
        }
    }
    return steps;
}

export function paperDetailFlow(detail: ExpansionDetail, reviewStatus: PaperFlowStep['status'] = 'unknown') {
    const { record } = detail;
    if (record.goal !== 'review') return paperExpansionDetailFlow(detail);
    const counts = reviewMaterialCounts(detail);
    const phase = paperMaterialPhase(record, { processing_count: counts.processing, distinct_ready_count: counts.distinctReady });
    const expansion = paperExpansionDetailFlow(detail);
    const current = expansion.find(step => !['done', 'pending', 'unrecorded'].includes(step.status || ''));
    const expansionStatus = record.auto_review_id ? 'done' : phase === 'fulltext' ? 'running'
        : phase === 'needFulltext' ? 'needFulltext' : phase === 'incompleteResearch' ? 'incompleteResearch'
            : record.status === 'completed' ? 'done' : current?.status || (record.status === 'pending' ? 'queued' : 'running');
    return paperFlowSteps('review', record.budget_mode, !record.result.papers.some(paper => paper.is_seed))
        .map(step => ({ ...step, status: step.id === 'expansion' ? expansionStatus
            : step.id === 'generateReview' ? record.auto_review_id ? reviewStatus : phase === 'waitingHandoff' ? 'queued' : 'pending'
                : 'done' } satisfies PaperFlowStep));
}
