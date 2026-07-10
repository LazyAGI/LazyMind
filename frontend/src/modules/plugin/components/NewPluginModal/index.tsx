import { useEffect, useState } from 'react';
import { Modal, Input, Button, Select, Tooltip, message } from 'antd';
import { FileTextOutlined, ThunderboltOutlined, BulbOutlined, QuestionCircleOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { createPluginDraft, aiGeneratePluginDraft, updatePluginDraftContent, deletePluginDraft } from '../../pluginDraftApi';
import { listSkillAssetsPage } from '@/modules/memory/skillApi';
import { serializePluginModel } from '../StateGraphEditor/core/pluginSerializer';
import { createEmptyPluginModel } from '../StateGraphEditor/core/pluginModel';
import './index.scss';

const PLUGIN_ID_REGEX = /^[a-zA-Z][a-zA-Z0-9-_]*$/;

type CreateMode = 'blank' | 'ai' | 'skill';

interface NewPluginModalProps {
  open: boolean;
  onCancel: () => void;
  onCreated: (draftId: string) => void;
}

export default function NewPluginModal({ open, onCancel, onCreated }: NewPluginModalProps) {
  const { t } = useTranslation();

  const MODE_CARDS: { mode: CreateMode; icon: React.ReactNode; title: string; desc: string; badge?: string }[] = [
    {
      mode: 'ai',
      icon: <BulbOutlined />,
      title: t('selfEvolutionRun.newPluginModeAiTitle'),
      desc: t('selfEvolutionRun.newPluginModeAiDesc'),
    },
    {
      mode: 'skill',
      icon: <ThunderboltOutlined />,
      title: t('selfEvolutionRun.newPluginModeSkillTitle'),
      desc: t('selfEvolutionRun.newPluginModeSkillDesc'),
    },
    {
      mode: 'blank',
      icon: <FileTextOutlined />,
      title: t('selfEvolutionRun.newPluginModeBlankTitle'),
      desc: t('selfEvolutionRun.newPluginModeBlankDesc'),
      badge: t('selfEvolutionRun.newPluginModeBlankBadge'),
    },
  ];
  const [mode, setMode] = useState<CreateMode>('ai');

  // skill mode: selected skill
  const [skillId, setSkillId] = useState<string | undefined>(undefined);
  const [skillName, setSkillName] = useState('');
  const [skillOptions, setSkillOptions] = useState<{ label: string; value: string }[]>([]);
  const [skillLoading, setSkillLoading] = useState(false);

  // fields shown after skill is selected (or always for ai/blank)
  const [pluginId, setPluginId] = useState('');
  const [idError, setIdError] = useState('');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');

  const [creating, setCreating] = useState(false);

  // For skill mode: fields appear only after skill is chosen
  const skillSelected = mode === 'skill' && !!skillId;
  const showFields = mode === 'ai' || mode === 'blank' || skillSelected;

  const reset = () => {
    setMode('ai');
    setSkillId(undefined);
    setSkillName('');
    setSkillOptions([]);
    setPluginId('');
    setIdError('');
    setName('');
    setDescription('');
  };

  // When a skill is selected, auto-fill pluginId with a slugified skill name
  useEffect(() => {
    if (skillId && skillName) {
      const slug = skillName
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '')
        .slice(0, 48);
      setPluginId(slug || '');
      setIdError('');
    }
  }, [skillId, skillName]);

  const handleCancel = () => {
    reset();
    onCancel();
  };

  const handleSkillSearch = async (keyword: string) => {
    setSkillLoading(true);
    try {
      const result = await listSkillAssetsPage({ keyword, page: 1, pageSize: 20, excludeBuiltinTemplates: true });
      setSkillOptions(result.records.map((r) => ({ label: r.name, value: r.id })));
    } catch {
      // ignore
    } finally {
      setSkillLoading(false);
    }
  };

  const handleSkillChange = (val: string, option: { label: string; value: string } | { label: string; value: string }[]) => {
    setSkillId(val);
    const opt = Array.isArray(option) ? option[0] : option;
    setSkillName(opt?.label ?? '');
  };

  const handleModeChange = (newMode: CreateMode) => {
    setMode(newMode);
    // Reset detail fields when switching mode
    setSkillId(undefined);
    setSkillName('');
    setPluginId('');
    setIdError('');
    setName('');
    setDescription('');
  };

  const handleCreate = async () => {
    const trimmedId = pluginId.trim();
    if (!trimmedId) {
      setIdError(t('selfEvolutionRun.newPluginIdErrorEmpty'));
      return;
    }
    if (!PLUGIN_ID_REGEX.test(trimmedId)) {
      setIdError(t('selfEvolutionRun.newPluginIdErrorInvalid'));
      return;
    }
    if (mode === 'ai' && !description.trim()) {
      message.warning(t('selfEvolutionRun.newPluginDescRequired'));
      return;
    }
    if (mode === 'skill' && !skillId) {
      message.warning(t('selfEvolutionRun.newPluginSkillRequired'));
      return;
    }

    // Display name falls back to plugin id if empty
    const effectiveName = name.trim() || trimmedId;

    setCreating(true);
    let draftId: string | undefined;
    try {
      const draft = await createPluginDraft({ name: effectiveName, source_type: mode });
      draftId = draft.id;
      const pm = { ...createEmptyPluginModel(), id: trimmedId, name: effectiveName };
      await updatePluginDraftContent(draft.id, {
        plugin_yaml_content: serializePluginModel(pm),
      });
      if (mode === 'ai') {
        await aiGeneratePluginDraft(draft.id, { description: description.trim() });
      } else if (mode === 'skill' && skillId) {
        await aiGeneratePluginDraft(draft.id, { skill_id: skillId });
      }
      draftId = undefined;
      reset();
      onCreated(draft.id);
    } catch {
      message.error(t('selfEvolutionRun.newPluginCreateFailed'));
      if (draftId) {
        deletePluginDraft(draftId).catch(() => {});
      }
    } finally {
      setCreating(false);
    }
  };

  const canCreate = showFields && pluginId.trim() !== '' && !idError;

  return (
    <Modal
      title={t('selfEvolutionRun.newPluginModalTitle')}
      open={open}
      onCancel={handleCancel}
      footer={
        <div className="npm-footer">
          <Button onClick={handleCancel}>{t('selfEvolutionRun.newPluginCancelBtn')}</Button>
          <Button type="primary" loading={creating} disabled={!canCreate} onClick={() => void handleCreate()}>
            {t('selfEvolutionRun.newPluginCreateBtn')}
          </Button>
        </div>
      }
      className="new-plugin-modal"
      width={520}
      destroyOnClose
    >
      <div className="npm-body">
        {/* Mode selector — always on top */}
        <p className="npm-section-label">{t('selfEvolutionRun.newPluginSelectMode')}</p>
        <div className="npm-mode-cards">
          {MODE_CARDS.map((card) => (
            <button
              key={card.mode}
              type="button"
              className={`npm-mode-card${mode === card.mode ? ' npm-mode-card--active' : ''}`}
              onClick={() => handleModeChange(card.mode)}
            >
              {card.badge && <span className="npm-mode-badge">{card.badge}</span>}
              <span className="npm-mode-icon">{card.icon}</span>
              <span className="npm-mode-title">{card.title}</span>
              <span className="npm-mode-desc">{card.desc}</span>
            </button>
          ))}
        </div>

        {/* Skill selector — shown first for skill mode, before fields */}
        {mode === 'skill' && (
          <div className="npm-expand">
            <Select
              showSearch
              placeholder={t('selfEvolutionRun.newPluginSkillSearchPlaceholder')}
              value={skillId}
              onChange={handleSkillChange}
              onSearch={handleSkillSearch}
              loading={skillLoading}
              options={skillOptions}
              filterOption={false}
              style={{ width: '100%' }}
              onFocus={() => skillOptions.length === 0 && void handleSkillSearch('')}
            />
          </div>
        )}

        {/* AI description textarea */}
        {mode === 'ai' && (
          <div className="npm-expand">
            <Input.TextArea
              placeholder={t('selfEvolutionRun.newPluginAiPlaceholder')}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              autoSize={{ minRows: 5, maxRows: 10 }}
            />
          </div>
        )}

        {/* Plugin id + name — shown after mode selection (skill: only after skill chosen) */}
        {showFields && (
          <div className="npm-fields npm-expand">
            <div className="npm-field-row">
              <div className="npm-field-label">
                {t('selfEvolutionRun.newPluginFieldPluginId')} <span className="npm-required-mark">*</span>
                <Tooltip title={t('selfEvolutionRun.newPluginFieldPluginIdTooltip')}>
                  <QuestionCircleOutlined className="npm-tip-icon" />
                </Tooltip>
              </div>
              <div className="npm-field-input">
                <Input
                  autoFocus
                  value={pluginId}
                  onChange={(e) => {
                    setPluginId(e.target.value);
                    setIdError(
                      e.target.value.trim() && !PLUGIN_ID_REGEX.test(e.target.value.trim())
                        ? t('selfEvolutionRun.newPluginIdErrorInvalid')
                        : '',
                    );
                  }}
                  placeholder={t('selfEvolutionRun.newPluginFieldPluginIdPlaceholder')}
                  status={idError ? 'error' : undefined}
                  onPressEnter={() => void handleCreate()}
                />
                {idError && <span className="npm-field-error">{idError}</span>}
              </div>
            </div>
            <div className="npm-field-row">
              <div className="npm-field-label">
                {t('selfEvolutionRun.newPluginFieldDisplayName')}
                <Tooltip title={t('selfEvolutionRun.newPluginFieldDisplayNameTooltip')}>
                  <QuestionCircleOutlined className="npm-tip-icon" />
                </Tooltip>
              </div>
              <div className="npm-field-input">
                <Input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={pluginId.trim() ? t('selfEvolutionRun.newPluginFieldDisplayNamePlaceholderWithId', { id: pluginId.trim() }) : t('selfEvolutionRun.newPluginFieldDisplayNamePlaceholder')}
                  onPressEnter={() => void handleCreate()}
                />
              </div>
            </div>
          </div>
        )}
      </div>
    </Modal>
  );
}
