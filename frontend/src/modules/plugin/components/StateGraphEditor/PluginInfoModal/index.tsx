import { useEffect, useState } from 'react';
import { Modal, Input, Button, Tooltip, message, Spin } from 'antd';
import { QuestionCircleOutlined } from '@ant-design/icons';
import type { PluginModel } from '../core/pluginModel';
import type { ScenarioData } from '../ScenarioEditor';
import { polishPluginInfo, type PolishableField } from '../../../pluginDraftApi';
import './index.scss';

const PLUGIN_ID_REGEX = /^[a-zA-Z][a-zA-Z0-9-_]*$/;

const POLISHABLE_FIELDS: PolishableField[] = ['description', 'when_to_use', 'overview', 'notes'];

const SparkleIcon = () => (
  <svg className="pim-sparkle-icon" viewBox="0 0 16 16" fill="currentColor" xmlns="http://www.w3.org/2000/svg">
    <path d="M8 1l1.2 3.8L13 6l-3.8 1.2L8 11l-1.2-3.8L3 6l3.8-1.2L8 1z" />
    <path d="M13 9l.6 1.9L15.5 12l-1.9.6L13 15l-.6-1.9L10.5 12l1.9-.6L13 9z" opacity="0.6" />
  </svg>
);

export interface PluginInfoModalProps {
  open: boolean;
  onCancel: () => void;
  pluginModel: PluginModel;
  scenarioData: ScenarioData;
  onSave?: (pm: PluginModel, sd: ScenarioData) => Promise<void>;
  readonly?: boolean;
}

