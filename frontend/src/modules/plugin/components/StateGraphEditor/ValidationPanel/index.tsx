import { useMemo } from 'react';
import { Tooltip } from 'antd';
import { ExclamationCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { ValidationError } from '../core/validator';
import './index.scss';

interface Props {
  errors: ValidationError[];
  getTargetNodeId?: (error: ValidationError) => string | null;
  onSelectNode?: (nodeId: string) => void;
}

export default function ValidationPanel({ errors, getTargetNodeId, onSelectNode }: Props) {
  const { t } = useTranslation();
  const grouped = useMemo(() => {
    const map = new Map<string, ValidationError[]>();
    for (const err of errors) {
      const key = err.nodeId ?? '__global__';
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(err);
    }
    return map;
  }, [errors]);

  if (errors.length === 0) return null;

  return (
    <div className="validation-panel" role="alert" aria-label={t('selfEvolutionRun.validationPanelAriaLabel')}>
      <div className="validation-panel-header">
        <ExclamationCircleOutlined className="validation-panel-icon" />
        <span>{t('selfEvolutionRun.validationPanelErrorCount', { count: errors.length })}</span>
      </div>
      <ul className="validation-panel-list">
        {[...grouped.entries()].map(([groupKey, groupErrors]) =>
          groupErrors.map((err, index) => {
            const targetNodeId = getTargetNodeId?.(err) ?? err.nodeId ?? null;
            const message = t(`selfEvolutionRun.validationErrors.${err.code}`, {
              defaultValue: err.message,
              node: err.nodeId ?? '',
              edge: err.edgeKey ?? '',
              material: err.materialId ?? '',
              producer: String(err.details?.producer_step_id ?? ''),
            });
            return (
            <li key={`${groupKey}-${err.code}-${index}`} className="validation-panel-item">
              <CloseCircleOutlined className="validation-panel-item-icon" />
              <Tooltip title={targetNodeId ? t('selfEvolutionRun.validationPanelLocateNode', { code: err.code }) : err.code}>
                <button
                  type="button"
                  className={`validation-panel-item-text${targetNodeId ? ' is-locatable' : ''}`}
                  disabled={!targetNodeId}
                  onClick={() => {
                    if (targetNodeId) onSelectNode?.(targetNodeId);
                  }}
                >
                  {message}
                </button>
              </Tooltip>
            </li>
            );
          }),
        )}
      </ul>
    </div>
  );
}
