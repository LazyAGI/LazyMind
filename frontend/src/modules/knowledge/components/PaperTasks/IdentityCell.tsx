import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Dropdown, Form, Input, InputNumber, Modal, Space, Tag } from 'antd';
import { updatePaperIdentity, type DocumentAcademic } from '../../api/paperWorkflows';
export default function IdentityCell({ id, name, value, writable, refresh }: {
    id: string;
    name: string;
    value?: DocumentAcademic;
    writable: boolean;
    refresh: () => void;
}) {
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);
    const [busy, setBusy] = useState(false);
    const [form] = Form.useForm();
    const update = async (input: Parameters<typeof updatePaperIdentity>[1]) => {
        setBusy(true);
        try {
            await updatePaperIdentity(id, input);
            refresh();
        }
        finally {
            setBusy(false);
        }
    };
    const edit = () => {
        form.setFieldsValue({ ...value?.paper, title: value?.paper?.title || name, authors: value?.paper?.authors?.join('; ') });
        setOpen(true);
    };
    const statusLabels: Record<string, string> = { pending: t('paperTasks.identifyPending'), processing: t('paperTasks.identifyProcessing'), succeeded: t('paperTasks.identifySucceeded'), failed: t('paperTasks.identifyFailed') };
    return <><Space direction="vertical" size={0}>
  <Tag>{t(value?.state.kind === 'paper' ? 'paperTasks.paper' : value?.state.kind === 'other' ? 'paperTasks.ordinary' : 'paperTasks.unconfirmed')}</Tag><span title={value?.state.error}>{value?.state.manual ? t('paperTasks.manual') : statusLabels[value?.state.status || ''] || t('paperTasks.unidentified')}</span>
  {writable && <Dropdown menu={{ items: [{ key: 'edit', label: t(value?.state.kind === 'paper' ? 'paperTasks.editPaper' : 'paperTasks.confirmPaper'), onClick: edit }, { key: 'other', label: t('paperTasks.markOther'), onClick: () => void update({ kind: 'other' }) }, { key: 'retry', label: t('paperTasks.recognize'), onClick: () => void update({ reidentify: true }) }] }}><Button type="link" size="small" loading={busy}>{t('paperTasks.paperInfo')} ▾</Button></Dropdown>}
 </Space><Modal open={open} title={t('paperTasks.editPaper')} confirmLoading={busy} onCancel={() => setOpen(false)} onOk={async () => {
        const data = await form.validateFields();
        await update({ kind: 'paper', paper: { ...data, authors: String(data.authors || '').split(/[;；]/).map((s: string) => s.trim()).filter(Boolean) } });
        setOpen(false);
    }}><Form form={form} layout="vertical">
  <Form.Item name="title" label={t('paperTasks.title')} rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="authors" label={t('paperTasks.authors')}><Input /></Form.Item><Form.Item name="year" label={t('paperTasks.year')}><InputNumber min={1000} max={9999}/></Form.Item><Form.Item name="venue" label={t('paperTasks.venue')}><Input /></Form.Item><Form.Item name="doi" label="DOI"><Input /></Form.Item><Form.Item name="arxiv_id" label="arXiv ID"><Input /></Form.Item><Form.Item name="abstract" label={t('paperTasks.abstract')}><Input.TextArea /></Form.Item>
 </Form></Modal></>;
}
