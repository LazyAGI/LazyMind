import { Form, Input } from 'antd';
import type { PluginModel } from '../core/pluginModel';
import './index.scss';

interface Props {
  model: PluginModel;
  onChange: (model: PluginModel) => void;
}

export default function PluginConfigEditor({ model, onChange }: Props) {
  const update = (patch: Partial<PluginModel>) => onChange({ ...model, ...patch });

  return (
    <div className="plugin-config-editor">
      <section className="pce-section">
        <p className="pce-section-title">基本信息</p>
        <Form layout="vertical" size="small">
          <Form.Item label="插件标识">
            <Input
              value={model.id}
              onChange={(e) => update({ id: e.target.value })}
              placeholder="my-plugin（英文字母开头）"
            />
          </Form.Item>
          <Form.Item label="显示名称">
            <Input
              value={model.name}
              onChange={(e) => update({ name: e.target.value })}
              placeholder="例如：图片处理插件"
            />
          </Form.Item>
          <Form.Item label="插件描述">
            <Input.TextArea
              value={model.description ?? ''}
              onChange={(e) => update({ description: e.target.value })}
              placeholder="简短描述插件的用途…"
              autoSize={{ minRows: 2, maxRows: 4 }}
            />
          </Form.Item>
          <Form.Item label="触发条件（请用英文描述）">
            <Input.TextArea
              value={model.when_to_use ?? ''}
              onChange={(e) => update({ when_to_use: e.target.value })}
              placeholder="Describe in English when this plugin should be triggered…"
              autoSize={{ minRows: 2, maxRows: 4 }}
            />
          </Form.Item>
        </Form>
      </section>
    </div>
  );
}
