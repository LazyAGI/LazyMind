import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import type { BudgetMode, DirectionSelection } from '../../api/paperWorkflows';

export const researchDepthLabels = { fast: '快速', balanced: '标准', deep: '深入' };

const sharedExpansionSteps = ['search', 'screen'] as const;
const expansionSteps = {
  fast: ['search'],
  balanced: sharedExpansionSteps,
  deep: [...sharedExpansionSteps, 'analyze'],
} as const;
const reviewModes = ['fast', 'balanced'] as const;
const expansionModes = [...reviewModes, 'deep'] as const;

export default function ResearchDepth({ value, onChange, forReview = false, directionSelection, onDirectionSelectionChange, disabled = false }: {
  value: BudgetMode;
  onChange: (value: BudgetMode) => void;
  forReview?: boolean;
  directionSelection: DirectionSelection;
  onDirectionSelectionChange: (value: DirectionSelection) => void;
  disabled?: boolean;
}) {
  const name = useId();
  const { t } = useTranslation();
  const title = t('paperTasks.expansionDepth.title');
  const directionTitle = t('paperTasks.expansionDepth.directionSelection');
  const modes = forReview ? reviewModes : expansionModes;
  return <section className="paper-research-depth">
    <h3>{title}</h3>
    <div className={`paper-research-depth-options ${forReview ? 'is-review' : 'has-flow'}`} role="radiogroup" aria-label={title}>
      {modes.map(mode => {
        const label = t(`paperTasks.expansionDepth.${mode}.label`);
        const directionOptions = <span className="paper-expansion-direction-options" role="radiogroup"
          aria-label={`${label} · ${directionTitle}`} onClick={forReview ? event => event.stopPropagation() : undefined}>
          {(['all', 'manual'] as const).map(selection => <label key={selection}>
            <input type="radio" name={`${name}-${mode}-directions`} value={selection}
              checked={directionSelection === selection} disabled={disabled || (forReview && value !== mode)}
              onChange={() => onDirectionSelectionChange(selection)} />
            {t(`paperTasks.expansionDepth.${selection === 'all' ? 'allDirections' : 'yourChoice'}`)}
          </label>)}
        </span>;
        return <div key={mode} className={`paper-research-depth-option${value === mode ? ' is-selected' : ''}`} onClick={() => { if (!disabled) onChange(mode); }}>
          <label className="paper-research-depth-name"><input type="radio" name={name} value={mode} checked={value === mode} disabled={disabled} onChange={() => onChange(mode)} />{label}</label>
          <span className="paper-research-depth-description"><span>{forReview && mode === 'fast' ? t('paperTasks.reviewFlow.entry.targetedSearch') : t(`paperTasks.expansionDepth.${mode}.summary`)}</span></span>
          {forReview ? mode === 'balanced' && <div className="paper-review-card-directions">
            <span>{directionTitle}</span>
            {directionOptions}
          </div> : <>
            <span className="paper-expansion-mode-flow" role="list">
              {mode !== 'fast' && ['research', 'chooseDirection'].map(step => <span key={step} role="listitem"
                className={`paper-expansion-mode-step step-${step}`}>
                <span>{t(`paperTasks.expansionDepth.steps.${step}`)}</span>
                {step === 'chooseDirection' && directionOptions}
              </span>)}
              {[...expansionSteps[mode], 'prepareFulltext'].map(step => <span key={step} role="listitem"
                className={`paper-expansion-mode-step step-${step}`}>
                {t(`paperTasks.expansionDepth.steps.${step}`)}
              </span>)}
            </span>
            <span className="paper-expansion-mode-output">{t(`paperTasks.expansionDepth.outputs.${mode}`)}</span>
          </>}
        </div>;
      })}
    </div>
  </section>;
}
