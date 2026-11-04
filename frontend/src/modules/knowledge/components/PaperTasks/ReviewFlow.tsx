import type { ReactNode } from 'react';
import { Alert, Steps } from 'antd';
import { useTranslation } from 'react-i18next';
import './ReviewFlow.scss';

interface Props {
    status?: string;
    reviewStarted?: boolean;
    readyCount?: number;
    entry?: boolean;
    direct?: boolean;
    children?: ReactNode;
}

export default function ReviewFlow({ status, reviewStarted = false, readyCount, entry = false, direct = false, children }: Props) {
    const { t } = useTranslation();
    if (entry) return <section className="paper-review-flow" aria-label={t('paperTasks.reviewFlow.label')}>
        <ReviewEntryFlow direct={direct} />
    </section>;
    if (reviewStarted || direct) return <section className="paper-review-flow" aria-label={t('paperTasks.reviewFlow.label')}>
        <Alert type="info" showIcon message={t(reviewStarted ? 'paperTasks.reviewReceipt' : 'paperTasks.directReviewIntro')}
            description={t(reviewStarted ? 'paperTasks.reviewReceiptHint' : 'paperTasks.directReviewHint')} />
        {children}
    </section>;
    const preparing = ['preparing', 'awaiting_info', 'preparation_failed'].includes(status || '');
    const failed = ['failed', 'preparation_failed'].includes(status || '');
    const stopped = status === 'canceled';
    const waitingForContent = status === 'completed' && !reviewStarted;
    const current = entry || preparing ? 0 : reviewStarted ? 3 : status === 'awaiting_confirmation' ? 1 : 2;
    const message = entry ? 'intro' : failed ? 'failed' : stopped ? 'stopped' : reviewStarted ? 'writing'
        : waitingForContent ? (readyCount !== undefined && readyCount < 2 ? 'needFulltext' : 'ready') : 'preparing';

    return <section className="paper-review-flow" aria-label={t('paperTasks.reviewFlow.label')}>
        <Steps size="small" current={current} status={failed ? 'error' : stopped || waitingForContent || reviewStarted ? 'wait' : 'process'}
            items={['research', 'confirm', 'read', 'write'].map(stage => ({ title: t(`paperTasks.reviewFlow.stage.${stage}`) }))} />
        <Alert type={failed ? 'error' : stopped ? 'warning' : 'info'} showIcon
            message={t(`paperTasks.reviewFlow.${message}`)}
            description={t(`paperTasks.reviewFlow.${message}Hint`, { count: readyCount })} />
        {children}
    </section>;
}

function ReviewEntryFlow({ direct }: { direct: boolean }) {
    const { t } = useTranslation();
    const stages = direct ? ['inputs', 'selectedFulltext', 'handoff', 'generate'] : ['inputs', 'research', 'directions', 'expandedFulltext', 'handoff', 'generate'];
    return <><strong>{t('paperTasks.reviewFlow.label')}</strong><ol className="paper-review-entry-flow">
        {stages.map((stage, index) => <li key={stage}><span className="paper-review-stage-number">{index + 1}</span><div>{t(`paperTasks.reviewFlow.entry.${stage}`)}<small>{t(`paperTasks.reviewFlow.entry.${stage}Hint`)}</small></div></li>)}
    </ol></>;
}
