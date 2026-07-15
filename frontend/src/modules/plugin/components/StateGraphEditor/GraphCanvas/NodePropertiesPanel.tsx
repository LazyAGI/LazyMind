import { useState, useEffect } from 'react';
import { Button, Checkbox, Input, Select, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';
import {
  CloseOutlined,
  PlusOutlined,
  QuestionCircleOutlined,
  DownOutlined,
  RightOutlined,
  DeleteOutlined,
} from '@ant-design/icons';
import type { StepNode, GraphModel } from '../core/model';
import {
  VIRTUAL_END,
  VIRTUAL_START,
  isHiddenId,
} from '../core/model';
import type { PluginModel } from '../core/pluginModel';
import type { ScenarioData } from '../ScenarioEditor';
import PromptEditor from './PromptEditor';
import InputMaterialsEditor from './InputMaterialsEditor';
import SkipConditionEditor from './SkipConditionEditor';
import { listToolAssets } from '@/modules/memory/toolApi';
import './NodePropertiesPanel.scss';

const STEP_ID_REGEX = /^[a-zA-Z0-9_]+$/;

// Module-level cache so the tool list is fetched once per session.
let _cachedSystemTools: Array<{ label: string; name: string }> | null = null;
let _systemToolsRequest: Promise<Array<{ label: string; name: string }>> | null = null;
let _systemToolsRetryAfter = 0;
const SYSTEM_TOOLS_RETRY_DELAY_MS = 30_000;

function loadSystemTools(): Promise<Array<{ label: string; name: string }>> {
  if (_cachedSystemTools) return Promise.resolve(_cachedSystemTools);
  if (_systemToolsRequest) return _systemToolsRequest;
  if (Date.now() < _systemToolsRetryAfter) return Promise.resolve([]);

  _systemToolsRequest = listToolAssets({ silentError: true })
    .then((tools) => {
      _cachedSystemTools = tools.map((tool) => ({
        label: tool.name || tool.id,
        name: tool.id,
      }));
      return _cachedSystemTools;
    })
    .catch(() => {
      _systemToolsRetryAfter = Date.now() + SYSTEM_TOOLS_RETRY_DELAY_MS;
      return [];
    })
    .finally(() => {
      _systemToolsRequest = null;
    });
  return _systemToolsRequest;
}

interface Props {
  node: StepNode;
  model: GraphModel;
  /** Plugin metadata model — provides tool function lists. */
  pluginModel?: PluginModel;
  /** Scenario data — provides step descriptions edited inline. */
  scenarioData?: ScenarioData;
  onScenarioChange?: (data: ScenarioData) => void;
  onClose: () => void;
  /** Returns false when the change was rejected (e.g. duplicate id). */
  onChange: (updated: StepNode) => boolean;
  onDelete: (nodeId: string) => void;
  /** When true the "添加分支" button is disabled (node is a parallel-fork child). */
  disableAddTransition?: boolean;
  /** When true all editing controls are disabled (read-only view). */
  readonly?: boolean;
  visualContent?: React.ReactNode;
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

export default function NodePropertiesPanel({ node, model, pluginModel, scenarioData, onScenarioChange, onClose, onChange, onDelete, disableAddTransition, readonly = false, visualContent }: Props) {
  const { t } = useTranslation();
  // Derive allowSkip directly from node state so it stays in sync when node
  // prop updates. Using local state here caused the checkbox and model to
  // diverge, leading to crashes on the second toggle.
  const allowSkip = node.skipIf !== undefined || node.legacySkipIf !== undefined;
  // Display value for the id field: hidden-placeholder ids are shown as empty string.
  const [idDraft, setIdDraft] = useState<string>(isHiddenId(node.id) ? '' : node.id);
  // Set when the upstream rejects the id (e.g. duplicate).
  const [idConflict, setIdConflict] = useState(false);
  const [activeTab, setActiveTab] = useState<'step' | 'visual'>('step');

  const [systemTools, setSystemTools] = useState<Array<{ label: string; name: string }>>(_cachedSystemTools ?? []);

  useEffect(() => {
    let active = true;
    void loadSystemTools().then((tools) => {
      if (active) setSystemTools(tools);
    });
    return () => {
      active = false;
    };
  }, []);

  // Keep draft in sync when node.id changes from outside (e.g. undo, external rename),
  // but only when the user isn't actively typing.
  const [idFocused, setIdFocused] = useState(false);
  const displayedNodeId = isHiddenId(node.id) ? '' : node.id;
  useEffect(() => {
    if (idFocused) return;
    setIdDraft((current) => current === displayedNodeId ? current : displayedNodeId);
    setIdConflict(false);
  }, [displayedNodeId, idFocused]);

  // Slot options: all defined slots
  const slotOptions = Object.keys(model.slots).map((id) => ({
    label: model.slots[id].label ? `${id} (${model.slots[id].label})` : id,
    value: id,
  }));

  // Slots already used as outputs by this node
  const usedOutputSlots = new Set(node.outputs.map((r) => r.material).filter(Boolean));
  // Available (unused) slot options for inputs and outputs
  const availableOutputSlots = slotOptions.filter((o) => !usedOutputSlots.has(o.value));

  // Grouped tool options: system tools first, then plugin script functions.
  const pluginFunctions: string[] = pluginModel?.tool_scripts
    ? pluginModel.tool_scripts.flatMap((ts) => ts.functions)
    : [];
  const toolFunctionOptions: Array<{ label: string; options: { label: string; value: string }[] }> = [];
  if (systemTools.length > 0) {
    toolFunctionOptions.push({
       label: t('selfEvolutionRun.nodePropsSysTools'),
      options: systemTools.map((t) => ({ label: `${t.label} (${t.name})`, value: t.name })),
    });
  }
  if (pluginFunctions.length > 0) {
    toolFunctionOptions.push({
       label: t('selfEvolutionRun.nodePropsPluginTools'),
      options: pluginFunctions.map((fn) => ({ label: fn, value: fn })),
    });
  }
  // Flat fallback when both groups are empty (e.g. loading), keep existing values selectable.
  const flatFallbackOptions: Array<{ label: string; options: { label: string; value: string }[] }> =
    toolFunctionOptions.length === 0 && (node.tools?.length ?? 0) > 0
       ? [{ label: t('selfEvolutionRun.nodePropsSelectedTools'), options: (node.tools ?? []).map((t: string) => ({ label: t, value: t })) }]
      : [];

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

  // __start__ virtual node: render a minimal panel with only flow/route controls.
  if (node.id === VIRTUAL_START) {
    return (
      <div className="node-props-panel" role="complementary" aria-label={t('selfEvolutionRun.nodePropsStartNodeAriaLabel')} onDoubleClick={(e) => e.stopPropagation()}>
        <div className="node-props-panel-header">
          <span className="node-props-panel-title">{t('selfEvolutionRun.nodePropsStartNodeTitle')}</span>
          <Button type="text" icon={<CloseOutlined />} size="small" onClick={onClose} aria-label={t('selfEvolutionRun.nodePropsCloseAriaLabel')} />
        </div>
        <div className="node-props-panel-body">
          <Section title={t('selfEvolutionRun.stateGraphSectionFlow')}>
            <div className="npp-transitions">
              {node.transitions.map((tr, idx) => (
                <div key={idx} className="npp-transition-row">
                  <Select
                    value={tr.to || undefined}
                    options={model.nodes.filter((n) => n.id !== VIRTUAL_END).map((n) => ({ label: n.label || n.id, value: n.id }))}
                    onChange={(val) => {
                      const next = [...node.transitions];
                      next[idx] = { ...tr, to: val };
                      update({ transitions: next });
                    }}
                    placeholder={t('selfEvolutionRun.stateGraphFlowTargetPlaceholder')}
                    style={{ flex: 1 }}
                    size="small"
                  />
                  <div style={{ flex: 2, marginLeft: 4 }}>
                    <Input
                      value={tr.when ?? ''}
                      disabled={readonly}
                      size="small"
                      placeholder={t('selfEvolutionRun.stateGraphFlowConditionPlaceholder')}
                      onChange={(event) => {
                        const next = [...node.transitions];
                        next[idx] = { ...tr, when: event.target.value || undefined, condition: undefined };
                        update({ transitions: next });
                      }}
                    />
                  </div>
                  <Button
                    type="text"
                    danger
                    size="small"
                    icon={<CloseOutlined />}
                    onClick={() => update({ transitions: node.transitions.filter((_, i) => i !== idx) })}
                  />
                </div>
              ))}
              <Button
                type="dashed"
                size="small"
                icon={<PlusOutlined />}
                block
                onClick={() => update({ transitions: [...node.transitions, { to: '' }] })}
              >
                {t('selfEvolutionRun.stateGraphAddBranch')}
              </Button>
            </div>
            {node.transitions.length > 1 && (
              <FieldRow label={t('selfEvolutionRun.stateGraphRouteMode')} tip={t('selfEvolutionRun.stateGraphRouteModeTip')}>
                <Select
                  value={node.route ?? 'all'}
                  options={[
                    { label: t('selfEvolutionRun.stateGraphRouteModeAll'), value: 'all' },
                    { label: t('selfEvolutionRun.stateGraphRouteModeChoice'), value: 'choice' },
                  ]}
                  onChange={(val) => update({ route: val })}
                  size="small"
                  style={{ width: '100%' }}
                />
              </FieldRow>
            )}
          </Section>
        </div>
      </div>
    );
  }

  return (
    <div className="node-props-panel" role="complementary" aria-label={t('selfEvolutionRun.stateGraphPanelTitle')} onDoubleClick={(e) => e.stopPropagation()}>
      {/* header */}
      <div className="node-props-panel-header">
        <div className="node-props-tabs"><button className={activeTab === 'step' ? 'active' : ''} onClick={() => setActiveTab('step')}>步骤设置</button><button className={activeTab === 'visual' ? 'active' : ''} onClick={() => setActiveTab('visual')}>视觉效果</button></div>
        <Button type="text" icon={<CloseOutlined />} size="small" onClick={onClose} aria-label={t('selfEvolutionRun.stateGraphPanelTitle')} />
      </div>

      {/* body */}
      {activeTab === 'visual' ? visualContent : <><div className="node-props-panel-body">
        {/* ── 分组一：基本信息 ── */}
        <Section title={t('selfEvolutionRun.stateGraphBasicInfo')}>
          <FieldRow label={t('selfEvolutionRun.stateGraphFieldStepId')} tip={t('selfEvolutionRun.stateGraphFieldStepIdTip')}>
            <Input
              value={idDraft}
              status={stepIdError ? 'error' : undefined}
              readOnly={readonly}
              onChange={(e) => {
                if (readonly) return;
                setIdDraft(e.target.value);
                setIdConflict(false);
              }}
              onFocus={() => setIdFocused(true)}
              onBlur={() => {
                setIdFocused(false);
                if (!readonly) commitIdDraft();
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !readonly) commitIdDraft();
              }}
              placeholder={t('selfEvolutionRun.stateGraphFieldStepIdPlaceholder')}
              size="small"
            />
            {stepIdError && (
              <span className="npp-field-error">
                {idConflict
                  ? t('selfEvolutionRun.stateGraphFieldStepIdConflict')
                  : idDraft.startsWith('.hid')
                  ? t('selfEvolutionRun.stateGraphFieldStepIdHidPrefix')
                  : t('selfEvolutionRun.stateGraphFieldStepIdInvalid')}
              </span>
            )}
          </FieldRow>
          <FieldRow label={t('selfEvolutionRun.stateGraphFieldLabel')} tip={t('selfEvolutionRun.stateGraphFieldLabelTip')}>
            <Input
              value={node.label}
              readOnly={readonly}
              onChange={(e) => { if (!readonly) update({ label: e.target.value }); }}
              placeholder={t('selfEvolutionRun.stateGraphFieldLabelPlaceholder')}
              size="small"
            />
          </FieldRow>
          <FieldRow label={t('selfEvolutionRun.stateGraphFieldDesc')} tip={t('selfEvolutionRun.stateGraphFieldDescTip')}>
            <Input.TextArea
              value={scenarioData?.stepDescriptions[node.id] ?? ''}
              readOnly={readonly}
              onChange={(e) => {
                if (readonly) return;
                if (onScenarioChange && scenarioData) {
                  onScenarioChange({
                    ...scenarioData,
                    stepDescriptions: { ...scenarioData.stepDescriptions, [node.id]: e.target.value },
                  });
                }
              }}
              placeholder={t('selfEvolutionRun.stateGraphFieldDescPlaceholder')}
              autoSize={{ minRows: 2, maxRows: 4 }}
              size="small"
              style={{ marginTop: 4 }}
            />
          </FieldRow>
          <FieldRow label={t('selfEvolutionRun.stateGraphExecutionMode')} tip={t('selfEvolutionRun.stateGraphFieldModeTip')}>
            <Select
              value={node.mode}
              disabled={readonly}
              options={[
                { label: t('selfEvolutionRun.stateGraphModeHumanDesc'), value: 'human' },
                { label: t('selfEvolutionRun.stateGraphModeAutoDesc'), value: 'auto' },
              ]}
              onChange={(val) => { if (!readonly) update({ mode: val }); }}
              size="small"
              style={{ width: '100%' }}
            />
          </FieldRow>
        </Section>

        {/* ── 分组二：素材 ── */}
        <Section title={t('selfEvolutionRun.stateGraphSectionMaterials')}>
          <div className="npp-field-block">
            <InputMaterialsEditor
              inputs={node.inputs}
              slots={model.slots}
              readonly={readonly}
              label={t('selfEvolutionRun.stateGraphArtifactInputs')}
              tip={t('selfEvolutionRun.stateGraphInputsTip')}
              onChange={(inputs) => update({ inputs })}
            />
          </div>
          <div className="npp-field-block" style={{ marginTop: 10 }}>
            <div className="npp-material-header">
              <LabelWithTip label={t('selfEvolutionRun.stateGraphArtifactOutputs')} tip={t('selfEvolutionRun.stateGraphOutputsTip')} />
              <Tooltip title={slotOptions.length === 0
                ? t('selfEvolutionRun.stateGraphNoMaterial')
                : availableOutputSlots.length === 0
                ? t('selfEvolutionRun.stateGraphAllMaterialsUsed')
                : t('selfEvolutionRun.stateGraphAddOutputMaterial')}>
                <Button
                  type="text"
                  size="small"
                  className="npp-material-add-button"
                  aria-label={t('selfEvolutionRun.stateGraphAddOutputMaterial')}
                  icon={<PlusOutlined />}
                  disabled={readonly || availableOutputSlots.length === 0}
                  onClick={() => { if (!readonly) update({ outputs: [...node.outputs, { material: '', required: true }] }); }}
                />
              </Tooltip>
            </div>
            {node.outputs.map((ref, idx) => {
              const slotLabel = slotOptions.find((o) => o.value === ref.material)?.label ?? ref.material;
              return (
                <div key={idx} className="npp-material-row npp-output-material-row">
                  <Tooltip title={slotLabel} placement="top">
                    <Select
                      value={ref.material}
                      options={slotOptions}
                      optionRender={(opt) => (
                        <Tooltip title={String(opt.label)} placement="left" mouseEnterDelay={0.3}>
                          <span className="npp-select-option-text">{opt.label}</span>
                        </Tooltip>
                      )}
                      onChange={(val) => {
                        const next = [...node.outputs];
                        next[idx] = { ...ref, material: val };
                        update({ outputs: next });
                      }}
                      placeholder={t('selfEvolutionRun.stateGraphArtifacts')}
                      size="small"
                      className="npp-slot-select"
                    />
                  </Tooltip>
                  <Button
                    type="text"
                    danger
                    size="small"
                    className="npp-material-icon-button"
                    icon={<CloseOutlined />}
                    disabled={readonly}
                    onClick={() => { if (!readonly) update({ outputs: node.outputs.filter((_, i) => i !== idx) }); }}
                  />
                </div>
              );
            })}
          </div>
        </Section>

        {/* ── 分组三：执行流程 ── */}
        <Section title={t('selfEvolutionRun.stateGraphSectionFlow')}>
          <div className="npp-field-block">
            <div className="npp-transitions-header-row">
              <LabelWithTip label={t('selfEvolutionRun.stateGraphFlowNext')} tip={t('selfEvolutionRun.stateGraphFlowNextTip')} />
              {node.transitions.length > 0 && (
                <span className="node-props-transition-col-label col-condition-title">{t('selfEvolutionRun.stateGraphFlowConditionLabel')}</span>
              )}
            </div>
            <div className="npp-transitions" style={{ marginTop: 6 }}>
              {node.transitions.map((tr, idx) => {
                const transitionOptions = [
                  ...model.nodes.filter((n) => n.id !== node.id).map((n) => ({ label: n.label, value: n.id })),
                  { label: t('selfEvolutionRun.stateGraphFlowEnd'), value: VIRTUAL_END },
                ];
                return (
                <div key={idx} className="node-props-transition-row">
                  <Select
                    value={tr.to}
                    options={transitionOptions}
                    optionRender={(opt) => (
                      <Tooltip title={String(opt.label)} placement="left" mouseEnterDelay={0.3}>
                        <span className="npp-select-option-text">{opt.label}</span>
                      </Tooltip>
                    )}
                    onChange={(val) => {
                      const next = [...node.transitions];
                      next[idx] = { ...tr, to: val };
                      update({ transitions: next });
                    }}
                    className="npp-slot-select"
                    size="small"
                    placeholder={t('selfEvolutionRun.stateGraphFlowNextPlaceholder')}
                  />
                  <div style={{ flex: 2, marginLeft: 4, minWidth: 0 }}>
                    <Input
                      value={tr.when ?? ''}
                      disabled={readonly}
                      size="small"
                      placeholder={t('selfEvolutionRun.stateGraphFlowConditionPlaceholder')}
                      onChange={(event) => {
                        const next = [...node.transitions];
                        next[idx] = { ...tr, when: event.target.value || undefined, condition: undefined };
                        update({ transitions: next });
                      }}
                    />
                  </div>
                  <Button
                    type="text"
                    danger
                    size="small"
                    icon={<CloseOutlined />}
                    onClick={() => update({ transitions: node.transitions.filter((_, i) => i !== idx) })}
                    aria-label={t('selfEvolutionRun.stateGraphAddBranch')}
                  />
                </div>
              ); })}
              <Tooltip title={disableAddTransition ? t('selfEvolutionRun.stateGraphAddBranchDisabledTip') : undefined}>
                <Button
                  type="dashed"
                  size="small"
                  icon={<PlusOutlined />}
                  block
                  disabled={disableAddTransition}
                  onClick={() => update({ transitions: [...node.transitions, { to: '' }] })}
                >
                  {t('selfEvolutionRun.stateGraphAddBranch')}
                </Button>
              </Tooltip>
            </div>
          </div>

          {node.transitions.length > 1 && (
            <FieldRow label={t('selfEvolutionRun.stateGraphRouteMode')} tip={t('selfEvolutionRun.stateGraphRouteModeTip')}>
              <Select
                value={node.route ?? 'all'}
                options={[
                  { label: t('selfEvolutionRun.stateGraphRouteModeAll'), value: 'all' },
                  { label: t('selfEvolutionRun.stateGraphRouteModeChoice'), value: 'choice' },
                ]}
                onChange={(val) => update({ route: val })}
                size="small"
                style={{ width: '100%' }}
              />
            </FieldRow>
          )}

          <div className="npp-skip-section">
            <Checkbox
              checked={allowSkip}
              onChange={(e) => {
                if (!e.target.checked) {
                  update({ skipIf: undefined, legacySkipIf: undefined });
                } else {
                  update({ skipIf: { all: [{ material: '' }] }, legacySkipIf: undefined });
                }
              }}
            >
              <span className="npp-skip-label">{t('selfEvolutionRun.stateGraphAllowSkip')}</span>
              <Tooltip title={t('selfEvolutionRun.stateGraphAllowSkipTip')} placement="top">
                <QuestionCircleOutlined className="npp-tip-icon" />
              </Tooltip>
            </Checkbox>
            {allowSkip && (
              <div className="npp-skip-input">
                <SkipConditionEditor
                  value={node.skipIf}
                  slots={model.slots}
                  readonly={readonly}
                  onChange={(skipIf) => update({ skipIf, legacySkipIf: undefined })}
                />
                {node.legacySkipIf && !node.skipIf && (
                  <span className="npp-field-error">旧自然语言条件不可执行：{node.legacySkipIf}</span>
                )}
              </div>
            )}
          </div>
        </Section>

        {/* ── 分组四：执行逻辑 ── */}
        <Section title={t('selfEvolutionRun.stateGraphSectionLogic')}>
          <div className="npp-field-block">
            <LabelWithTip label={t('selfEvolutionRun.stateGraphFieldPrompt')} tip={t('selfEvolutionRun.stateGraphFieldPromptTip')} />
            <PromptEditor
              value={node.prompt ?? ''}
              onChange={(val) => update({ prompt: val || undefined })}
              slots={Object.values(model.slots)}
              placeholder={t('selfEvolutionRun.stateGraphFieldPromptPlaceholder')}
            />
          </div>
          <div className="npp-field-block" style={{ marginTop: 10 }}>
            <LabelWithTip label={t('selfEvolutionRun.stateGraphFieldTools')} tip={t('selfEvolutionRun.stateGraphFieldToolsTip')} />
            <Select
              mode="tags"
              value={node.tools ?? []}
              options={toolFunctionOptions.length > 0 ? toolFunctionOptions : flatFallbackOptions}
              disabled={readonly}
              onChange={(val) => { if (!readonly) update({ tools: val.length > 0 ? val : undefined }); }}
              placeholder={t('selfEvolutionRun.stateGraphFieldToolsPlaceholder')}
              size="small"
              style={{ width: '100%', marginTop: 4 }}
            />
          </div>
          <div className="npp-field-block" style={{ marginTop: 10 }}>
            <LabelWithTip label={t('selfEvolutionRun.stateGraphFieldCriteria')} tip={t('selfEvolutionRun.stateGraphFieldCriteriaTip')} />
            <Input.TextArea
              value={node.acceptanceCriteria ?? ''}
              onChange={(e) => update({ acceptanceCriteria: e.target.value || undefined })}
              placeholder={t('selfEvolutionRun.stateGraphFieldCriteriaPlaceholder')}
              autoSize={{ minRows: 2, maxRows: 4 }}
              style={{ marginTop: 4 }}
            />
          </div>
        </Section>
      </div>

      {/* footer */}
      <div className="node-props-panel-footer">
        {!readonly && (
          <Button danger size="small" block icon={<DeleteOutlined />} onClick={() => onDelete(node.id)}>
            {t('selfEvolutionRun.stateGraphDeleteStep')}
          </Button>
        )}
      </div>
      </>}
    </div>
  );
}
