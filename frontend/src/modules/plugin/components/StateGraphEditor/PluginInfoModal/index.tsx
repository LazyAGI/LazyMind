import { useEffect, useState } from 'react';
import { Modal, Input, Button, Tooltip, message, Spin } from 'antd';
import { QuestionCircleOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
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
  const { t } = useTranslation();
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
    if (!val.trim()) return t('selfEvolutionRun.pluginInfoIdRequired');
    if (!PLUGIN_ID_REGEX.test(val.trim())) return t('selfEvolutionRun.pluginInfoIdInvalid');
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
      message.error(t('selfEvolutionRun.pluginInfoPolishFailed'));
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
      message.error(t('selfEvolutionRun.pluginInfoPolishAllFailed'));
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
      if (onSave) await onSave(newPm, newSd);
      onCancel();
    } catch {
      message.error(t('selfEvolutionRun.pluginInfoSaveFailed'));
    } finally {
      setSaving(false);
    }
  };

  const isAnyPolishing = polishingAll || polishingFields.size > 0;

  const renderPolishIcon = (field: PolishableField, hasValue: boolean) => {
    if (readonly || !hasValue) return null;
    const isLoading = polishingFields.has(field);
    return (
      <Tooltip title={t('selfEvolutionRun.pluginInfoPolishTooltip')}>
        <button
          className={`pim-polish-btn${isLoading ? ' pim-polish-btn--loading' : ''}`}
          onClick={() => handlePolishField(field)}
          disabled={isLoading || isAnyPolishing}
          type="button"
          aria-label={t('selfEvolutionRun.pluginInfoPolishTooltip')}
        >
          {isLoading ? <Spin size="small" /> : <SparkleIcon />}
        </button>
      </Tooltip>
    );
  };

  return (
    <Modal
      title={t('selfEvolutionRun.pluginInfoModalTitle')}
      open={open}
      onCancel={onCancel}
      width={560}
      footer={
        readonly ? (
          <div className="pim-footer">
            <Button onClick={onCancel}>{t('selfEvolutionRun.pluginInfoCloseBtn')}</Button>
          </div>
        ) : (
          <div className="pim-footer">
            <Button onClick={onCancel}>{t('selfEvolutionRun.pluginInfoCancelBtn')}</Button>
            <Tooltip title={t('selfEvolutionRun.pluginInfoPolishAllTooltip')}>
              <Button
                className="pim-polish-all-btn"
                icon={<SparkleIcon />}
                loading={polishingAll}
                disabled={isAnyPolishing}
                onClick={handlePolishAll}
              >
                {t('selfEvolutionRun.pluginInfoPolishAllBtn')}
              </Button>
            </Tooltip>
            <Button type="primary" loading={saving} onClick={handleSave}>{t('selfEvolutionRun.pluginInfoSaveBtn')}</Button>
          </div>
        )
      }
      destroyOnClose
    >
      <div className="pim-body">
        {/* 插件标识 */}
        <div className="pim-row">
          <div className="pim-row-label">
            {t('selfEvolutionRun.pluginInfoFieldPluginId')}
            <Tooltip title={t('selfEvolutionRun.pluginInfoFieldPluginIdTooltip')}>
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
              placeholder={t('selfEvolutionRun.pluginInfoFieldPluginIdPlaceholder')}
              status={idError ? 'error' : undefined}
            />
            {idError && <span className="pim-field-error">{idError}</span>}
          </div>
        </div>

        {/* 显示名称 */}
        <div className="pim-row">
          <div className="pim-row-label">
            {t('selfEvolutionRun.pluginInfoFieldDisplayName')}
            <Tooltip title={t('selfEvolutionRun.pluginInfoFieldDisplayNameTooltip')}>
              <QuestionCircleOutlined className="pim-tip-icon" />
            </Tooltip>
          </div>
          <div className="pim-row-input">
            <Input
              value={pluginName}
              readOnly={readonly}
              onChange={(e) => { if (!readonly) setPluginName(e.target.value); }}
              placeholder={t('selfEvolutionRun.pluginInfoExamplePlaceholder')}
            />
          </div>
        </div>

        {/* 插件描述 */}
        <div className="pim-block">
          <div className="pim-block-label">
            {t('selfEvolutionRun.pluginInfoFieldDescription')}
            {renderPolishIcon('description', !!description.trim())}
          </div>
          <Input.TextArea
            value={description}
            readOnly={readonly || polishingFields.has('description') || polishingAll}
            onChange={(e) => { if (!readonly) setDescription(e.target.value); }}
            placeholder={t('selfEvolutionRun.pluginInfoFieldDescriptionPlaceholder')}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 触发条件 */}
        <div className="pim-block">
          <div className="pim-block-label">
            {t('selfEvolutionRun.pluginInfoFieldWhenToUse')}
            <Tooltip title={t('selfEvolutionRun.pluginInfoFieldWhenToUseTooltip')}>
              <QuestionCircleOutlined className="pim-tip-icon" />
            </Tooltip>
            {renderPolishIcon('when_to_use', !!whenToUse.trim())}
          </div>
          <Input.TextArea
            value={whenToUse}
            readOnly={readonly || polishingFields.has('when_to_use') || polishingAll}
            onChange={(e) => { if (!readonly) setWhenToUse(e.target.value); }}
            placeholder={t('selfEvolutionRun.pluginInfoFieldWhenToUsePlaceholder')}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 场景描述 */}
        <div className="pim-block">
          <div className="pim-block-label">
            {t('selfEvolutionRun.pluginInfoFieldOverview')}
            {renderPolishIcon('overview', !!overview.trim())}
          </div>
          <Input.TextArea
            value={overview}
            readOnly={readonly || polishingFields.has('overview') || polishingAll}
            onChange={(e) => { if (!readonly) setOverview(e.target.value); }}
            placeholder={t('selfEvolutionRun.pluginInfoFieldOverviewPlaceholder')}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 注意事项 */}
        <div className="pim-block">
          <div className="pim-block-label">
            {t('selfEvolutionRun.pluginInfoFieldNotes')}
            {renderPolishIcon('notes', !!notes.trim())}
          </div>
          <Input.TextArea
            value={notes}
            readOnly={readonly || polishingFields.has('notes') || polishingAll}
            onChange={(e) => { if (!readonly) setNotes(e.target.value); }}
            placeholder={t('selfEvolutionRun.pluginInfoFieldNotesPlaceholder')}
            autoSize={{ minRows: 2, maxRows: 4 }}
          />
        </div>
      </div>
    </Modal>
  );
}
