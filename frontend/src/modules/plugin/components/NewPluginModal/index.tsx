import { useEffect, useState } from 'react';
import { Modal, Input, Button, Select, Tooltip, message } from 'antd';
import { FileTextOutlined, ThunderboltOutlined, BulbOutlined, QuestionCircleOutlined } from '@ant-design/icons';
import { createPluginDraft, aiGeneratePluginDraft, updatePluginDraftContent } from '../../pluginDraftApi';
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

const MODE_CARDS: { mode: CreateMode; icon: React.ReactNode; title: string; desc: string; badge?: string }[] = [
  {
    mode: 'ai',
    icon: <BulbOutlined />,
    title: '描述需求',
    desc: '用自然语言描述目标，AI 帮你生成',
  },
  {
    mode: 'skill',
    icon: <ThunderboltOutlined />,
    title: '从技能转化',
    desc: '选择已有技能，AI 将其转化为可执行插件',
  },
  {
    mode: 'blank',
    icon: <FileTextOutlined />,
    title: '空白创建',
    desc: '从空白开始，手动搭建工作流',
    badge: '不推荐',
  },
];

export default function NewPluginModal({ open, onCancel, onCreated }: NewPluginModalProps) {
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
      const result = await listSkillAssetsPage({ keyword, page: 1, pageSize: 20 });
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
      setIdError('请输入插件标识');
      return;
    }
    if (!PLUGIN_ID_REGEX.test(trimmedId)) {
      setIdError('必须以英文字母开头，只能包含英文字母、数字、连字符和下划线');
      return;
    }
    if (mode === 'ai' && !description.trim()) {
      message.warning('请填写需求描述');
      return;
    }
    if (mode === 'skill' && !skillId) {
      message.warning('请选择一个技能');
      return;
    }

    // Display name falls back to plugin id if empty
    const effectiveName = name.trim() || trimmedId;

    setCreating(true);
    try {
      const draft = await createPluginDraft({ name: effectiveName, source_type: mode });
      const pm = { ...createEmptyPluginModel(), id: trimmedId, name: effectiveName };
      await updatePluginDraftContent(draft.id, {
        plugin_yaml_content: serializePluginModel(pm),
      });
      if (mode === 'ai') {
        await aiGeneratePluginDraft(draft.id, { description: description.trim() });
      } else if (mode === 'skill' && skillId) {
        await aiGeneratePluginDraft(draft.id, { skill_id: skillId });
      }
      reset();
      onCreated(draft.id);
    } catch {
      message.error('创建失败，请重试');
    } finally {
      setCreating(false);
    }
  };

  const canCreate = showFields && pluginId.trim() !== '' && !idError;

  return (
    <Modal
      title="新插件"
      open={open}
      onCancel={handleCancel}
      footer={
        <div className="npm-footer">
          <Button onClick={handleCancel}>取消</Button>
          <Button type="primary" loading={creating} disabled={!canCreate} onClick={() => void handleCreate()}>
            创建
          </Button>
        </div>
      }
      className="new-plugin-modal"
      width={520}
      destroyOnClose
    >
      <div className="npm-body">
        {/* Mode selector — always on top */}
        <p className="npm-section-label">选择创建方式</p>
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
              placeholder="搜索并选择技能"
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
              placeholder={[
                '请描述你想创建的插件，越详细越好，例如：',
                '· 场景：用于什么业务场景，解决什么问题',
                '· 输入：用户需要提供什么内容（文件、文本、图片等）',
                '· 步骤：大致分几个处理步骤，每步做什么',
                '· 触发条件：什么情况下应该使用这个插件',
                '· 不适用场景：什么情况下不应启用',
              ].join('\n')}
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
                插件标识 <span className="npm-required-mark">*</span>
                <Tooltip title="用于系统识别，英文字母开头，只含英文/数字/连字符/下划线">
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
                        ? '必须以英文字母开头，只能包含英文字母、数字、连字符和下划线'
                        : '',
                    );
                  }}
                  placeholder="在此输入插件标识，需有场景语义，如插件的英文名称"
                  status={idError ? 'error' : undefined}
                  onPressEnter={() => void handleCreate()}
                />
                {idError && <span className="npm-field-error">{idError}</span>}
              </div>
            </div>
            <div className="npm-field-row">
              <div className="npm-field-label">
                显示名称
                <Tooltip title="选填，不填则使用插件标识作为名称">
                  <QuestionCircleOutlined className="npm-tip-icon" />
                </Tooltip>
              </div>
              <div className="npm-field-input">
                <Input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={pluginId.trim() ? `默认使用插件标识「${pluginId.trim()}」` : '插件名称（如：合同审阅助手）'}
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
