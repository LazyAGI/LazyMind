import { Button, Form, Input, InputNumber } from 'antd';
import { useTranslation } from 'react-i18next';

export interface PaperMetadataValues {
  title: string;
  authors?: string;
  year?: number;
  venue?: string;
  doi?: string;
  arxiv_id?: string;
  abstract?: string;
  tags?: string;
}

export default function PaperMetadataForm({ value, disabled, includeTags, onSave }: {
  value: PaperMetadataValues;
  disabled?: boolean;
  includeTags?: boolean;
  onSave: (value: PaperMetadataValues) => void | Promise<void>;
}) {
  const { t } = useTranslation();
  return <Form layout="vertical" initialValues={{ ...value, year: value.year || undefined }} disabled={disabled} onFinish={onSave}>
    <Form.Item name="title" label="题名" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item>
    <Form.Item name="authors" label={t('paperTasks.authors')}><Input /></Form.Item>
    <Form.Item name="venue" label={t('paperTasks.venue')}><Input /></Form.Item>
    <Form.Item name="year" label={t('paperTasks.year')} rules={[{ type: 'number', min: 1000, max: 9999 }]}><InputNumber min={1000} max={9999} /></Form.Item>
    <Form.Item name="doi" label="DOI"><Input /></Form.Item>
    <Form.Item name="arxiv_id" label="arXiv"><Input /></Form.Item>
    <Form.Item name="abstract" label={t('paperTasks.abstract')}><Input.TextArea /></Form.Item>
    {includeTags && <Form.Item name="tags" label="标签"><Input placeholder="用分号分开" /></Form.Item>}
    <Button type="primary" htmlType="submit" loading={disabled}>保存题录</Button>
  </Form>;
}
