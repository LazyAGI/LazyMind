import { Button, Form, Input, Select, Divider } from 'antd';
import { CloseOutlined, PlusOutlined } from '@ant-design/icons';
import type { StepNode, GraphModel } from '../core/model';
import './NodePropertiesPanel.scss';

interface Props {
  node: StepNode;
  model: GraphModel;
  onClose: () => void;
  onChange: (updated: StepNode) => void;
  onDelete: (nodeId: string) => void;
}

export default function NodePropertiesPanel({ node, model, onClose, onChange, onDelete }: Props) {
  const slotOptions = Object.keys(model.slots).map((id) => ({ label: id, value: id }));

  // Slots produced by topology-prior nodes (simplified: all slots not in own outputs)
  const availableInputSlots = slotOptions.filter((o) => !node.outputs.includes(o.value));

  const update = (patch: Partial<StepNode>) => {
    onChange({ ...node, ...patch });
  };

  return (
    <div className="node-props-panel" role="complementary" aria-label="节点属性">
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
          <Form.Item label="步骤 ID">
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

          <Form.Item label="执行模式">
            <Select
              value={node.mode}
              options={[
                { label: 'human（人工）', value: 'human' },
                { label: 'auto（自动）', value: 'auto' },
              ]}
              onChange={(val) => update({ mode: val })}
            />
          </Form.Item>

          <Divider style={{ margin: '8px 0' }} />

          <Form.Item label="输入 Slots">
            <Select
              mode="multiple"
              value={node.inputs}
              options={availableInputSlots}
              onChange={(val) => update({ inputs: val })}
              placeholder="选择输入 slot"
              allowClear
            />
          </Form.Item>

          <Form.Item label="输出 Slots">
            <Select
              mode="multiple"
              value={node.outputs}
              options={slotOptions}
              onChange={(val) => update({ outputs: val })}
              placeholder="选择输出 slot"
              allowClear
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
