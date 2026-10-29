import { useId } from 'react';
import type { BudgetMode } from '../../api/paperWorkflows';

export const researchDepthOptions: Array<{ value: BudgetMode; label: string; description: string[] }> = [
  { value: 'fast', label: '快速', description: ['聚焦核心问题', '代表论文与关键结论'] },
  { value: 'balanced', label: '标准', description: ['覆盖主要研究路线', '比较方法与关键证据'] },
  { value: 'deep', label: '深入', description: ['拓展关联与分歧', '追踪证据与适用边界'] },
];
export const researchDepthLabels = { fast: '快速', balanced: '标准', deep: '深入' };

export default function ResearchDepth({ value, onChange }: { value: BudgetMode; onChange: (value: BudgetMode) => void }) {
  const name = useId();
  return <section className="paper-research-depth">
    <h3>调研深度</h3>
    <div className="paper-research-depth-options" role="radiogroup" aria-label="调研深度">
      {researchDepthOptions.map(option => <label key={option.value} className={`paper-research-depth-option${value === option.value ? ' is-selected' : ''}`}>
        <span className="paper-research-depth-name"><input type="radio" name={name} value={option.value} checked={value === option.value} onChange={() => onChange(option.value)} />{option.label}</span>
        <span className="paper-research-depth-description">{option.description.map(line => <span key={line}>{line}</span>)}</span>
      </label>)}
    </div>
    <p className="paper-research-depth-hint">本任务从初步调研到正式拓展，始终使用此深度。</p>
  </section>;
}
