import { useState } from 'react';
import { Button, Input, Select, Tooltip, Empty } from 'antd';
import { PlusOutlined, DeleteOutlined, CloseOutlined } from '@ant-design/icons';
import type { SlotDef, GraphModel } from '../core/model';
import './index.scss';

const ARTIFACT_ID_REGEX = /^[a-zA-Z0-9_]+$/;

const TYPE_OPTIONS = [
  { label: '文本', value: 'text' },
  { label: '图片', value: 'image' },
  { label: '文件', value: 'file' },
  { label: 'JSON', value: 'json' },
];

interface Props {
  model: GraphModel;
  onClose: () => void;
  onModelChange: (model: GraphModel) => void;
}

interface DraftArtifact {
  id: string;
  type: string;
  label: string;
  idError?: string;
}

const EMPTY_DRAFT: DraftArtifact = { id: '', type: 'text', label: '' };

export default function ArtifactPanel({ model, onClose, onModelChange }: Props) {
  const [draft, setDraft] = useState<DraftArtifact>(EMPTY_DRAFT);
  const [adding, setAdding] = useState(false);

  const artifacts = Object.values(model.slots);

  const validateId = (id: string): string | undefined => {
    if (!id.trim()) return 'ID 不能为空';
    if (!ARTIFACT_ID_REGEX.test(id)) return 'ID 只能包含英文字母、数字和下划线';
    if (model.slots[id]) return '该 ID 已存在';
    return undefined;
  };

  const handleAdd = () => {
    const idError = validateId(draft.id);
    if (idError) {
      setDraft((d) => ({ ...d, idError }));
      return;
    }
    const newSlot: SlotDef = { id: draft.id, type: draft.type, label: draft.label || undefined };
    const newSlots = { ...model.slots, [draft.id]: newSlot };
    onModelChange({ ...model, slots: newSlots });
    setDraft(EMPTY_DRAFT);
    setAdding(false);
  };

  const handleDelete = (id: string) => {
    const newSlots = { ...model.slots };
    delete newSlots[id];
    // Also remove references from nodes
    const newNodes = model.nodes.map((n) => ({
      ...n,
      inputs: n.inputs.filter((s) => s !== id),
      outputs: n.outputs.filter((s) => s !== id),
    }));
    onModelChange({ ...model, slots: newSlots, nodes: newNodes });
  };

  const updateArtifact = (id: string, patch: Partial<Omit<SlotDef, 'id'>>) => {
    const updated: SlotDef = { ...model.slots[id], ...patch };
    onModelChange({ ...model, slots: { ...model.slots, [id]: updated } });
  };

  return (
    <div className="artifact-panel" role="complementary" aria-label="成果管理" onDoubleClick={(e) => e.stopPropagation()}>
      <div className="artifact-panel-header">
        <span className="artifact-panel-title">成果 (Artifacts)</span>
        <Button type="text" icon={<CloseOutlined />} size="small" onClick={onClose} aria-label="关闭成果面板" />
      </div>

      <div className="artifact-panel-desc">
        成果是步骤间传递的数据单元，对应 YAML 中的 <code>slots</code> 字段。
      </div>

      <div className="artifact-panel-body">
        {artifacts.length === 0 && !adding && (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description="暂无成果定义"
            style={{ margin: '24px 0' }}
          />
        )}

        {artifacts.map((art) => (
          <div key={art.id} className="artifact-row">
            <div className="artifact-row-id">
              <code>{art.id}</code>
            </div>
            <Select
              size="small"
              value={art.type}
              options={TYPE_OPTIONS}
              onChange={(val) => updateArtifact(art.id, { type: val })}
              style={{ width: 80 }}
            />
            <Input
              size="small"
              value={art.label ?? ''}
              onChange={(e) => updateArtifact(art.id, { label: e.target.value || undefined })}
              placeholder="显示名（可选）"
              style={{ flex: 1 }}
            />
            <Tooltip title="删除成果（同时移除节点引用）">
              <Button
                type="text"
                danger
                size="small"
                icon={<DeleteOutlined />}
                onClick={() => handleDelete(art.id)}
                aria-label={`删除成果 ${art.id}`}
              />
            </Tooltip>
          </div>
        ))}

        {adding && (
          <div className="artifact-add-form">
            <Input
              size="small"
              value={draft.id}
              onChange={(e) => setDraft((d) => ({ ...d, id: e.target.value, idError: undefined }))}
              placeholder="成果 ID（英文/数字/下划线）"
              status={draft.idError ? 'error' : ''}
              onPressEnter={handleAdd}
              autoFocus
            />
            {draft.idError && <div className="artifact-id-error">{draft.idError}</div>}
            <div className="artifact-add-row2">
              <Select
                size="small"
                value={draft.type}
                options={TYPE_OPTIONS}
                onChange={(val) => setDraft((d) => ({ ...d, type: val }))}
                style={{ width: 90 }}
              />
              <Input
                size="small"
                value={draft.label}
                onChange={(e) => setDraft((d) => ({ ...d, label: e.target.value }))}
                placeholder="显示名（可选）"
                style={{ flex: 1 }}
              />
            </div>
            <div className="artifact-add-actions">
              <Button size="small" type="primary" onClick={handleAdd}>确认添加</Button>
              <Button size="small" onClick={() => { setAdding(false); setDraft(EMPTY_DRAFT); }}>取消</Button>
            </div>
          </div>
        )}
      </div>

      {!adding && (
        <div className="artifact-panel-footer">
          <Button
            type="dashed"
            size="small"
            icon={<PlusOutlined />}
            block
            onClick={() => setAdding(true)}
          >
            添加成果
          </Button>
        </div>
      )}
    </div>
  );
}
