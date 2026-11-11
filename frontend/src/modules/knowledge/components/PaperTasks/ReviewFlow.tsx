import type { ReactNode } from 'react';
import { Alert } from 'antd';
import { useTranslation } from 'react-i18next';
import PaperFlow from './PaperFlow';
import { paperFlowSteps, type PaperFlowStep } from './paperFlowModel';
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
    const expansionStatus: PaperFlowStep['status'] = message === 'ready' ? 'done' : failed ? 'failed' : stopped ? 'paused'
        : message === 'needFulltext' ? 'needFulltext' : researchIncomplete ? 'incompleteResearch' : 'running';
    const steps = paperFlowSteps('review', 'balanced').map<PaperFlowStep>(step => ({ ...step,
        status: step.id === 'inputs' ? 'done' : step.id === 'generateReview' ? message === 'ready' ? 'queued' : 'pending' : expansionStatus,
    }));

    return <section className="paper-review-flow">
        <PaperFlow title={t('paperTasks.reviewFlow.label')} steps={steps} />
        <Alert type={failed ? 'error' : stopped || researchIncomplete || message === 'needFulltext' ? 'warning' : 'info'} showIcon
            message={t(`paperTasks.reviewFlow.${message}`)}
            description={t(`paperTasks.reviewFlow.${message}Hint`, { count: readyCount, processing: processingCount })} />
        {children}
    </section>;
}

export function ReviewEntryFlow({ mode, directionOnly = false }: { mode: 'direct' | 'fast' | 'balanced'; directionOnly?: boolean }) {
    const { t } = useTranslation();
    return <PaperFlow title={t('paperTasks.reviewFlow.label')} steps={paperFlowSteps('review', mode, directionOnly)} preview />;
}
