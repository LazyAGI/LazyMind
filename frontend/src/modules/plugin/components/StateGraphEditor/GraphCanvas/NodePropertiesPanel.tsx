import { Button, Form, Input, Select, Divider } from 'antd';
import { CloseOutlined, PlusOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { StepNode, GraphModel } from '../core/model';
import './NodePropertiesPanel.scss';

const STEP_ID_REGEX = /^[a-zA-Z0-9_]+$/;

interface Props {
  node: StepNode;
  model: GraphModel;
  onClose: () => void;
  onChange: (updated: StepNode) => void;
  onDelete: (nodeId: string) => void;
}

export default function NodePropertiesPanel({ node, model, onClose, onChange, onDelete }: Props) {
  const { t } = useTranslation();
  const slotOptions = Object.keys(model.slots).map((id) => ({
    label: model.slots[id].label ? `${id} (${model.slots[id].label})` : id,
    value: id,
  }));

  const update = (patch: Partial<StepNode>) => {
    onChange({ ...node, ...patch });
  };

  return (
    <div className="node-props-panel" role="complementary" aria-label="节点属性" onDoubleClick={(e) => e.stopPropagation()}>
      <div className="node-props-panel-header">
        <span className="node-props-panel-title">节点属性</span>
        <Button
          type="text"
          icon={<CloseOutlined />}
          size="small"
          onClick={onClose}
          aria-label="关闭属性面板"
        />
      </div>

      <div className="node-props-panel-body">
        <Form layout="vertical" size="small">
          <Form.Item
            label="步骤 ID"
            validateStatus={node.id && !STEP_ID_REGEX.test(node.id) ? 'error' : ''}
            help={node.id && !STEP_ID_REGEX.test(node.id) ? '步骤 ID 只能包含英文字母、数字和下划线' : undefined}
          >
            <Input
              value={node.id}
              onChange={(e) => update({ id: e.target.value })}
              placeholder="步骤唯一ID"
            />
          </Form.Item>

          <Form.Item label="显示标签">
            <Input
              value={node.label}
              onChange={(e) => update({ label: e.target.value })}
              placeholder="节点显示名称"
            />
          </Form.Item>

          <Form.Item label={t('selfEvolutionRun.stateGraphExecutionMode')}>
            <Select
              value={node.mode}
              options={[
                { label: t('selfEvolutionRun.stateGraphModeHumanDesc'), value: 'human' },
                { label: t('selfEvolutionRun.stateGraphModeAutoDesc'), value: 'auto' },
              ]}
              onChange={(val) => update({ mode: val })}
            />
          </Form.Item>

          <Divider style={{ margin: '8px 0' }} />

          <Form.Item
            label="输入成果"
            extra={Object.keys(model.slots).length === 0 ? <span style={{ fontSize: 11, color: '#bfbfbf' }}>请先在工具栏添加成果</span> : undefined}
          >
            <Select
              mode="multiple"
              value={node.inputs}
              options={slotOptions}
              onChange={(val) => update({ inputs: val })}
              placeholder="选择输入成果"
              allowClear
              notFoundContent={<span style={{ fontSize: 12, color: '#bfbfbf' }}>暂无成果，请先添加</span>}
            />
          </Form.Item>

          <Form.Item
            label="输出成果"
            extra={Object.keys(model.slots).length === 0 ? <span style={{ fontSize: 11, color: '#bfbfbf' }}>请先在工具栏添加成果</span> : undefined}
          >
            <Select
              mode="multiple"
              value={node.outputs}
              options={slotOptions}
              onChange={(val) => update({ outputs: val })}
              placeholder="选择输出成果"
              allowClear
              notFoundContent={<span style={{ fontSize: 12, color: '#bfbfbf' }}>暂无成果，请先添加</span>}
            />
          </Form.Item>

          <Divider style={{ margin: '8px 0' }} />

          <Form.Item label="转移条件">
            <div className="node-props-transitions">
              {node.transitions.map((t, idx) => (
                <div key={idx} className="node-props-transition-row">
                  <Select
                    value={t.to}
                    options={[
                      ...model.nodes.filter((n) => n.id !== node.id).map((n) => ({ label: n.label, value: n.id })),
                      { label: '__end__', value: '__end__' },
                    ]}
                    onChange={(val) => {
                      const next = [...node.transitions];
                      next[idx] = { ...t, to: val };
                      update({ transitions: next });
                    }}
                    style={{ flex: 1 }}
                    placeholder="目标节点"
                  />
                  <Input
                    value={t.condition}
                    onChange={(e) => {
                      const next = [...node.transitions];
                      next[idx] = { ...t, condition: e.target.value };
                      update({ transitions: next });
                    }}
                    style={{ flex: 2, marginLeft: 4 }}
                    placeholder="条件描述"
                  />
                  <Button
                    type="text"
                    danger
                    size="small"
                    icon={<CloseOutlined />}
                    onClick={() => {
                      update({ transitions: node.transitions.filter((_, i) => i !== idx) });
                    }}
                    aria-label="删除转移条件"
                  />
                </div>
              ))}
              <Button
                type="dashed"
                size="small"
                icon={<PlusOutlined />}
                block
                onClick={() => update({ transitions: [...node.transitions, { to: '', condition: '' }] })}
              >
                添加转移
              </Button>
            </div>
          </Form.Item>
        </Form>

        <div className="node-props-panel-footer">
          <Button danger size="small" block onClick={() => onDelete(node.id)}>
            删除此节点
          </Button>
        </div>
      </div>
    </div>
  );
}
