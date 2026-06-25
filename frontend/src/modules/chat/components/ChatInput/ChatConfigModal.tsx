import { useState } from 'react';
import { Modal, Form, Radio, Switch, message } from 'antd';
import { useTranslation } from 'react-i18next';
import {
  ConversationSettingsApi,
  type ConversationPluginSettings,
} from '../../utils/request';

interface ChatConfigModalProps {
  open: boolean;
  onClose: () => void;
  conversationId: string;
  initialSettings?: ConversationPluginSettings;
}

export default function ChatConfigModal({
  open,
  onClose,
  conversationId,
  initialSettings,
}: ChatConfigModalProps) {
  const { t } = useTranslation();
  const [form] = Form.useForm<ConversationPluginSettings>();
  const [saving, setSaving] = useState(false);

  async function handleOk() {
    const values = await form.validateFields();
    setSaving(true);
    try {
      await ConversationSettingsApi().patchPluginSettings(conversationId, values);
      message.success(t('chat.conversationConfigSaved'));
      onClose();
    } catch {
      message.error(t('chat.conversationConfigSaveFailed'));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      title={t('chat.conversationConfigTitle')}
      open={open}
      onOk={handleOk}
      onCancel={onClose}
      confirmLoading={saving}
      destroyOnClose
      width={420}
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          plugin_mode: initialSettings?.plugin_mode ?? 'dynamic',
          enable_subagent: initialSettings?.enable_subagent ?? true,
        }}
        preserve={false}
      >
        <Form.Item name="plugin_mode" label={t('chat.conversationConfigPluginMode')}>
          <Radio.Group>
            <Radio value="dynamic">{t('chat.conversationConfigPluginModeDynamic')}</Radio>
            <Radio value="auto">{t('chat.conversationConfigPluginModeAuto')}</Radio>
          </Radio.Group>
        </Form.Item>
        <Form.Item
          name="enable_subagent"
          label={t('chat.conversationConfigEnableSubagent')}
          extra={t('chat.conversationConfigEnableSubagentDesc')}
          valuePropName="checked"
        >
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  );
}
