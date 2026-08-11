import { useState } from 'react';
import { Button, Select, Tooltip } from 'antd';
import {
  CloseOutlined,
  DownOutlined,
  PlusOutlined,
  QuestionCircleOutlined,
  UpOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { SlotDef, StepInput } from '../core/model';

interface Props {
  inputs: StepInput[];
  slots: Record<string, SlotDef>;
  readonly?: boolean;
  label: string;
  tip: string;
  onChange: (inputs: StepInput[]) => void;
}

export default function InputMaterialsEditor({
  inputs,
  slots,
  readonly = false,
  label,
  tip,
  onChange,
}: Props) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set());
  const slotOptions = Object.values(slots).map((slot) => ({
    value: slot.id,
    label: slot.label ? `${slot.id} (${slot.label})` : slot.id,
  }));

  const updateAt = (index: number, patch: Partial<StepInput>) => {
    const next = inputs.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item);
    onChange(next);
  };

  return (
    <div className="npp-material-editor">
      <div className="npp-material-header">
        <span className="npp-field-label npp-material-title">
          {label}
          <Tooltip title={tip} placement="top">
            <QuestionCircleOutlined className="npp-tip-icon" />
          </Tooltip>
        </span>
        <Tooltip title={slotOptions.length === 0 ? t('selfEvolutionRun.stateGraphNoMaterial') : t('selfEvolutionRun.stateGraphAddInputMaterial')}>
          <Button
            type="text"
            size="small"
            className="npp-material-add-button"
            aria-label={t('selfEvolutionRun.stateGraphAddInputMaterial')}
            disabled={readonly || slotOptions.length === 0}
            icon={<PlusOutlined />}
            onClick={() => onChange([...inputs, { material: '', required: true }])}
          />
        </Tooltip>
      </div>

      <div className="npp-material-list">
        {inputs.map((item, itemIndex) => {
          const isExpanded = item.required && expanded.has(itemIndex);
          const alternatives = item.alternatives ?? [];
          const statusTip = item.required
            ? t('selfEvolutionRun.stateGraphAlternativeCount', { count: alternatives.length })
            : t('selfEvolutionRun.stateGraphOptionalMaterialTip');

          return (
            <div className="npp-material-item" key={itemIndex}>
              <div className="npp-material-row">
                <Select
                  size="small"
                  value={item.material || undefined}
                  options={slotOptions}
                  disabled={readonly}
                  showSearch
                  optionFilterProp="label"
                  placeholder={t('selfEvolutionRun.stateGraphArtifacts')}
                  className="npp-slot-select"
                  onChange={(material) => updateAt(itemIndex, { material })}
                />

                <Tooltip title={statusTip} placement="top">
                  <button
                    type="button"
                    className={`npp-material-status ${item.required ? 'is-required' : 'is-optional'}`}
                    disabled={readonly}
                    onClick={() => {
                      const required = !item.required;
                      updateAt(itemIndex, { required, ...(!required ? { alternatives: undefined } : {}) });
                    }}
                  >
                    {item.required
                      ? t('selfEvolutionRun.stateGraphSlotRequired')
                      : t('selfEvolutionRun.stateGraphSlotOptional')}
                  </button>
                </Tooltip>

                <Button
                  type="text"
                  size="small"
                  className="npp-material-icon-button"
                  disabled={!item.required}
                  aria-label={isExpanded
                    ? t('selfEvolutionRun.stateGraphCollapseAlternatives')
                    : t('selfEvolutionRun.stateGraphExpandAlternatives')}
                  icon={isExpanded ? <UpOutlined /> : <DownOutlined />}
                  onClick={() => {
                    setExpanded((current) => {
                      const next = new Set(current);
                      if (next.has(itemIndex)) next.delete(itemIndex);
                      else next.add(itemIndex);
                      return next;
                    });
                  }}
                />
                <Button
                  type="text"
                  danger
                  size="small"
                  className="npp-material-icon-button"
                  disabled={readonly}
                  aria-label={t('common.delete')}
                  icon={<CloseOutlined />}
                  onClick={() => {
                    setExpanded((current) => new Set(
                      [...current]
                        .filter((index) => index !== itemIndex)
                        .map((index) => index > itemIndex ? index - 1 : index),
                    ));
                    onChange(inputs.filter((_, index) => index !== itemIndex));
                  }}
                />
              </div>

              {isExpanded && (
                <div className="npp-material-alternatives">
                  {alternatives.map((material, alternativeIndex) => (
                    <div className="npp-material-alternative-row" key={alternativeIndex}>
                      <span className="npp-material-branch-line" aria-hidden="true" />
                      <Select
                        size="small"
                        value={material || undefined}
                        options={slotOptions}
                        disabled={readonly}
                        showSearch
                        optionFilterProp="label"
                        placeholder={t('selfEvolutionRun.stateGraphAlternativeMaterial')}
                        className="npp-slot-select"
                        onChange={(nextMaterial) => {
                          const nextAlternatives = [...alternatives];
                          nextAlternatives[alternativeIndex] = nextMaterial;
                          updateAt(itemIndex, { alternatives: nextAlternatives });
                        }}
                      />
                      <Button
                        type="text"
                        danger
                        size="small"
                        className="npp-material-icon-button"
                        disabled={readonly}
                        aria-label={t('common.delete')}
                        icon={<CloseOutlined />}
                        onClick={() => updateAt(itemIndex, {
                          alternatives: alternatives.filter((_, index) => index !== alternativeIndex),
                        })}
                      />
                    </div>
                  ))}
                  <Button
                    type="text"
                    size="small"
                    className="npp-add-alternative"
                    disabled={readonly || slotOptions.length === 0}
                    icon={<PlusOutlined />}
                    onClick={() => updateAt(itemIndex, { alternatives: [...alternatives, ''] })}
                  >
                    {t('selfEvolutionRun.stateGraphAddAlternativeMaterial')}
                  </Button>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
