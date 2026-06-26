import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Button,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  TimePicker,
  Tooltip,
  Upload,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { UploadFile } from 'antd/es/upload/interface';
import { PlusOutlined, UploadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import { cancelSchedule, createSchedule, listSchedules, listScheduleTasks } from './api';
import type { Schedule, Task, TaskListResponse } from './api';
import { KnowledgeBaseServiceApi } from '@/modules/chat/utils/request';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';

/* ────────────────────────────────────────────────
   Helper: build cron expression from picker state
──────────────────────────────────────────────── */
const WEEKDAY_LABELS = ['日', '一', '二', '三', '四', '五', '六'];
const WEEKDAY_VALUES = [0, 1, 2, 3, 4, 5, 6];

function buildCronExpr(weekdays: number[], time: dayjs.Dayjs): string {
  const minute = time.minute();
  const hour = time.hour();
  const dowPart = weekdays.length === 0 || weekdays.length === 7
    ? '*'
    : weekdays.join(',');
  return `${minute} ${hour} * * ${dowPart}`;
}

function parseCronExpr(cron: string): { weekdays: number[]; time: dayjs.Dayjs } {
  const parts = cron.trim().split(/\s+/);
  const minute = parseInt(parts[0] ?? '0', 10) || 0;
  const hour = parseInt(parts[1] ?? '0', 10) || 0;
  const dowStr = parts[4] ?? '*';
  const weekdays =
    dowStr === '*'
      ? []
      : dowStr.split(',').map((v) => parseInt(v, 10)).filter((v) => !isNaN(v));
  return { weekdays, time: dayjs().hour(hour).minute(minute).second(0) };
}

function capitalize(s: string) {
  if (!s) return '';
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function describeCron(cron: string): string {
  const { weekdays, time } = parseCronExpr(cron);
  const timeStr = time.format('HH:mm');
  if (weekdays.length === 0) return `每天 ${timeStr}`;
  const labels = weekdays.map((d) => `周${WEEKDAY_LABELS[d]}`).join('、');
  return `${labels} ${timeStr}`;
}

/* ────────────────────────────────────────────────
   VisualScheduler sub-component (compact single-line)
──────────────────────────────────────────────── */
interface VisualSchedulerProps {
  value?: string;
  onChange?: (cron: string) => void;
}

function VisualScheduler({ value, onChange }: VisualSchedulerProps) {
  const parsed = value ? parseCronExpr(value) : { weekdays: [1, 2, 3, 4, 5], time: dayjs().hour(9).minute(0).second(0) };
  const [weekdays, setWeekdays] = useState<number[]>(parsed.weekdays);
  const [time, setTime] = useState<dayjs.Dayjs>(parsed.time);

  useEffect(() => {
    if (value) {
      const p = parseCronExpr(value);
      setWeekdays(p.weekdays);
      setTime(p.time);
    }
  }, [value]);

  const emit = (wd: number[], t: dayjs.Dayjs) => {
    onChange?.(buildCronExpr(wd, t));
  };

  const toggleDay = (day: number) => {
    const next = weekdays.includes(day)
      ? weekdays.filter((d) => d !== day)
      : [...weekdays, day].sort((a, b) => a - b);
    setWeekdays(next);
    emit(next, time);
  };

  const handleTimeChange = (val: dayjs.Dayjs | null) => {
    if (!val) return;
    setTime(val);
    emit(weekdays, val);
  };

  return (
    <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 6 }}>
      <span style={{ fontSize: 13, color: '#555' }}>每周</span>
      {WEEKDAY_VALUES.map((d) => (
        <Button
          key={d}
          size='small'
          type={weekdays.includes(d) ? 'primary' : 'default'}
          onClick={() => toggleDay(d)}
          style={{ minWidth: 32, borderRadius: 6, padding: '0 6px' }}
        >
          {WEEKDAY_LABELS[d]}
        </Button>
      ))}
      <TimePicker
        value={time}
        onChange={handleTimeChange}
        format='HH:mm'
        allowClear={false}
        size='small'
        style={{ width: 80 }}
      />
      <span style={{ fontSize: 12, color: '#888' }}>
        {`(${Intl.DateTimeFormat().resolvedOptions().timeZone})`}
      </span>
    </div>
  );
}

