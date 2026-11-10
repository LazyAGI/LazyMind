import { CheckOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { PaperFlowStep } from './paperFlowModel';

export default function ExpansionSteps({ steps }: { steps: PaperFlowStep[] }) {
    const { t } = useTranslation();
    return <ol className="paper-review-card-flow">
        {steps.map((step, index) => <li key={step.id} data-status={step.status}>
            <span className="paper-review-card-step-number" aria-hidden>{step.status === 'done' ? <CheckOutlined /> : index + 1}</span>
            <span>{step.labelKey ? t(step.labelKey) : step.label || step.id}</span>
            {step.status && <span className="paper-review-card-step-status">{t(`paperTasks.flowStatus.${step.status}`)}</span>}
        </li>)}
    </ol>;
}
