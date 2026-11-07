import { CheckOutlined } from '@ant-design/icons';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { PaperFlowStep } from './paperFlowModel';
import './ReviewFlow.scss';

export default function PaperFlow({ title, steps, preview = false, hint, children }: {
    title: string;
    steps: PaperFlowStep[];
    preview?: boolean;
    hint?: string;
    children?: ReactNode;
}) {
    const { t } = useTranslation();
    const current = preview ? steps[0] : steps.find(step => step.status !== 'done' && step.status !== 'pending' && step.status !== 'unrecorded');
    return <section className={preview ? 'paper-review-entry' : 'paper-review-entry paper-task-detail-flow'} aria-label={title}>
        <div className="paper-review-entry-heading">
            <h3>{title}</h3>
            <span>{t('paperTasks.reviewFlow.entry.stepCount', { count: steps.length })}</span>
        </div>
        <ol className="paper-review-entry-flow" aria-live="polite">
            {steps.map((step, index) => <li key={step.id} aria-current={step === current ? 'step' : undefined} data-status={step.status}>
                <span className="paper-review-stage-number" aria-hidden>{step.status === 'done' ? <CheckOutlined /> : index + 1}</span>
                <span className="paper-review-stage-caption">
                    <span className="paper-review-stage-title">{step.labelKey ? t(step.labelKey) : step.label || step.id}</span>
                    {step.status && <span className="paper-review-stage-status">{t(`paperTasks.flowStatus.${step.status}`)}</span>}
                </span>
            </li>)}
        </ol>
        {hint && <p className="paper-review-entry-hint">{hint}</p>}
        {children}
    </section>;
}
