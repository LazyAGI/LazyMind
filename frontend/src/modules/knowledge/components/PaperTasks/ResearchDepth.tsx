import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import type { BudgetMode } from '../../api/paperWorkflows';

const researchDepthOptions: Array<{ value: BudgetMode; label: string; description: string[] }> = [
  { value: 'fast', label: '快速', description: ['聚焦核心问题', '代表论文与关键结论'] },
  { value: 'balanced', label: '标准', description: ['覆盖主要研究路线', '比较方法与关键证据'] },
  { value: 'deep', label: '深入', description: ['拓展关联与分歧', '追踪证据与适用边界'] },
];
export const researchDepthLabels = { fast: '快速', balanced: '标准', deep: '深入' };

const sharedExpansionSteps = ['research', 'chooseDirection', 'search', 'screen', 'groupedList'] as const;
const expansionSteps = {
  fast: ['directSearch', 'prioritizedList'],
  balanced: sharedExpansionSteps,
  deep: [...sharedExpansionSteps, 'report'],
} as const;

export default function ResearchDepth({ value, onChange, expansion = false }: { value: BudgetMode; onChange: (value: BudgetMode) => void; expansion?: boolean }) {
  const name = useId();
  const { t } = useTranslation();
  const title = t(expansion ? 'paperTasks.expansionDepth.title' : 'paperTasks.reviewFlow.depthTitle');
  const options = expansion ? researchDepthOptions.map(({ value: mode }) => ({
    value: mode,
    label: t(`paperTasks.expansionDepth.${mode}.label`),
    description: [t(`paperTasks.expansionDepth.${mode}.summary`)],
  })) : researchDepthOptions;
  return <section className="paper-research-depth">
    <h3>{title}</h3>
    <div className={expansion ? 'paper-research-depth-options has-flow' : 'paper-research-depth-options'} role="radiogroup" aria-label={title}>
      {options.map(option => <label key={option.value} className={`paper-research-depth-option${value === option.value ? ' is-selected' : ''}`}>
        <span className="paper-research-depth-name"><input type="radio" name={name} value={option.value} checked={value === option.value} onChange={() => onChange(option.value)} />{option.label}</span>
        <span className="paper-research-depth-description">{option.description.map(line => <span key={line}>{line}</span>)}</span>
        {expansion && <span className="paper-expansion-mode-flow" role="list">
          {expansionSteps[option.value].map(step => <span key={step} role="listitem"
            className={`paper-expansion-mode-step step-${step}`}>
            {t(`paperTasks.expansionDepth.steps.${step}`)}
            {step === 'chooseDirection' && <span className="paper-expansion-mode-human">{t('paperTasks.expansionDepth.yourChoice')}*</span>}
          </span>)}
        </span>}
      </label>)}
    </div>
    {expansion && <p className="paper-expansion-mode-hint">* {t('paperTasks.expansionDepth.directionsHint')}</p>}
  </section>;
}