/* ────────────────────────────────────────────────
   ExpandedScheduleTasks: sub-table for a schedule
──────────────────────────────────────────────── */
function ExpandedScheduleTasks({ scheduleId }: { scheduleId: string }) {
  const { t } = useTranslation();
  const [data, setData] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);

  const fetch = useCallback(async (p: number) => {
    setLoading(true);
    try {
      const resp: TaskListResponse = await listScheduleTasks(scheduleId, p, 10);
      setData(resp.items ?? []);
      setTotal(resp.total ?? 0);
    } catch {
      /* ignore */
    } finally {
      setLoading(false);
    }
  }, [scheduleId]);

  useEffect(() => { void fetch(page); }, [fetch, page]);

  const columns: ColumnsType<Task> = [
    {
      title: t('taskCenter.tasks'),
      dataIndex: 'conversation_title',
      render: (v: string, r: Task) => v || r.title || r.conversation_id,
    },
    {
      title: t('taskCenter.statusCol'),
      dataIndex: 'status',
      width: 90,
      render: (v: string) => <Tag color={v === 'completed' ? 'green' : v === 'failed' ? 'red' : 'blue'}>{t(`taskCenter.status${capitalize(v)}`) || v}</Tag>,
    },
    {
      title: t('taskCenter.steps'),
      dataIndex: 'steps',
      width: 80,
      render: (steps: Task['steps']) => {
        if (!steps?.length) return '—';
        const done = steps.filter((s) => s.status === 'completed' || s.status === 'succeeded').length;
        return `${done}/${steps.length}`;
      },
    },
    {
      title: t('taskCenter.createdAt'),
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => new Date(v).toLocaleString(),
    },
  ];

  return (
    <Table<Task>
      rowKey='id'
      size='small'
      loading={loading}
      dataSource={data}
      columns={columns}
      pagination={{
        current: page,
        pageSize: 10,
        total,
        onChange: (p) => setPage(p),
        size: 'small',
        showTotal: (n) => `共 ${n} 次`,
      }}
      style={{ margin: '8px 0' }}
    />
  );
}

