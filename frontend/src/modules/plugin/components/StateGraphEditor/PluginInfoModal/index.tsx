import { useEffect, useState } from 'react';
import { Modal, Input, Button, Tooltip, message } from 'antd';
import { QuestionCircleOutlined } from '@ant-design/icons';
import type { PluginModel } from '../core/pluginModel';
import type { ScenarioData } from '../ScenarioEditor';
import './index.scss';

const PLUGIN_ID_REGEX = /^[a-zA-Z][a-zA-Z0-9-_]*$/;

export interface PluginInfoModalProps {
  open: boolean;
  onCancel: () => void;
  pluginModel: PluginModel;
  scenarioData: ScenarioData;
  onSave: (pm: PluginModel, sd: ScenarioData) => Promise<void>;
}

export default function PluginInfoModal({ open, onCancel, pluginModel, scenarioData, onSave }: PluginInfoModalProps) {
  const [saving, setSaving] = useState(false);
  const [pluginId, setPluginId] = useState('');
  const [pluginName, setPluginName] = useState('');
  const [description, setDescription] = useState('');
  const [whenToUse, setWhenToUse] = useState('');
  const [overview, setOverview] = useState('');
  const [notes, setNotes] = useState('');
  const [idError, setIdError] = useState('');

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

  return (
    <Modal
      title="插件信息"
      open={open}
      onCancel={onCancel}
      width={560}
      footer={
        <div className="pim-footer">
          <Button onClick={onCancel}>取消</Button>
          <Button type="primary" loading={saving} onClick={handleSave}>保存</Button>
        </div>
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
              onChange={(e) => {
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
              onChange={(e) => setPluginName(e.target.value)}
              placeholder="例如：图片处理插件"
            />
          </div>
        </div>

        {/* 插件描述 */}
        <div className="pim-block">
          <div className="pim-block-label">插件描述</div>
          <Input.TextArea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
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
          </div>
          <Input.TextArea
            value={whenToUse}
            onChange={(e) => setWhenToUse(e.target.value)}
            placeholder="Describe in English when this plugin should be triggered…"
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 场景描述 */}
        <div className="pim-block">
          <div className="pim-block-label">场景描述</div>
          <Input.TextArea
            value={overview}
            onChange={(e) => setOverview(e.target.value)}
            placeholder="描述该插件适用的业务场景…"
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </div>

        {/* 注意事项 */}
        <div className="pim-block">
          <div className="pim-block-label">注意事项</div>
          <Input.TextArea
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="补充使用时需要注意的事项…"
            autoSize={{ minRows: 2, maxRows: 4 }}
          />
        </div>
      </div>
    </Modal>
  );
}