export default function PluginInfoModal({ open, onCancel, pluginModel, scenarioData, onSave, readonly = false }: PluginInfoModalProps) {
  const [saving, setSaving] = useState(false);
  const [pluginId, setPluginId] = useState('');
  const [pluginName, setPluginName] = useState('');
  const [description, setDescription] = useState('');
  const [whenToUse, setWhenToUse] = useState('');
  const [overview, setOverview] = useState('');
  const [notes, setNotes] = useState('');
  const [idError, setIdError] = useState('');
  const [polishingFields, setPolishingFields] = useState<Set<PolishableField>>(new Set());
  const [polishingAll, setPolishingAll] = useState(false);

  useEffect(() => {
    if (open) {
      setPluginId(pluginModel.id || '');
      setPluginName(pluginModel.name || '');
      setDescription(pluginModel.description || '');
      setWhenToUse(pluginModel.when_to_use || '');
      setOverview(scenarioData.overview || '');
      setNotes(scenarioData.notes || '');
      setIdError('');
    }
  }, [open, pluginModel, scenarioData]);

  const validateId = (val: string) => {
    if (!val.trim()) return '插件标识不能为空';
    if (!PLUGIN_ID_REGEX.test(val.trim())) return '必须以英文字母开头，只能包含英文字母、数字、连字符和下划线';
    return '';
  };

  const getFieldValue = (field: PolishableField): string => {
    switch (field) {
      case 'description': return description;
      case 'when_to_use': return whenToUse;
      case 'overview': return overview;
      case 'notes': return notes;
    }
  };

  const setFieldValue = (field: PolishableField, value: string) => {
    switch (field) {
      case 'description': setDescription(value); break;
      case 'when_to_use': setWhenToUse(value); break;
      case 'overview': setOverview(value); break;
      case 'notes': setNotes(value); break;
    }
  };

  const handlePolishField = async (field: PolishableField) => {
    const value = getFieldValue(field);
    if (!value.trim()) return;

    setPolishingFields(prev => new Set(prev).add(field));
    try {
      const currentFields: Partial<Record<PolishableField, string>> = {
        description, when_to_use: whenToUse, overview, notes,
      };
      const result = await polishPluginInfo({ fields: currentFields, target_fields: [field] });
      if (result[field]) setFieldValue(field, result[field]!);
    } catch {
      message.error('润色失败，请稍后重试');
    } finally {
      setPolishingFields(prev => {
        const next = new Set(prev);
        next.delete(field);
        return next;
      });
    }
  };

  const handlePolishAll = async () => {
    const currentFields: Partial<Record<PolishableField, string>> = {
      description, when_to_use: whenToUse, overview, notes,
    };
    const targetFields = POLISHABLE_FIELDS.filter(f => (currentFields[f] || '').trim() !== '');
    if (targetFields.length === 0) return;

    setPolishingAll(true);
    try {
      const result = await polishPluginInfo({ fields: currentFields, target_fields: targetFields });
      for (const field of targetFields) {
        if (result[field]) setFieldValue(field, result[field]!);
      }
    } catch {
      message.error('一键润色失败，请稍后重试');
    } finally {
      setPolishingAll(false);
    }
  };

  const handleSave = async () => {
    const err = validateId(pluginId);
    if (err) {
      setIdError(err);
      return;
    }
    setSaving(true);
    try {
      const newPm: PluginModel = {
        ...pluginModel,
        id: pluginId.trim(),
        name: pluginName.trim(),
        description: description.trim(),
        when_to_use: whenToUse.trim(),
      };
      const newSd: ScenarioData = {
        ...scenarioData,
        overview: overview.trim(),
        notes: notes.trim(),
      };
      await onSave(newPm, newSd);
      onCancel();
    } catch {
      message.error('保存失败');
    } finally {
      setSaving(false);
    }
  };

  const isAnyPolishing = polishingAll || polishingFields.size > 0;

  const renderPolishIcon = (field: PolishableField, hasValue: boolean) => {
    if (readonly || !hasValue) return null;
    const isLoading = polishingFields.has(field);
    return (
      <Tooltip title="智能润色">
        <button
          className={`pim-polish-btn${isLoading ? ' pim-polish-btn--loading' : ''}`}
          onClick={() => handlePolishField(field)}
          disabled={isLoading || isAnyPolishing}
          type="button"
          aria-label="智能润色"
        >
          {isLoading ? <Spin size="small" /> : <SparkleIcon />}
        </button>
      </Tooltip>
    );
  };

  return (
    <Modal
      title="插件信息"
      open={open}
      onCancel={onCancel}
      width={560}
      footer={
        readonly ? (
          <div className="pim-footer">
            <Button onClick={onCancel}>关闭</Button>
          </div>
        ) : (
          <div className="pim-footer">
            <Button onClick={onCancel}>取消</Button>
            <Tooltip title="对所有非空字段一键智能润色">
              <Button
                className="pim-polish-all-btn"
                icon={<SparkleIcon />}
                loading={polishingAll}
                disabled={isAnyPolishing}
                onClick={handlePolishAll}
              >
                一键润色
              </Button>
            </Tooltip>
            <Button type="primary" loading={saving} onClick={handleSave}>保存</Button>
          </div>
        )
      }
      destroyOnClose
    >
      <div className="pim-body">
        {/* 插件标识 */}
        <div className="pim-row">
          <div className="pim-row-label">
            插件标识
            <Tooltip title="用于系统识别，英文字母开头，只含英文/数字/连字符/下划线">
              <QuestionCircleOutlined className="pim-tip-icon" />
            </Tooltip>
          </div>
          <div className="pim-row-input">
            <Input
              value={pluginId}
              readOnly={readonly}
              onChange={(e) => {
                if (readonly) return;
                setPluginId(e.target.value);
                setIdError(validateId(e.target.value));
              }}
              placeholder="在此输入插件标识，需有场景语义，如插件的英文名称"
              status={idError ? 'error' : undefined}
            />
            {idError && <span className="pim-field-error">{idError}</span>}
          </div>
        </div>

        {/* 显示名称 */}
        <div className="pim-row">
          <div className="pim-row-label">
            显示名称
            <Tooltip title="展示给用户看的名称">
              <QuestionCircleOutlined className="pim-tip-icon" />
            </Tooltip>
          </div>
          <div className="pim-row-input">
            <Input
              value={pluginName}
              readOnly={readonly}
              onChange={(e) => { if (!readonly) setPluginName(e.target.value); }}
              placeholder="例如：图片处理插件"
            />
          </div>
        </div>

        {/* 插件描述 */}
        <div className="pim-block">
          <div className="pim-block-label">
            插件描述
            {renderPolishIcon('description', !!description.trim())}
          </div>
          <Input.TextArea
            value={description}
            readOnly={readonly || polishingFields.has('description') || polishingAll}
            onChange={(e) => { if (!readonly) setDescription(e.target.value); }}
            placeholder="简短描述插件的用途…"
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 触发条件 */}
        <div className="pim-block">
          <div className="pim-block-label">
            触发条件（请用英文描述）
            <Tooltip title="描述什么情况下 AI 应该调用此插件">
              <QuestionCircleOutlined className="pim-tip-icon" />
            </Tooltip>
            {renderPolishIcon('when_to_use', !!whenToUse.trim())}
          </div>
          <Input.TextArea
            value={whenToUse}
            readOnly={readonly || polishingFields.has('when_to_use') || polishingAll}
            onChange={(e) => { if (!readonly) setWhenToUse(e.target.value); }}
            placeholder="Describe in English when this plugin should be triggered…"
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 场景描述 */}
        <div className="pim-block">
          <div className="pim-block-label">
            场景描述
            {renderPolishIcon('overview', !!overview.trim())}
          </div>
          <Input.TextArea
            value={overview}
            readOnly={readonly || polishingFields.has('overview') || polishingAll}
            onChange={(e) => { if (!readonly) setOverview(e.target.value); }}
            placeholder="描述该插件适用的业务场景…"
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 注意事项 */}
        <div className="pim-block">
          <div className="pim-block-label">
            注意事项
            {renderPolishIcon('notes', !!notes.trim())}
          </div>
          <Input.TextArea
            value={notes}
            readOnly={readonly || polishingFields.has('notes') || polishingAll}
            onChange={(e) => { if (!readonly) setNotes(e.target.value); }}
            placeholder="补充使用时需要注意的事项…"
            autoSize={{ minRows: 2, maxRows: 4 }}
          />
        </div>
      </div>
    </Modal>
  );
}