/* ────────────────────────────────────────────────
   Main ScheduleList component
──────────────────────────────────────────────── */
export default function ScheduleList() {
  const { t } = useTranslation();
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm();
  const [fileList, setFileList] = useState<UploadFile[]>([]);
  const [uploadedPaths, setUploadedPaths] = useState<string[]>([]);
  const [uploading, setUploading] = useState(false);
  const [kbOptions, setKbOptions] = useState<{ value: string; label: string }[]>([]);
  const [expandedKeys, setExpandedKeys] = useState<string[]>([]);

  const localTimezone = useRef(Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai');

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

  useEffect(() => { void fetchSchedules(); }, [fetchSchedules]);

  useEffect(() => {
    KnowledgeBaseServiceApi()
      .datasetServiceListDatasets({ pageSize: 100 })
      .then((res) => {
        const datasets = res?.data?.datasets ?? [];
        setKbOptions(datasets.map((d) => ({ value: d.dataset_id ?? '', label: d.display_name ?? d.dataset_id ?? '' })));
      })
      .catch(() => {});
  }, []);

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
        name: values.name ?? '',
        remark: values.remark ?? '',
        cron_expr: values.cron_expr || buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)),
        prompt_template: values.prompt_template,
        timezone: localTimezone.current,
        kb_ids: values.kb_ids ?? [],
        file_ids: uploadedPaths,
      });
      message.success(t('taskCenter.createSuccess'));
      setModalOpen(false);
      form.resetFields();
      setFileList([]);
      setUploadedPaths([]);
      void fetchSchedules();
    } catch (err: unknown) {
      const isValidation = err != null && typeof err === 'object' && 'errorFields' in err;
      if (!isValidation) {
        message.error(t('taskCenter.createError'));
      }
    } finally {
      setSubmitting(false);
    }
  };

  const handleOpenModal = () => {
    form.resetFields();
    form.setFieldValue('cron_expr', buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)));
    setFileList([]);
    setUploadedPaths([]);
    setModalOpen(true);
  };

  const columns: ColumnsType<Schedule> = [
    {
      title: t('taskCenter.scheduleName'),
      dataIndex: 'name',
      render: (v: string, record: Schedule) => {
        const display = v || record.prompt_template?.slice(0, 20) + (record.prompt_template?.length > 20 ? '…' : '');
        return record.remark ? (
          <Tooltip title={record.remark}>
            <span style={{ borderBottom: '1px dashed #aaa', cursor: 'help' }}>{display}</span>
          </Tooltip>
        ) : display;
      },
    },
    {
      title: t('taskCenter.scheduleDescription'),
      dataIndex: 'prompt_template',
      ellipsis: true,
      render: (v: string) => (
        <Tooltip title={v}>
          <span>{v?.length > 30 ? `${v.slice(0, 30)}…` : v}</span>
        </Tooltip>
      ),
    },
    {
      title: t('taskCenter.scheduleAttachments'),
      dataIndex: 'file_ids',
      width: 60,
      render: (v: string[]) => (v?.length ? `${v.length}` : '—'),
    },
    {
      title: t('taskCenter.scheduleTriggerPeriod'),
      dataIndex: 'cron_expr',
      width: 180,
      render: (v: string) => describeCron(v),
    },
    {
      title: t('taskCenter.scheduleTaskCount'),
      dataIndex: 'run_count',
      width: 90,
      render: (v: number, record: Schedule) => (
        <Button
          type='link'
          size='small'
          style={{ padding: 0 }}
          onClick={() => {
            setExpandedKeys((prev) =>
              prev.includes(record.id) ? prev.filter((k) => k !== record.id) : [...prev, record.id],
            );
          }}
        >
          {v ?? 0}
        </Button>
      ),
    },
    {
      title: t('taskCenter.nextRunAt'),
      dataIndex: 'next_run_at',
      width: 160,
      render: (v: string) => (v ? new Date(v).toLocaleString() : '—'),
    },
    {
      title: t('taskCenter.enabled'),
      dataIndex: 'enabled',
      width: 70,
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
        <Button type='primary' icon={<PlusOutlined />} onClick={handleOpenModal}>
          {t('taskCenter.newSchedule')}
        </Button>
      </Space>
      <Table<Schedule>
        rowKey='id'
        loading={loading}
        dataSource={schedules}
        columns={columns}
        pagination={false}
        expandable={{
          expandedRowKeys: expandedKeys,
          onExpandedRowsChange: (keys) => setExpandedKeys(keys as string[]),
          expandedRowRender: (record) => <ExpandedScheduleTasks scheduleId={record.id} />,
          rowExpandable: () => false,
          showExpandColumn: false,
        }}
      />
      <Modal
        title={t('taskCenter.newSchedule')}
        open={modalOpen}
        onOk={handleCreate}
        onCancel={() => {
          setModalOpen(false);
          form.resetFields();
          setFileList([]);
          setUploadedPaths([]);
        }}
        confirmLoading={submitting || uploading}
        destroyOnHidden
        width={600}
      >
        <Form form={form} layout='vertical' size='small'>
          <Form.Item name='name' label='任务名称（选填）'>
            <Input placeholder='用于列表展示和检索，留空则显示描述前20字' />
          </Form.Item>
          <Form.Item name='prompt_template' label='任务描述' rules={[{ required: true, message: '请输入任务描述' }]}>
            <Input.TextArea rows={3} placeholder='描述你希望系统定期执行的任务' />
          </Form.Item>
          <Form.Item name='remark' label='备注（选填）'>
            <Input placeholder='内部备注，不影响执行' />
          </Form.Item>
          <Form.Item label='附件（最多3个）'>
            <Upload
              fileList={fileList}
              maxCount={3}
              accept='.png,.jpg,.jpeg,.pdf,.docx,.doc,.pptx'
              beforeUpload={() => false}
              onChange={({ fileList: newList }) => setFileList(newList)}
              customRequest={async ({ file, onSuccess, onError, onProgress }) => {
                setUploading(true);
                try {
                  const path = await uploadFileInChunks(file as File, {
                    onProgress: (p) => onProgress?.({ percent: p.percentage }),
                  });
                  setUploadedPaths((prev) => [...prev, path]);
                  onSuccess?.(path);
                } catch (err) {
                  message.error('附件上传失败');
                  onError?.(err as Error);
                } finally {
                  setUploading(false);
                }
              }}
              onRemove={(file) => {
                const idx = fileList.findIndex((f) => f.uid === file.uid);
                setFileList((prev) => prev.filter((f) => f.uid !== file.uid));
                if (idx >= 0) {
                  setUploadedPaths((prev) => {
                    const next = [...prev];
                    next.splice(idx, 1);
                    return next;
                  });
                }
              }}
            >
              <Button size='small' icon={<UploadOutlined />}>上传文件</Button>
            </Upload>
          </Form.Item>
          {kbOptions.length > 0 && (
            <Form.Item name='kb_ids' label='关联知识库（选填）'>
              <Select mode='multiple' allowClear placeholder='选择知识库' options={kbOptions} />
            </Form.Item>
          )}
          <Form.Item name='cron_expr' label='执行时间' rules={[{ required: true }]}>
            <VisualScheduler />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
