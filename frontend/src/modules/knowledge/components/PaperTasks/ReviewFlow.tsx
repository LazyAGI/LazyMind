import type { ReactNode } from 'react';
import { Alert, Steps } from 'antd';
import { useTranslation } from 'react-i18next';
import './ReviewFlow.scss';

interface Props {
    status?: string;
    reviewStarted?: boolean;
    readyCount?: number;
    processingCount?: number;
    researchIncomplete?: boolean;
    children?: ReactNode;
}

export default function ReviewFlow({ status, reviewStarted = false, readyCount = 0, processingCount = 0, researchIncomplete = false, children }: Props) {
    const { t } = useTranslation();
    if (reviewStarted) return <section className="paper-review-flow" aria-label={t('paperTasks.reviewFlow.label')}>
        <Alert type="info" showIcon message={t('paperTasks.reviewReceipt')} description={t('paperTasks.reviewReceiptHint')} />
        {children}
    </section>;
    const failed = ['failed', 'preparation_failed'].includes(status || '');
    const stopped = status === 'canceled' || status === 'stopped';
    const completed = status === 'completed';
    const message = failed ? 'failed' : stopped ? 'stopped' : !completed ? 'preparing'
        : researchIncomplete ? 'incompleteResearch' : processingCount > 0 ? 'processingFulltext' : readyCount < 2 ? 'needFulltext' : 'ready';
    const current = message === 'ready' ? 2 : completed && !researchIncomplete ? 1 : 0;

    return <section className="paper-review-flow" aria-label={t('paperTasks.reviewFlow.label')}>
        <Steps size="small" current={current} status={failed ? 'error' : stopped || researchIncomplete || message === 'needFulltext' ? 'wait' : 'process'}
            items={['expand', 'prepareFulltext', 'handoff'].map(stage => ({ title: t(`paperTasks.reviewFlow.stage.${stage}`) }))} />
        <Alert type={failed ? 'error' : stopped || researchIncomplete || message === 'needFulltext' ? 'warning' : 'info'} showIcon
            message={t(`paperTasks.reviewFlow.${message}`)}
            description={t(`paperTasks.reviewFlow.${message}Hint`, { count: readyCount, processing: processingCount })} />
        {children}
    </section>;
}

const entryExpansionSteps = {
    direct: [],
    fast: ['reviewFlow.entry.targetedSearch', 'expansionDepth.steps.prepareFulltext'],
    balanced: ['reviewFlow.entry.prepareDirections', 'reviewFlow.entry.search', 'expansionDepth.steps.screen', 'expansionDepth.steps.prepareFulltext'],
} as const;

export function ReviewEntryFlow({ mode, directionOnly }: { mode: keyof typeof entryExpansionSteps; directionOnly: boolean }) {
    const { t } = useTranslation();
    const title = t('paperTasks.reviewFlow.label');
    const stages = [
        directionOnly ? 'reviewFlow.entry.topic' : 'reviewFlow.entry.inputs',
        ...entryExpansionSteps[mode],
        'reviewFlow.stage.handoff',
    ];
    return <section className="paper-review-entry" aria-label={title} aria-live="polite">
        <div className="paper-review-entry-heading">
            <h3>{title}</h3>
            <span>{t('paperTasks.reviewFlow.entry.stepCount', { count: stages.length })}</span>
        </div>
        <ol className="paper-review-entry-flow" role="list">
            {stages.map((stage, index) => <li key={stage} aria-current={index === 0 ? 'step' : undefined}>
                <span className="paper-review-stage-number" aria-hidden>{index + 1}</span>
                <span className="paper-review-stage-title">{t(`paperTasks.${stage}`)}</span>
            </li>)}
        </ol>
        {mode !== 'direct' && <p className="paper-review-entry-hint">{t('paperTasks.reviewFlow.entry.fulltextBatchHint')}</p>}
    </section>;
}
