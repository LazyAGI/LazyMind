import { useState } from 'react';
import { Button, Checkbox, Input, InputNumber, Select, Tooltip, Empty } from 'antd';
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
  cardinality: 'single' | 'list';
  ordered: boolean;
  allow_manual_add: boolean;
  summary_max_chars: string;
  idError?: string;
}

const EMPTY_DRAFT: DraftArtifact = {
  id: '',
  type: 'text',
  label: '',
  cardinality: 'single',
  ordered: false,
  allow_manual_add: true,
  summary_max_chars: '',
};

/** Returns true if any step node uses slotId as an input. */
function isUsedAsInput(model: GraphModel, slotId: string): boolean {
  return model.nodes.some((n) => n.inputs.includes(slotId));
}

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
    const isList = draft.cardinality === 'list';
    const maxChars = parseInt(draft.summary_max_chars, 10);
    const newSlot: SlotDef = {
      id: draft.id,
      type: draft.type,
      label: draft.label || undefined,
      cardinality: isList ? 'list' : undefined,
      ordered: (isList && draft.ordered) ? true : undefined,
      allow_manual_add: isList ? draft.allow_manual_add : undefined,
      summary_max_chars: (!isNaN(maxChars) && maxChars > 0) ? maxChars : undefined,
    };
    const newSlots = { ...model.slots, [draft.id]: newSlot };
    onModelChange({ ...model, slots: newSlots });
    setDraft(EMPTY_DRAFT);
    setAdding(false);
  };

  const handleDelete = (id: string) => {
    const newSlots = { ...model.slots };
    delete newSlots[id];
    const newNodes = model.nodes.map((n) => ({
      ...n,
      inputs: n.inputs.filter((s) => s !== id),
      outputs: n.outputs.filter((s) => s !== id),
    }));
    onModelChange({ ...model, slots: newSlots, nodes: newNodes });
  };

  const updateArtifact = (id: string, patch: Partial<Omit<SlotDef, 'id'>>) => {
    const current = model.slots[id];
    const updated: SlotDef = { ...current, ...patch };
    // When switching back to single, clear list-only fields
    if ('cardinality' in patch && patch.cardinality !== 'list') {
      updated.cardinality = undefined;
      updated.ordered = undefined;
      updated.allow_manual_add = undefined;
    }
    onModelChange({ ...model, slots: { ...model.slots, [id]: updated } });
  };

  // Resolve effective allow_manual_add: explicit value wins, otherwise derive from usage.
  const resolveAllowManualAdd = (art: SlotDef): boolean => {
    if (art.allow_manual_add !== undefined) return art.allow_manual_add;
    return isUsedAsInput(model, art.id);
  };

  return (
    <div className="artifact-panel" role="complementary" aria-label="素材管理" onDoubleClick={(e) => e.stopPropagation()}>
      <div className="artifact-panel-header">
        <span className="artifact-panel-title">素材</span>
        <Button type="text" icon={<CloseOutlined />} size="small" onClick={onClose} aria-label="关闭素材面板" />
      </div>

      <div className="artifact-panel-desc">
        素材是步骤之间传递的东西，可以是文字、图片、文件等任何形式的内容。
        你也可以在这里定义用户一开始就提供的素材（如上传的图片、填写的文字等）。
      </div>

      <div className="artifact-panel-body">
        {artifacts.length === 0 && !adding && (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description="暂无素材定义"
            style={{ margin: '24px 0' }}
          />
        )}

        {artifacts.map((art) => (
          <div key={art.id} className="artifact-row">
            <div className="artifact-row-id">
              <Tooltip title={art.id}>
                <code>{art.id}</code>
              </Tooltip>
            </div>
            <div className="artifact-row-fields">
              <div className="artifact-row-main">
                <Select
                  size="small"
                  value={art.type}
                  options={TYPE_OPTIONS}
                  onChange={(val) => updateArtifact(art.id, { type: val })}
                  className="artifact-type-select"
                />
                <Input
                  size="small"
                  value={art.label ?? ''}
                  onChange={(e) => updateArtifact(art.id, { label: e.target.value || undefined })}
                  placeholder="素材名称（可选）"
                  className="artifact-label-input"
                />
              </div>
              <div className="artifact-row-flags">
                <Checkbox
                  checked={art.cardinality === 'list'}
                  onChange={(e) =>
                    updateArtifact(art.id, { cardinality: e.target.checked ? 'list' : 'single' })
                  }
                >
                  列表
                </Checkbox>
                {art.cardinality === 'list' && (
                  <>
                    <Checkbox
                      checked={!!art.ordered}
                      onChange={(e) => updateArtifact(art.id, { ordered: e.target.checked || undefined })}
                    >
                      有序
                    </Checkbox>
                    <Checkbox
                      checked={resolveAllowManualAdd(art)}
                      onChange={(e) => updateArtifact(art.id, { allow_manual_add: e.target.checked })}
                    >
                      允许手动添加
                    </Checkbox>
                  </>
                )}
              </div>
              <div className="artifact-row-extra">
                <span className="artifact-extra-label">摘要字数上限</span>
                <InputNumber
                  size="small"
                  min={0}
                  value={art.summary_max_chars ?? null}
                  onChange={(val) =>
                    updateArtifact(art.id, { summary_max_chars: val ?? undefined })
                  }
                  placeholder="不限"
                  className="artifact-summary-input"
                />
              </div>
            </div>
            <Tooltip title="删除素材（同时移除节点引用）">
              <Button
                type="text"
                danger
                size="small"
                icon={<DeleteOutlined />}
                onClick={() => handleDelete(art.id)}
                aria-label={`删除素材 ${art.id}`}
                className="artifact-row-delete"
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
              placeholder="素材标识（英文/数字/下划线）"
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
                className="artifact-type-select"
              />
              <Input
                size="small"
                value={draft.label}
                onChange={(e) => setDraft((d) => ({ ...d, label: e.target.value }))}
                placeholder="素材名称（可选）"
                className="artifact-label-input"
              />
            </div>
            <div className="artifact-add-flags">
              <Checkbox
                checked={draft.cardinality === 'list'}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, cardinality: e.target.checked ? 'list' : 'single' }))
                }
              >
                列表
              </Checkbox>
              {draft.cardinality === 'list' && (
                <>
                  <Checkbox
                    checked={draft.ordered}
                    onChange={(e) => setDraft((d) => ({ ...d, ordered: e.target.checked }))}
                  >
                    有序
                  </Checkbox>
                  <Checkbox
                    checked={draft.allow_manual_add}
                    onChange={(e) => setDraft((d) => ({ ...d, allow_manual_add: e.target.checked }))}
                  >
                    允许手动添加
                  </Checkbox>
                </>
              )}
            </div>
            <div className="artifact-add-extra">
              <span className="artifact-extra-label">摘要字数上限</span>
              <InputNumber
                size="small"
                min={0}
                value={draft.summary_max_chars ? parseInt(draft.summary_max_chars, 10) : null}
                onChange={(val) =>
                  setDraft((d) => ({ ...d, summary_max_chars: val != null ? String(val) : '' }))
                }
                placeholder="不限"
                className="artifact-summary-input"
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
            添加素材
          </Button>
        </div>
      )}
    </div>
  );
}
