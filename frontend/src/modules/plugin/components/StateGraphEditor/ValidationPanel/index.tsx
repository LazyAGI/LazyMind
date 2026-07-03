import { useMemo } from 'react';
import { Tooltip } from 'antd';
import { ExclamationCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import type { ValidationError } from '../core/validator';
import './index.scss';

interface Props {
  errors: ValidationError[];
  onSelectNode?: (nodeId: string) => void;
}

export default function ValidationPanel({ errors, onSelectNode }: Props) {
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
    <div className="validation-panel" role="alert" aria-label="校验错误列表">
      <div className="validation-panel-header">
        <ExclamationCircleOutlined className="validation-panel-icon" />
        <span>发现 {errors.length} 个校验错误</span>
      </div>
      <ul className="validation-panel-list">
        {[...grouped.entries()].map(([groupKey, groupErrors]) =>
          groupErrors.map((err) => (
            <li key={`${groupKey}-${err.code}`} className="validation-panel-item">
              <CloseCircleOutlined className="validation-panel-item-icon" />
              <Tooltip title={err.code}>
                <button
                  type="button"
                  className="validation-panel-item-text"
                  onClick={() => {
                    if (err.nodeId) onSelectNode?.(err.nodeId);
                  }}
                >
                  {err.message}
                </button>
              </Tooltip>
            </li>
          )),
        )}
      </ul>
    </div>
  );
}
