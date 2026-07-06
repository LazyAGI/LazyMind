import { useState } from 'react';
import { Button, Checkbox, Input, Select, Tooltip } from 'antd';
import {
  CloseOutlined,
  PlusOutlined,
  QuestionCircleOutlined,
  DownOutlined,
  RightOutlined,
  DeleteOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { StepNode, GraphModel } from '../core/model';
import { VIRTUAL_END, isHiddenId } from '../core/model';
import './NodePropertiesPanel.scss';

const STEP_ID_REGEX = /^[a-zA-Z0-9_]+$/;

interface Props {
  node: StepNode;
  model: GraphModel;
  onClose: () => void;
  /** Returns false when the change was rejected (e.g. duplicate id). */
  onChange: (updated: StepNode) => boolean;
  onDelete: (nodeId: string) => void;
  /** When true the "添加分支" button is disabled (node is a parallel-fork child). */
  disableAddTransition?: boolean;
}

interface SectionProps {
  title: string;
  defaultOpen?: boolean;
  children: React.ReactNode;
}

function Section({ title, defaultOpen = true, children }: SectionProps) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="npp-section">
      <button className="npp-section-header" onClick={() => setOpen((v) => !v)}>
        <span className="npp-section-title">{title}</span>
        {open ? <DownOutlined className="npp-section-icon" /> : <RightOutlined className="npp-section-icon" />}
      </button>
      {open && <div className="npp-section-body">{children}</div>}
    </div>
  );
}

function LabelWithTip({ label, tip }: { label: string; tip: string }) {
  return (
    <span className="npp-field-label">
      {label}
      <Tooltip title={tip} placement="top">
        <QuestionCircleOutlined className="npp-tip-icon" />
      </Tooltip>
    </span>
  );
}

function FieldRow({ label, tip, children }: { label: string; tip: string; children: React.ReactNode }) {
  return (
    <div className="npp-field-row">
      <LabelWithTip label={label} tip={tip} />
      <div className="npp-field-control">{children}</div>
    </div>
  );
}

