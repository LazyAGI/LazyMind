import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Button,
  Drawer,
  Empty,
  Form,
  Input,
  Modal,
  Segmented,
  Select,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  TimePicker,
  Typography,
  Upload,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { UploadFile } from 'antd/es/upload/interface';
import { AppstoreOutlined, CalendarOutlined, EllipsisOutlined, PlayCircleOutlined, PlusOutlined, SearchOutlined, UnorderedListOutlined, UploadOutlined } from '@ant-design/icons';
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
import { axiosInstance, BASE_URL, localizeErrorCode } from '@/components/request';
import { CHAT_RESUME_CONVERSATION_KEY } from '@/modules/chat/constants/chat';

/* ── KnowledgeSelect: reusable KB selector with embedding guard ────────── */
interface KnowledgeSelectProps {
  value?: string[];
  onChange?: (val: string[]) => void;
  options: { value: string; label: string }[];
  embeddingReady: boolean | null;
}

function KnowledgeSelect({ value, onChange, options, embeddingReady }: KnowledgeSelectProps) {
  const { t } = useTranslation();
  if (embeddingReady === false) {
    return (
      <Typography.Text type='secondary' style={{ fontSize: 12 }}>
        {t('taskCenter.kbEmbeddingNotReady')}
      </Typography.Text>
    );
  }

  if (options.length === 0 && embeddingReady !== null) {
    return (
      <Typography.Text type='secondary' style={{ fontSize: 12 }}>
        {t('taskCenter.kbNoAvailable')}
        <Typography.Link href='/lib/knowledge/list' target='_blank'>
          {t('taskCenter.kbCreateLink')}
        </Typography.Link>
      </Typography.Text>
    );
  }

  return (
    <Select
      mode='multiple'
      allowClear
      placeholder={embeddingReady === null ? t('taskCenter.kbLoading') : t('taskCenter.scheduleKbPlaceholder')}
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

type TFunc = (key: string) => string;

function describeCron(cron: string, t: TFunc): string {
  const { weekdays, time } = parseCronExpr(cron);
  const timeStr = time.format('HH:mm');
  if (weekdays.length === 0) return t('taskCenter.cronDaily').replace('{{time}}', timeStr);
  const sep = t('taskCenter.weekdaySeparator');
  const labels = weekdays.map((d) => t(`taskCenter.weekdayFull${d}`)).join(sep);
  return t('taskCenter.cronWeekdays').replace('{{days}}', labels).replace('{{time}}', timeStr);
}

/* ────────────────────────────────────────────────
   VisualScheduler sub-component (compact single-line)
──────────────────────────────────────────────── */
interface VisualSchedulerProps {
  value?: string;
  onChange?: (cron: string) => void;
}

function VisualScheduler({ value, onChange }: VisualSchedulerProps) {
  const { t } = useTranslation();
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

  const emit = (wd: number[], time: dayjs.Dayjs) => {
    onChange?.(buildCronExpr(wd, time));
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
      <span style={{ fontSize: 13, color: '#555' }}>{t('taskCenter.weekly')}</span>
      {WEEKDAY_VALUES.map((d) => (
        <Button
          key={d}
          size='small'
          type={weekdays.includes(d) ? 'primary' : 'default'}
          onClick={() => toggleDay(d)}
          style={{ minWidth: 32, borderRadius: 6, padding: '0 6px' }}
        >
          {t(`taskCenter.weekdayShort${d}`)}
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
    { text: t('taskCenter.statusCompleted'), value: 'succeeded' },
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
        <Tag color={v === 'succeeded' ? 'green' : v === 'failed' ? 'red' : 'blue'}>
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
        const done = steps.filter((s) => s.status === 'succeeded').length;
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
        showTotal: (n) => t('taskCenter.scheduleRunCountTotal', { total: n }),
      }}
      style={{ margin: '8px 0' }}
    />
  );
}

/* ────────────────────────────────────────────────
   Main ScheduleList component
──────────────────────────────────────────────── */
interface ScheduleListProps {
  active: boolean;
}

export default function ScheduleList({ active }: ScheduleListProps) {
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
  const [viewMode, setViewMode] = useState<'large' | 'compact'>('large');
  const [selectedSchedule, setSelectedSchedule] = useState<Schedule | null>(null);
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
      // API errors are reported by the shared request interceptor.
    } finally {
      setLoading(false);
    }
  }, [t, statusFilter]);

  useEffect(() => {
    if (active) void fetchSchedules();
  }, [active, fetchSchedules]);

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
    } catch {}
  };

  const handleEnable = async (id: string) => {
    try {
      await enableSchedule(id);
      message.success(t('taskCenter.scheduleEnableSuccess'));
      void fetchSchedules();
    } catch {}
  };

  const handleRunNow = async (id: string) => {
    try {
      await runScheduleNow(id);
      message.success(t('taskCenter.scheduleRunNowSuccess'));
      void fetchSchedules();
    } catch {}
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
        message.success(t('taskCenter.scheduleUpdateSuccess'));
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
    } catch {
      // Form validation stays local; API errors use the shared interceptor.
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

  return (
    <div className='schedule-plans'>
      <div className='schedule-toolbar'>
        <Input
          prefix={<SearchOutlined style={{ color: '#bbb' }} />}
          placeholder={t('taskCenter.scheduleSearchPlaceholder')}
          allowClear
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
        />
        <Space.Compact>
          {(['enabled', 'all', 'disabled'] as const).map((v) => (
            <Button
              key={v}
              size='middle'
              type={statusFilter === v ? 'primary' : 'default'}
              onClick={() => setStatusFilter(v)}
            >
              {v === 'enabled' ? t('taskCenter.scheduleStatusEnabled') : v === 'disabled' ? t('taskCenter.scheduleStatusDisabled') : t('taskCenter.scheduleStatusAll')}
            </Button>
          ))}
        </Space.Compact>
        <div className='schedule-toolbar-spacer' />
        <Segmented value={viewMode} onChange={(value) => setViewMode(value as 'large' | 'compact')} options={[
          { value: 'large', label: t('taskCenter.largeCards'), icon: <AppstoreOutlined /> },
          { value: 'compact', label: t('taskCenter.smallCards'), icon: <UnorderedListOutlined /> },
        ]} />
        <Button type='primary' icon={<PlusOutlined />} onClick={handleOpenModal}>{t('taskCenter.newSchedule')}</Button>
      </div>
      <Spin spinning={loading}>
        <section className='schedule-board'>
          <header className='schedule-board-header'>
            <div><h2>{t('taskCenter.scheduleBoardTitle')}</h2><p>{t('taskCenter.scheduleBoardDescription')}</p></div>
            <span>{t('taskCenter.scheduleBoardCount', { total: displaySchedules.length })}</span>
          </header>
          {displaySchedules.length ? (
            <div className={`schedule-grid ${viewMode}`}>
              {displaySchedules.map((schedule) => (
                <article className={`schedule-card ${schedule.enabled ? '' : 'is-disabled'}`} key={schedule.id} onClick={() => setSelectedSchedule(schedule)}>
                  <div className='schedule-card-identity'>
                    <span className='schedule-icon'><CalendarOutlined /></span>
                    <div><h3>{schedule.name || schedule.prompt_template.slice(0, 24)}</h3><p>{schedule.prompt_template}</p></div>
                    <span className={`schedule-status-chip ${schedule.enabled ? 'enabled' : 'disabled'}`}>{schedule.enabled ? t('taskCenter.scheduleStatusEnabled') : t('taskCenter.scheduleStatusDisabled')}</span>
                  </div>
                  <div className='schedule-card-timing'>
                    <strong><CalendarOutlined /> {describeCron(schedule.cron_expr, t)}</strong>
                    <span>{t('taskCenter.nextRunAt')}：{schedule.next_run_at ? dayjs(schedule.next_run_at).format('YYYY/MM/DD HH:mm') : '—'}</span>
                    {viewMode === 'large' && <span>{t('taskCenter.lastRun')}：{schedule.last_run_at ? dayjs(schedule.last_run_at).format('YYYY/MM/DD HH:mm') : '—'}</span>}
                  </div>
                  <div className='schedule-card-actions' onClick={(event) => event.stopPropagation()}>
                    <label><Switch size='small' checked={schedule.enabled} onChange={(checked) => void (checked ? handleEnable(schedule.id) : handleDisable(schedule.id))} /> {schedule.enabled ? t('taskCenter.scheduleStatusEnabled') : t('taskCenter.scheduleStatusDisabled')}</label>
                    <span>{t('taskCenter.scheduleRunTotal', { total: schedule.run_count ?? 0 })}</span>
                    <div><Button className='schedule-run-button' icon={<PlayCircleOutlined />} onClick={() => void handleRunNow(schedule.id)}>{viewMode === 'large' ? t('taskCenter.scheduleRunNow') : null}</Button><Button icon={<EllipsisOutlined />} aria-label={t('taskCenter.scheduleEdit')} onClick={() => handleOpenEdit(schedule)} /></div>
                  </div>
                </article>
              ))}
            </div>
          ) : <Empty className='schedule-empty' description={t('taskCenter.empty')} />}
        </section>
      </Spin>
      <Drawer className='schedule-detail-drawer' width={460} open={Boolean(selectedSchedule)} onClose={() => setSelectedSchedule(null)} title={selectedSchedule?.name || t('taskCenter.scheduleName')} footer={selectedSchedule ? <Button type='primary' block size='large' onClick={() => handleOpenEdit(selectedSchedule)}>{t('taskCenter.scheduleEdit')}</Button> : null}>
        {selectedSchedule && <div className='schedule-detail-content'>
          <section><h3>{t('taskCenter.scheduleDescription')}</h3><p>{selectedSchedule.prompt_template}</p></section>
          <section><h3>{t('taskCenter.scheduleTriggerPeriod')}</h3><p>{describeCron(selectedSchedule.cron_expr, t)} · {selectedSchedule.timezone}</p></section>
          <section><h3>{t('taskCenter.nextRunAt')}</h3><p>{selectedSchedule.next_run_at ? dayjs(selectedSchedule.next_run_at).format('YYYY/MM/DD HH:mm:ss') : '—'}</p></section>
          <section><h3>{t('taskCenter.lastRun')}</h3><p>{selectedSchedule.last_run_at ? dayjs(selectedSchedule.last_run_at).format('YYYY/MM/DD HH:mm:ss') : '—'}</p></section>
          <section><h3>{t('taskCenter.scheduleTaskCount')}</h3><ExpandedScheduleTasks scheduleId={selectedSchedule.id} /></section>
        </div>}
      </Drawer>
      <Modal
        title={
          <Input
            value={scheduleNameInput}
            onChange={(e) => setScheduleNameInput(e.target.value)}
            placeholder={editTarget ? t('taskCenter.scheduleNameInputLabel') : t('taskCenter.scheduleNewTitle')}
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
        okText={editTarget ? t('taskCenter.scheduleSaveBtn') : t('taskCenter.scheduleCreateBtn')}
        confirmLoading={submitting || uploading}
        width={600}
      >
        <Form key={modalKey} form={form} layout='vertical' size='small'>
          <Form.Item name='prompt_template' label={t('taskCenter.scheduleDescription')} rules={[{ required: true, message: t('taskCenter.scheduleDescriptionRequired') }]}>
            <Input.TextArea rows={3} placeholder={t('taskCenter.scheduleDescriptionPlaceholder')} />
          </Form.Item>
          <Form.Item name='remark' label={t('taskCenter.scheduleRemarkOptional')}>
            <Input placeholder={t('taskCenter.scheduleRemarkPlaceholder')} />
          </Form.Item>
          <Form.Item label={t('taskCenter.scheduleAttachmentsLabel')}>
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
                  if (!(err as { isAxiosError?: boolean })?.isAxiosError) {
                    message.error(localizeErrorCode('2000509'));
                  }
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
              <Button size='small' icon={<UploadOutlined />}>{t('taskCenter.scheduleUploadFileBtn')}</Button>
            </Upload>
          </Form.Item>
          <Form.Item
            name='kb_ids'
            label={t('taskCenter.scheduleKbOptional')}
            valuePropName='value'
          >
            <KnowledgeSelect
              options={kbOptions}
              embeddingReady={embeddingReady}
            />
          </Form.Item>
          <Form.Item name='cron_expr' label={t('taskCenter.scheduleExecutionTime')} rules={[{ required: true }]}>
            <VisualScheduler />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
