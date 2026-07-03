import { memo } from 'react';
import { Handle, Position } from '@xyflow/react';
import type { NodeProps } from '@xyflow/react';
import { Tag, Tooltip } from 'antd';
import { RobotOutlined, UserOutlined } from '@ant-design/icons';
import type { ValidationError } from '../core/validator';

export interface StepNodeData extends Record<string, unknown> {
  id: string;
  label: string;
  mode: 'human' | 'auto';
  inputs: string[];
  outputs: string[];
  transitions: { to: string; condition: string }[];
  hasError: boolean;
  errorMessages: string[];
}

function StepNodeComponent({ data, selected }: NodeProps) {
  const nodeData = data as unknown as StepNodeData;
  const { hasError, errorMessages, mode, label, id } = nodeData;

  return (
    <Tooltip
      title={hasError ? errorMessages.join('\n') : undefined}
      placement="top"
    >
      <div
        className={`step-node ${selected ? 'is-selected' : ''} ${hasError ? 'has-error' : ''}`}
        aria-label={`步骤节点: ${String(label)}`}
      >
        <Handle type="target" position={Position.Left} className="step-node-handle" />

        <div className="step-node-header">
          <span className="step-node-id">{String(id)}</span>
          <Tag
            className="step-node-mode-tag"
            icon={mode === 'auto' ? <RobotOutlined /> : <UserOutlined />}
            color={mode === 'auto' ? 'blue' : 'orange'}
          >
            {mode === 'auto' ? 'auto' : 'human'}
          </Tag>
        </div>
        <div className="step-node-label">{String(label)}</div>

        <Handle type="source" position={Position.Right} className="step-node-handle" />
      </div>
    </Tooltip>
  );
}

export const StepNodeRenderer = memo(StepNodeComponent);

// Virtual terminal node: __start__ or __end__
export function TerminalNode({ data }: NodeProps) {
  const nodeData = data as unknown as { type: 'start' | 'end'; label: string };
  const isStart = nodeData.type === 'start';
  return (
    <div className={`terminal-node terminal-node--${nodeData.type}`} aria-label={nodeData.label}>
      {!isStart && <Handle type="target" position={Position.Left} className="step-node-handle" />}
      <div className="terminal-node-dot" />
      <span className="terminal-node-label">{nodeData.label}</span>
      {isStart && <Handle type="source" position={Position.Right} className="step-node-handle" />}
    </div>
  );
}

// Helper: build node error map from validation errors
export function buildNodeErrorMap(errors: ValidationError[]): Map<string, string[]> {
  const map = new Map<string, string[]>();
  for (const err of errors) {
    if (!err.nodeId) continue;
    if (!map.has(err.nodeId)) map.set(err.nodeId, []);
    map.get(err.nodeId)!.push(err.message);
  }
  return map;
}
