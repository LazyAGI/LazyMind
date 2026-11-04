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
    entry?: boolean;
    children?: ReactNode;
}

export default function ReviewFlow({ status, reviewStarted = false, readyCount = 0, processingCount = 0, researchIncomplete = false, entry = false, children }: Props) {
    const { t } = useTranslation();
    if (entry) return <section className="paper-review-flow" aria-label={t('paperTasks.reviewFlow.label')}>
        <ReviewEntryFlow />
    </section>;
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

function ReviewEntryFlow() {
    const { t } = useTranslation();
    const stages = ['inputs', 'selectedFulltext', 'handoff'];
    return <><strong>{t('paperTasks.reviewFlow.label')}</strong><ol className="paper-review-entry-flow">
        {stages.map((stage, index) => <li key={stage}><span className="paper-review-stage-number">{index + 1}</span><div>{t(`paperTasks.reviewFlow.entry.${stage}`)}<small>{t(`paperTasks.reviewFlow.entry.${stage}Hint`)}</small></div></li>)}
    </ol></>;
}
