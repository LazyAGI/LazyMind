import { useEffect, useState } from 'react';
import type { StepNode } from '../core/model';
import './index.scss';

export interface ScenarioData {
  overview: string;
  /** Step descriptions keyed by step id. */
  stepDescriptions: Record<string, string>;
  notes: string;
}

interface Props {
  /** Current step list from state.yml — drives the rows table. */
  steps: StepNode[];
  value: ScenarioData;
  onChange: (data: ScenarioData) => void;
}

export function serializeScenario(steps: StepNode[], data: ScenarioData): string {
  const lines: string[] = [];
  if (data.overview.trim()) {
    lines.push('## 场景描述', '', data.overview.trim(), '');
  }
  if (steps.length > 0) {
    lines.push('## 工作流程', '');
    for (const step of steps) {
      const desc = data.stepDescriptions[step.id] ?? '';
      lines.push(`### ${step.id}（${step.label}）`, '', desc.trim() || '（暂无描述）', '');
    }
  }
  if (data.notes.trim()) {
    lines.push('## 注意事项', '', data.notes.trim(), '');
  }
  return lines.join('\n');
}

export function parseScenario(markdown: string, steps: StepNode[]): ScenarioData {
  const data: ScenarioData = { overview: '', stepDescriptions: {}, notes: '' };
  if (!markdown) return data;

  const sections = markdown.split(/^##\s+/m).filter(Boolean);
  for (const section of sections) {
    const [header, ...bodyLines] = section.split('\n');
    const body = bodyLines.join('\n').trim();
    if (header.includes('场景描述')) {
      data.overview = body;
    } else if (header.includes('工作流程')) {
      const stepBlocks = body.split(/^###\s+/m).filter(Boolean);
      for (const block of stepBlocks) {
        const [stepHeader, ...stepBodyLines] = block.split('\n');
        const stepId = stepHeader.split('（')[0].trim();
        const stepBody = stepBodyLines.join('\n').trim();
        if (stepId) data.stepDescriptions[stepId] = stepBody === '（暂无描述）' ? '' : stepBody;
      }
    } else if (header.includes('注意事项')) {
      data.notes = body;
    }
  }
  // Ensure all current steps have an entry
  for (const step of steps) {
    if (!(step.id in data.stepDescriptions)) {
      data.stepDescriptions[step.id] = '';
    }
  }
  return data;
}

/**
 * ScenarioEditor now only shows the step descriptions table.
 * Overview and notes are edited via PluginInfoModal (场景说明 tab).
 */
export default function ScenarioEditor({ steps, value, onChange }: Props) {
  const [localStepDescs, setLocalStepDescs] = useState<Record<string, string>>(value.stepDescriptions);

  // Sync step descriptions when steps change
  useEffect(() => {
    const next: Record<string, string> = {};
    for (const step of steps) {
      next[step.id] = localStepDescs[step.id] ?? '';
    }
    setLocalStepDescs(next);
  }, [steps.map((s) => s.id).join(',')]); // eslint-disable-line react-hooks/exhaustive-deps

  const updateStepDesc = (stepId: string, desc: string) => {
    const next = { ...value.stepDescriptions, [stepId]: desc };
    setLocalStepDescs(next);
    onChange({ ...value, stepDescriptions: next });
  };

  return (
    <div className="scenario-editor">
      {steps.length === 0 ? (
        <p className="se-empty-hint">请先在画布中添加步骤，在步骤属性面板填写步骤说明</p>
      ) : (
        <div className="se-steps-table">
          {steps.map((step) => (
            <div key={step.id} className="se-step-row">
              <div className="se-step-id">
                <span className="se-step-id-tag">{step.id}</span>
                <span className="se-step-label">{step.label}</span>
              </div>
              <input
                className="se-step-desc-input"
                value={localStepDescs[step.id] ?? ''}
                onChange={(e) => updateStepDesc(step.id, e.target.value)}
                placeholder="描述该步骤的作用…"
              />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
