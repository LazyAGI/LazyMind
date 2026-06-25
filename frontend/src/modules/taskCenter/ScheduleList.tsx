import { useCallback, useEffect, useState } from 'react';
import { Button, Form, Input, Modal, Select, Space, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { PlusOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { cancelSchedule, createSchedule, listSchedules } from './api';
import type { Schedule } from './api';

const TIMEZONE_OPTIONS = [
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Europe/London',
  'Europe/Berlin',
  'America/New_York',
  'America/Los_Angeles',
  'UTC',
].map((tz) => ({ value: tz, label: tz }));

export default function ScheduleList() {
  const { t } = useTranslation();
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm();

  const fetchSchedules = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await listSchedules();
      setSchedules(resp.items ?? []);
    } catch {
      message.error(t('taskCenter.loadError'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void fetchSchedules();
  }, [fetchSchedules]);

  const handleDisable = async (id: string) => {
    try {
      await cancelSchedule(id);
      message.success(t('taskCenter.cancelSuccess'));
      void fetchSchedules();
    } catch {
      message.error(t('taskCenter.cancelError'));
    }
  };

  const handleCreate = async () => {
    try {
      const values = await form.validateFields();
      setSubmitting(true);
      await createSchedule({
        cron_expr: values.cron_expr,
        prompt_template: values.prompt_template,
        timezone: values.timezone || 'Asia/Shanghai',
        conversation_id: values.conversation_id || undefined,
      });
      message.success(t('taskCenter.createSuccess'));
      setModalOpen(false);
      form.resetFields();
      void fetchSchedules();
    } catch (err: unknown) {
      const isValidation =
        err != null && typeof err === 'object' && 'errorFields' in err;
      if (!isValidation) {
        message.error(t('taskCenter.createError'));
      }
    } finally {
      setSubmitting(false);
    }
  };

  const columns: ColumnsType<Schedule> = [
    {
      title: t('taskCenter.cronExpr'),
      dataIndex: 'cron_expr',
      width: 180,
    },
    {
      title: t('taskCenter.promptTemplate'),
      dataIndex: 'prompt_template',
      ellipsis: true,
      render: (v: string) => (v?.length > 60 ? `${v.slice(0, 60)}…` : v),
    },
    {
      title: t('taskCenter.timezone'),
      dataIndex: 'timezone',
      width: 140,
    },
    {
      title: 'Next Run',
      dataIndex: 'next_run_at',
      width: 180,
      render: (v: string) => (v ? new Date(v).toLocaleString() : '—'),
    },
    {
      title: 'Enabled',
      dataIndex: 'enabled',
      width: 80,
      render: (v: boolean) =>
        v ? <Tag color='green'>On</Tag> : <Tag color='default'>Off</Tag>,
    },
    {
      title: '',
      key: 'actions',
      width: 80,
      render: (_: unknown, record: Schedule) =>
        record.enabled ? (
          <Button size='small' onClick={() => handleDisable(record.id)}>
            {t('taskCenter.cancelSchedule')}
          </Button>
        ) : null,
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Button
          type='primary'
          icon={<PlusOutlined />}
          onClick={() => setModalOpen(true)}
        >
          {t('taskCenter.newSchedule')}
        </Button>
      </Space>
      <Table<Schedule>
        rowKey='id'
        loading={loading}
        dataSource={schedules}
        columns={columns}
        pagination={false}
      />
      <Modal
        title={t('taskCenter.newSchedule')}
        open={modalOpen}
        onOk={handleCreate}
        onCancel={() => { setModalOpen(false); form.resetFields(); }}
        confirmLoading={submitting}
        destroyOnHidden
      >
        <Form form={form} layout='vertical'>
          <Form.Item
            name='cron_expr'
            label={t('taskCenter.cronExpr')}
            rules={[{ required: true }]}
            extra={t('taskCenter.cronExprHelp')}
          >
            <Input placeholder='0 9 * * 1' />
          </Form.Item>
          <Form.Item
            name='prompt_template'
            label={t('taskCenter.promptTemplate')}
            rules={[{ required: true }]}
          >
            <Input.TextArea rows={4} />
          </Form.Item>
          <Form.Item
            name='timezone'
            label={t('taskCenter.timezone')}
            initialValue='Asia/Shanghai'
          >
            <Select options={TIMEZONE_OPTIONS} />
          </Form.Item>
          <Form.Item name='conversation_id' label={t('taskCenter.conversationId')}>
            <Input placeholder='（可选）' />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