export default function NodePropertiesPanel({ node, model, onClose, onChange, onDelete, disableAddTransition }: Props) {
  const { t } = useTranslation();
  // Derive allowSkip directly from node.skipif so it stays in sync when node
  // prop updates. Using local state here caused the checkbox and model to
  // diverge, leading to crashes on the second toggle.
  const allowSkip = node.skipif !== undefined;
  // Display value for the id field: hidden-placeholder ids are shown as empty string.
  const [idDraft, setIdDraft] = useState<string>(isHiddenId(node.id) ? '' : node.id);
  // Set when the upstream rejects the id (e.g. duplicate).
  const [idConflict, setIdConflict] = useState(false);

  // Keep draft in sync when node.id changes from outside (e.g. undo, external rename),
  // but only when the user isn't actively typing.
  const [idFocused, setIdFocused] = useState(false);
  const displayedNodeId = isHiddenId(node.id) ? '' : node.id;
  if (!idFocused && idDraft !== displayedNodeId) {
    setIdDraft(displayedNodeId);
    setIdConflict(false);
  }

  const slotOptions = Object.keys(model.slots).map((id) => ({
    label: model.slots[id].label ? `${id} (${model.slots[id].label})` : id,
    value: id,
  }));

  const update = (patch: Partial<StepNode>) => onChange({ ...node, ...patch });

  // Commit the id draft upstream; revert and show conflict if rejected.
  const commitIdDraft = () => {
    const accepted = update({ id: idDraft });
    if (!accepted) {
      // Canvas rejected the id (e.g. duplicate) — revert to the current node id.
      setIdDraft(displayedNodeId);
      setIdConflict(true);
    } else {
      setIdConflict(false);
    }
  };

  // Error: non-empty draft with invalid chars, reserved prefix, or rejected by Canvas.
  const stepIdError = idConflict || !!(idDraft && (!STEP_ID_REGEX.test(idDraft) || idDraft.startsWith('.hid')));

  return (
    <div className="node-props-panel" role="complementary" aria-label="步骤设置" onDoubleClick={(e) => e.stopPropagation()}>
      {/* header */}
      <div className="node-props-panel-header">
        <span className="node-props-panel-title">步骤设置</span>
        <Button type="text" icon={<CloseOutlined />} size="small" onClick={onClose} aria-label="关闭属性面板" />
      </div>

      {/* body */}
      <div className="node-props-panel-body">
        {/* ── 分组一：基本信息 ── */}
        <Section title="基本信息">
          <FieldRow label="步骤标识" tip="用于代码引用，仅支持英文字母、数字和下划线">
            <Input
              value={idDraft}
              status={stepIdError ? 'error' : undefined}
              onChange={(e) => {
                setIdDraft(e.target.value);
                setIdConflict(false);
              }}
              onFocus={() => setIdFocused(true)}
              onBlur={() => {
                setIdFocused(false);
                commitIdDraft();
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') commitIdDraft();
              }}
              placeholder="步骤唯一标识"
              size="small"
            />
            {stepIdError && (
              <span className="npp-field-error">
                {idConflict
                  ? '步骤标识已存在，请使用其他名称'
                  : idDraft.startsWith('.hid')
                  ? '不能以 .hid 开头'
                  : '只能包含英文字母、数字和下划线'}
              </span>
            )}
          </FieldRow>
          <FieldRow label="展示名称" tip="在画布上展示的名称，例如：审核文档">
            <Input
              value={node.label}
              onChange={(e) => update({ label: e.target.value })}
              placeholder="步骤展示名称"
              size="small"
            />
          </FieldRow>
          <FieldRow label="执行方式" tip="人工审批：需要人工介入确认后才能继续；自动执行：由系统自动完成，无需人工干预">
            <Select
              value={node.mode}
              options={[
                { label: t('selfEvolutionRun.stateGraphModeHumanDesc'), value: 'human' },
                { label: t('selfEvolutionRun.stateGraphModeAutoDesc'), value: 'auto' },
              ]}
              onChange={(val) => update({ mode: val })}
              size="small"
              style={{ width: '100%' }}
            />
          </FieldRow>
        </Section>

        {/* ── 分组二：素材 ── */}
        <Section title="素材">
          <div className="npp-field-block">
            <LabelWithTip label="用到的素材" tip="本步骤执行时需要读取的素材" />
            <Select
              mode="multiple"
              value={node.inputs}
              options={slotOptions}
              onChange={(val) => update({ inputs: val })}
              placeholder={Object.keys(model.slots).length === 0 ? '请先在工具栏添加素材' : '请选择用到的素材'}
              allowClear
              size="small"
              disabled={Object.keys(model.slots).length === 0}
              style={{ width: '100%', marginTop: 4 }}
              notFoundContent={<span className="npp-hint">暂无素材，请先添加</span>}
            />
          </div>
          <div className="npp-field-block" style={{ marginTop: 10 }}>
            <LabelWithTip label="产出的素材" tip="本步骤执行完毕后会写入的素材" />
            <Select
              mode="multiple"
              value={node.outputs}
              options={slotOptions}
              onChange={(val) => update({ outputs: val })}
              placeholder={Object.keys(model.slots).length === 0 ? '请先在工具栏添加素材' : '请选择产出的素材'}
              allowClear
              size="small"
              disabled={Object.keys(model.slots).length === 0}
              style={{ width: '100%', marginTop: 4 }}
              notFoundContent={<span className="npp-hint">暂无素材，请先添加</span>}
            />
          </div>
        </Section>

        {/* ── 分组三：执行流程 ── */}
        <Section title="执行流程">
          {/* 完成后前往：表头与行合并 */}
          <div className="npp-field-block">
            <div className="npp-transitions-header-row">
              <LabelWithTip label="完成后前往" tip="本步骤完成后跳转的下一步骤及条件" />
              {node.transitions.length > 0 && (
                <span className="node-props-transition-col-label col-condition-title">条件（满足什么情况时）</span>
              )}
            </div>
            <div className="npp-transitions" style={{ marginTop: 6 }}>
              {node.transitions.map((tr, idx) => (
                <div key={idx} className="node-props-transition-row">
                  <Select
                    value={tr.to}
                    options={[
                      ...model.nodes.filter((n) => n.id !== node.id).map((n) => ({ label: n.label, value: n.id })),
                      { label: '结束', value: VIRTUAL_END },
                    ]}
                    onChange={(val) => {
                      const next = [...node.transitions];
                      next[idx] = { ...tr, to: val };
                      update({ transitions: next });
                    }}
                    style={{ flex: 1 }}
                    size="small"
                    placeholder="选择下一步骤"
                  />
                  <Input
                    value={tr.condition}
                    onChange={(e) => {
                      const next = [...node.transitions];
                      next[idx] = { ...tr, condition: e.target.value };
                      update({ transitions: next });
                    }}
                    style={{ flex: 2, marginLeft: 4 }}
                    size="small"
                    placeholder="（选填）满足什么情况时进入"
                  />
                  <Button
                    type="text"
                    danger
                    size="small"
                    icon={<CloseOutlined />}
                    onClick={() => update({ transitions: node.transitions.filter((_, i) => i !== idx) })}
                    aria-label="删除分支"
                  />
                </div>
              ))}
              <Tooltip title={disableAddTransition ? '并行分支的子步骤不允许再有多个出口（禁止二次分叉）' : undefined}>
                <Button
                  type="dashed"
                  size="small"
                  icon={<PlusOutlined />}
                  block
                  disabled={disableAddTransition}
                  onClick={() => update({ transitions: [...node.transitions, { to: '', condition: '' }] })}
                >
                  添加分支
                </Button>
              </Tooltip>
            </div>
          </div>

          {/* 流程推进方式：仅在有多个后继时显示 */}
          {node.transitions.length > 1 && (
            <FieldRow label="流程推进方式" tip="全部触发：同时触发所有满足条件的出口（并行）；选择一个：只走第一个满足条件的出口">
              <Select
                value={node.route ?? 'all'}
                options={[
                  { label: '全部触发（并行）', value: 'all' },
                  { label: '选择一个（第一个满足条件的）', value: 'choice' },
                ]}
                onChange={(val) => update({ route: val })}
                size="small"
                style={{ width: '100%' }}
              />
            </FieldRow>
          )}

          {/* 允许跳过 */}
          <div className="npp-skip-section">
            <Checkbox
              checked={allowSkip}
              onChange={(e) => {
                if (!e.target.checked) {
                  update({ skipif: undefined });
                } else {
                  // Enable allowSkip by setting skipif to empty string as placeholder.
                  update({ skipif: '' });
                }
              }}
            >
              <span className="npp-skip-label">允许跳过</span>
              <Tooltip title="满足此条件时，跳过本步骤直接执行后继节点" placement="top">
                <QuestionCircleOutlined className="npp-tip-icon" />
              </Tooltip>
            </Checkbox>
            {allowSkip && (
              <Input
                className="npp-skip-input"
                value={node.skipif ?? ''}
                onChange={(e) => update({ skipif: e.target.value })}
                placeholder="例如：用户已提供大纲"
                size="small"
              />
            )}
          </div>
        </Section>
      </div>

      {/* footer */}
      <div className="node-props-panel-footer">
        <Button danger size="small" block icon={<DeleteOutlined />} onClick={() => onDelete(node.id)}>
          删除此步骤
        </Button>
      </div>
    </div>
  );
}