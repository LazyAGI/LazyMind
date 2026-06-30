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
  Typography,
  Upload,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { UploadFile } from 'antd/es/upload/interface';
import { PlusOutlined, SearchOutlined, UploadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import dayjs from 'dayjs';
import utc from 'dayjs/plugin/utc';
import timezone from 'dayjs/plugin/timezone';
dayjs.extend(utc);
dayjs.extend(timezone);
import { cancelSchedule, createSchedule, enableSchedule, listSchedules, listScheduleTasks, runScheduleNow, updateSchedule } from './api';
import type { Schedule, Task, TaskListResponse } from './api';
import { KnowledgeBaseServiceApi } from '@/modules/chat/utils/request';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';
import { axiosInstance, BASE_URL } from '@/components/request';
import { CHAT_RESUME_CONVERSATION_KEY } from '@/modules/chat/constants/chat';

/* ── KnowledgeSelect: reusable KB selector with embedding guard ────────── */
interface KnowledgeSelectProps {
  value?: string[];
  onChange?: (val: string[]) => void;
  options: { value: string; label: string }[];
  embeddingReady: boolean | null;
}

function KnowledgeSelect({ value, onChange, options, embeddingReady }: KnowledgeSelectProps) {
  if (embeddingReady === false) {
    return (
      <Typography.Text type='secondary' style={{ fontSize: 12 }}>
        知识库功能需要配置 Embedding 模型后方可使用
      </Typography.Text>
    );
  }

  if (options.length === 0 && embeddingReady !== null) {
    return (
      <Typography.Text type='secondary' style={{ fontSize: 12 }}>
        暂无可用知识库，
        <Typography.Link href='/lib/knowledge/list' target='_blank'>
          去创建
        </Typography.Link>
      </Typography.Text>
    );
  }

  return (
    <Select
      mode='multiple'
      allowClear
      placeholder={embeddingReady === null ? '加载中…' : '选择知识库'}
      options={options}
      value={value}
      onChange={onChange}
      optionFilterProp='label'
      showSearch
      maxTagCount='responsive'
      disabled={embeddingReady === null}
    />
  );
}

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
  const parsed = value
    ? parseCronExpr(value)
    : { weekdays: [1, 2, 3, 4, 5], time: dayjs().hour(9).minute(0).second(0) };

  // Fully controlled: derive display state from `value` prop directly.
  // Internal state is only used as a fallback when value is absent.
  const [localWeekdays, setLocalWeekdays] = useState<number[]>(
    parsed.weekdays.length === 0 ? WEEKDAY_VALUES : parsed.weekdays,
  );
  const [localTime, setLocalTime] = useState<dayjs.Dayjs>(parsed.time);

  // Sync whenever the controlled value changes (e.g. form.setFieldsValue in edit mode).
  const prevValue = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (value !== undefined && value !== prevValue.current) {
      prevValue.current = value;
      const p = parseCronExpr(value);
      // Normalise: empty means every day, store as all 7 so localWeekdays stays consistent.
      setLocalWeekdays(p.weekdays.length === 0 ? WEEKDAY_VALUES : p.weekdays);
      setLocalTime(p.time);
    }
  }, [value]);

  const rawWeekdays = value ? parseCronExpr(value).weekdays : localWeekdays;
  // Empty array means "every day" (dow=*). Treat it as all 7 days selected so
  // the buttons light up correctly; buildCronExpr still emits '*' for all-7.
  const weekdays = rawWeekdays.length === 0 ? WEEKDAY_VALUES : rawWeekdays;
  const time = value ? parseCronExpr(value).time : localTime;

  const emit = (wd: number[], t: dayjs.Dayjs) => {
    onChange?.(buildCronExpr(wd, t));
  };

  const toggleDay = (day: number) => {
    const next = weekdays.includes(day)
      ? weekdays.filter((d) => d !== day)
      : [...weekdays, day].sort((a, b) => a - b);
    setLocalWeekdays(next);
    emit(next, time);
  };

  const handleTimeChange = (val: dayjs.Dayjs | null) => {
    if (!val) return;
    setLocalTime(val);
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
  const navigate = useNavigate();
  const [data, setData] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [statusFilter, setStatusFilterLocal] = useState<string[]>([]);

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

  const handleOpenConversation = (conversationId: string) => {
    sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, conversationId);
    navigate('/agent/chat/home');
  };

  const statusOptions = [
    { text: t('taskCenter.statusRunning'), value: 'running' },
    { text: t('taskCenter.statusCompleted'), value: 'completed' },
    { text: t('taskCenter.statusFailed'), value: 'failed' },
    { text: t('taskCenter.statusInterrupted'), value: 'interrupted' },
    { text: t('taskCenter.statusCanceled'), value: 'canceled' },
  ];

  const columns: ColumnsType<Task> = [
    {
      title: t('taskCenter.scheduleDescription'),
      dataIndex: 'conversation_title',
      render: (v: string, r: Task) => {
        const label = v || r.title || r.conversation_id;
        return (
          <Button
            type='link'
            style={{ padding: 0, textAlign: 'left', height: 'auto', whiteSpace: 'normal' }}
            onClick={() => handleOpenConversation(r.conversation_id)}
          >
            {label}
          </Button>
        );
      },
    },
    {
      title: t('taskCenter.statusCol'),
      dataIndex: 'status',
      width: 90,
      filters: statusOptions,
      filteredValue: statusFilter,
      onFilter: (value, record) => record.status === value,
      render: (v: string) => (
        <Tag color={v === 'completed' ? 'green' : v === 'failed' ? 'red' : 'blue'}>
          {t(`taskCenter.status${capitalize(v)}`) || v}
        </Tag>
      ),
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
    {
      title: t('taskCenter.finishedAt'),
      dataIndex: 'finished_at',
      width: 160,
      render: (v: string) => (v ? new Date(v).toLocaleString() : '—'),
    },
  ];

  return (
    <Table<Task>
      rowKey='id'
      size='small'
      loading={loading}
      dataSource={data}
      columns={columns}
      onChange={(_pagination, filters) => {
        setStatusFilterLocal((filters.status as string[]) ?? []);
      }}
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
  const [embeddingReady, setEmbeddingReady] = useState<boolean | null>(null);
  const [expandedKeys, setExpandedKeys] = useState<string[]>([]);
  const [scheduleNameInput, setScheduleNameInput] = useState('');
  // Filter state
  const [statusFilter, setStatusFilter] = useState<'all' | 'enabled' | 'disabled'>('enabled');
  const [keyword, setKeyword] = useState('');
  // Edit modal state
  const [editTarget, setEditTarget] = useState<Schedule | null>(null);
  // Incremented each time the modal opens to give VisualScheduler a fresh key,
  // forcing it to re-initialise its internal useState from the new value prop.
  const [modalKey, setModalKey] = useState(0);

  const localTimezone = useRef(Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai');

  const fetchSchedules = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await listSchedules(statusFilter === 'all' || statusFilter === 'disabled');
      setSchedules(resp.items ?? []);
    } catch {
      message.error(t('taskCenter.loadError'));
    } finally {
      setLoading(false);
    }
  }, [t, statusFilter]);

  useEffect(() => { void fetchSchedules(); }, [fetchSchedules]);

  // Client-side filter: status tab + keyword search
  const displaySchedules = schedules.filter((s) => {
    if (statusFilter === 'enabled' && !s.enabled) return false;
    if (statusFilter === 'disabled' && s.enabled) return false;
    if (keyword) {
      const kw = keyword.toLowerCase();
      const name = (s.name || s.prompt_template || '').toLowerCase();
      const desc = (s.prompt_template || '').toLowerCase();
      if (!name.includes(kw) && !desc.includes(kw)) return false;
    }
    return true;
  });

  useEffect(() => {
    KnowledgeBaseServiceApi()
      .datasetServiceListDatasets({ pageSize: 100 })
      .then((res) => {
        const datasets = res?.data?.datasets ?? [];
        setKbOptions(datasets.map((d) => ({ value: d.dataset_id ?? '', label: d.display_name ?? d.dataset_id ?? '' })));
      })
      .catch(() => {});

    // Check if embedding model is configured.
    axiosInstance
      .get(`${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_main`)
      .then((res: any) => {
        const ready = res?.data?.data?.ready ?? res?.data?.ready ?? null;
        setEmbeddingReady(ready === true);
      })
      .catch(() => setEmbeddingReady(false));
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

  const handleEnable = async (id: string) => {
    try {
      await enableSchedule(id);
      message.success('已启用');
      void fetchSchedules();
    } catch {
      message.error('启用失败');
    }
  };

  const handleRunNow = async (id: string) => {
    try {
      await runScheduleNow(id);
      message.success('已触发立即执行，任务正在运行中');
      void fetchSchedules();
    } catch {
      message.error('立即执行失败');
    }
  };

  const handleOpenEdit = (record: Schedule) => {
    setEditTarget(record);
    form.setFieldsValue({
      prompt_template: record.prompt_template,
      remark: record.remark,
      cron_expr: record.cron_expr,
      kb_ids: record.kb_ids ?? [],
    });
    setScheduleNameInput(record.name || '');
    setFileList([]);
    setUploadedPaths(record.file_ids ?? []);
    setModalKey((k) => k + 1);
    setModalOpen(true);
  };

  const handleCreate = async () => {
    try {
      const values = await form.validateFields();
      setSubmitting(true);
      const payload = {
        name: scheduleNameInput.trim(),
        remark: values.remark ?? '',
        cron_expr: values.cron_expr || buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)),
        prompt_template: values.prompt_template,
        timezone: localTimezone.current,
        kb_ids: values.kb_ids ?? [],
        file_ids: uploadedPaths,
      };
      if (editTarget) {
        await updateSchedule(editTarget.id, payload);
        message.success('修改成功');
      } else {
        await createSchedule(payload);
        message.success(t('taskCenter.createSuccess'));
      }
      setModalOpen(false);
      setEditTarget(null);
      form.resetFields();
      setScheduleNameInput('');
      setFileList([]);
      setUploadedPaths([]);
      void fetchSchedules();
    } catch (err: unknown) {
      const isValidation = err != null && typeof err === 'object' && 'errorFields' in err;
      if (!isValidation) {
        message.error(editTarget ? '修改失败' : t('taskCenter.createError'));
      }
    } finally {
      setSubmitting(false);
    }
  };

  const handleOpenModal = () => {
    setEditTarget(null);
    form.resetFields();
    form.setFieldValue('cron_expr', buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)));
    setFileList([]);
    setUploadedPaths([]);
    setScheduleNameInput('');
    setModalKey((k) => k + 1);
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
      title: '已执行次数',
      dataIndex: 'run_count',
      width: 100,
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
      render: (v: string) => (v ? dayjs(v).format('YYYY/MM/DD HH:mm:ss') : '—'),
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
      width: 180,
      render: (_: unknown, record: Schedule) => (
        <Space size={4}>
          <Button size='small' onClick={() => handleOpenEdit(record)}>编辑</Button>
          <Button size='small' onClick={() => handleRunNow(record.id)}>立即执行</Button>
          {record.enabled
            ? <Button size='small' onClick={() => handleDisable(record.id)}>{t('taskCenter.cancelSchedule')}</Button>
            : <Button size='small' type='primary' onClick={() => handleEnable(record.id)}>启用</Button>
          }
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 12, flexWrap: 'wrap' }} size={[8, 8]}>
        <Button type='primary' icon={<PlusOutlined />} onClick={handleOpenModal}>
          {t('taskCenter.newSchedule')}
        </Button>
        <Input
          prefix={<SearchOutlined style={{ color: '#bbb' }} />}
          placeholder='搜索任务名称或描述'
          allowClear
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          style={{ width: 220 }}
        />
        <Space.Compact>
          {(['enabled', 'all', 'disabled'] as const).map((v) => (
            <Button
              key={v}
              size='middle'
              type={statusFilter === v ? 'primary' : 'default'}
              onClick={() => setStatusFilter(v)}
            >
              {v === 'enabled' ? '启用中' : v === 'disabled' ? '已停用' : '全部'}
            </Button>
          ))}
        </Space.Compact>
      </Space>
      <Table<Schedule>
        rowKey='id'
        loading={loading}
        dataSource={displaySchedules}
        columns={columns}
        pagination={false}
        expandable={{
          expandedRowKeys: expandedKeys,
          onExpandedRowsChange: (keys) => setExpandedKeys(keys as string[]),
          expandedRowRender: (record) => <ExpandedScheduleTasks scheduleId={record.id} />,
          rowExpandable: (record) => (record.run_count ?? 0) > 0,
          showExpandColumn: false,
        }}
      />
      <Modal
        title={
          <Input
            value={scheduleNameInput}
            onChange={(e) => setScheduleNameInput(e.target.value)}
            placeholder={editTarget ? '任务名称' : '新定时任务'}
            variant='borderless'
            style={{ fontWeight: 600, fontSize: 16, padding: 0, width: '100%' }}
            maxLength={100}
          />
        }
        open={modalOpen}
        onOk={handleCreate}
        onCancel={() => {
          setModalOpen(false);
          setEditTarget(null);
          form.resetFields();
          setScheduleNameInput('');
          setFileList([]);
          setUploadedPaths([]);
        }}
        okText={editTarget ? '保存' : '创建'}
        confirmLoading={submitting || uploading}
        width={600}
      >
        <Form key={modalKey} form={form} layout='vertical' size='small'>
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
          <Form.Item
            name='kb_ids'
            label='知识库（选填）'
            valuePropName='value'
          >
            <KnowledgeSelect
              options={kbOptions}
              embeddingReady={embeddingReady}
            />
          </Form.Item>
          <Form.Item name='cron_expr' label='执行时间' rules={[{ required: true }]}>
            <VisualScheduler />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
