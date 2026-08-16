import { useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import {
  Button,
  Drawer,
  Dropdown,
  Empty,
  Form,
  Input,
  Modal,
  Segmented,
  Select,
  Space,
  Spin,
  Switch,
  Tabs,
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
import { AppstoreOutlined, CalendarOutlined, CheckCircleFilled, DeleteOutlined, EllipsisOutlined, FileTextOutlined, PlayCircleOutlined, PlusOutlined, SearchOutlined, UnorderedListOutlined, UploadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import dayjs from 'dayjs';
import utc from 'dayjs/plugin/utc';
import timezone from 'dayjs/plugin/timezone';
dayjs.extend(utc);
dayjs.extend(timezone);
import { batchCreateAutomationGroup, cancelSchedule, createSchedule, deleteAutomationGroup, deleteSchedule, enableSchedule, listAutomationGroups, listSchedules, listScheduleTasks, moveSchedule, runScheduleNow, updateSchedule } from './api';
import type { AutomationGroup, BatchScheduleDraft, Schedule, Task, TaskListResponse } from './api';
import { KnowledgeBaseServiceApi } from '@/modules/chat/utils/request';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';
import { axiosInstance, BASE_URL, localizeErrorCode } from '@/components/request';
import { CHAT_RESUME_CONVERSATION_KEY, selectChatConversationFilter } from '@/modules/chat/constants/chat';
import { taskStatusDescription } from './taskStatusDescription';

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

function CreateFieldRow({ label, required, children }: { label: string; required?: boolean; children: ReactNode }) {
  return <div className='create-field-row'><label className={required ? 'is-required' : ''}><span className='field-label-text'>{label}</span></label><div>{children}</div></div>;
}

function FieldLabel({ children }: { children: ReactNode }) {
  return <span className='field-label-text'>{children}</span>;
}

/* ────────────────────────────────────────────────
   Helper: build cron expression from picker state
──────────────────────────────────────────────── */
const WEEKDAY_VALUES = [0, 1, 2, 3, 4, 5, 6];

function parseCadence(value: string): { interval: number; unit?: 'week' | 'month'; cron: string } {
  const match = value.match(/^@every:(\d+):(week|month);(.+)$/);
  return match ? { interval: Math.max(1, Number(match[1])), unit: match[2] as 'week' | 'month', cron: match[3] } : { interval: 1, cron: value };
}

function withCadence(cron: string, interval: number, unit: 'week' | 'month'): string {
  return interval > 1 ? `@every:${interval}:${unit};${cron}` : cron;
}

function sortMonthDays(days: number[]): number[] {
  return [...days].sort((a, b) => {
    if (a > 0 && b < 0) return -1;
    if (a < 0 && b > 0) return 1;
    return a > 0 ? a - b : Math.abs(a) - Math.abs(b);
  });
}

function formatMonthDays(days: number[]): string {
  const sorted = sortMonthDays(days);
  const regular = sorted.filter((day) => day > 0);
  const fromEnd = sorted.filter((day) => day < 0).map((day) => Math.abs(day));
  return [
    regular.length ? `${regular.join('，')}日` : '',
    fromEnd.length ? `倒数第${fromEnd.join('，')}天` : '',
  ].filter(Boolean).join('，');
}

function parseMonthDayField(field: string): number[] {
  const days: number[] = [];
  field.split(',').forEach((rawToken) => {
    const token = rawToken.trim();
    const range = token.match(/^(-?\d+)-(-?\d+)$/);
    if (range) {
      const start = Number(range[1]);
      const end = Number(range[2]);
      const step = start <= end ? 1 : -1;
      for (let day = start; day !== end + step; day += step) days.push(day);
      return;
    }
    const day = Number(token);
    if (Number.isInteger(day)) days.push(day);
  });
  return sortMonthDays([...new Set(days.filter((day) => (day >= 1 && day <= 31) || (day >= -4 && day <= -1)))]);
}

function buildCronExpr(weekdays: number[], time: dayjs.Dayjs): string {
  const minute = time.minute();
  const hour = time.hour();
  const dowPart = weekdays.length === 0 || weekdays.length === 7
    ? '*'
    : weekdays.join(',');
  return `${minute} ${hour} * * ${dowPart}`;
}

function buildMonthlyCronExpr(days: number[], time: dayjs.Dayjs): string {
  return `${time.minute()} ${time.hour()} ${days.join(',') || '1'} * *`;
}

function parseCronExpr(cron: string): { weekdays: number[]; time: dayjs.Dayjs } {
  const parts = parseCadence(cron).cron.trim().split(/\s+/);
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

export function describeCron(cron: string, t: TFunc): string {
  const cadence = parseCadence(cron);
  const fields = cadence.cron.trim().split(/\s+/);
  if (fields.length === 5 && fields[2] !== '*') {
    const days = formatMonthDays(parseMonthDayField(fields[2]));
    return `每${cadence.interval > 1 ? cadence.interval : ''}月 · ${days} ${String(fields[1]).padStart(2, '0')}:${String(fields[0]).padStart(2, '0')}`;
  }
  const { weekdays, time } = parseCronExpr(cron);
  const timeStr = time.format('HH:mm');
  if (weekdays.length === 0) return t('taskCenter.cronDaily').replace('{{time}}', timeStr);
  const sep = t('taskCenter.weekdaySeparator');
  const labels = weekdays.map((d) => t(`taskCenter.weekdayFull${d}`)).join(sep);
  const weekly = t('taskCenter.cronWeekdays').replace('{{days}}', labels).replace('{{time}}', timeStr);
  return cadence.interval > 1 ? `每 ${cadence.interval} 周 · ${weekly}` : weekly;
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

  const rawWeekdays = value ? parseCronExpr(value).weekdays : localWeekdays;
  // Empty array means "every day" (dow=*). Treat it as all 7 days selected so
  // the buttons light up correctly; buildCronExpr still emits '*' for all-7.
  const weekdays = rawWeekdays.length === 0 ? WEEKDAY_VALUES : rawWeekdays;
  const time = value ? parseCronExpr(value).time : localTime;

  const emit = (wd: number[], nextTime: dayjs.Dayjs) => {
    onChange?.(withCadence(buildCronExpr(wd, nextTime), interval, 'week'));
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
    onChange?.(withCadence(mode === 'month' ? buildMonthlyCronExpr(monthDays, val) : buildCronExpr(weekdays, val), interval, mode));
  };

  const cadence = parseCadence(value || '');
  const fields = cadence.cron.trim().split(/\s+/);
  const initialMode = cadence.unit || (fields.length === 5 && fields[2] !== '*' ? 'month' : 'week');
  const [mode, setMode] = useState<'week' | 'month'>(initialMode);
  const [interval, setInterval] = useState(cadence.interval);
  const [monthDays, setMonthDays] = useState<number[]>(initialMode === 'month' ? parseMonthDayField(fields[2]) : [1]);
  const [monthPanelOpen, setMonthPanelOpen] = useState(false);

  // Sync every part of the visual picker when form.setFieldsValue loads another schedule.
  const prevValue = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (value !== undefined && value !== prevValue.current) {
      prevValue.current = value;
      const nextCadence = parseCadence(value);
      const nextFields = nextCadence.cron.trim().split(/\s+/);
      const nextMode = nextCadence.unit || (nextFields.length === 5 && nextFields[2] !== '*' ? 'month' : 'week');
      const nextParsed = parseCronExpr(value);
      setLocalWeekdays(nextParsed.weekdays.length === 0 ? WEEKDAY_VALUES : nextParsed.weekdays);
      setLocalTime(nextParsed.time);
      setMode(nextMode);
      setInterval(nextCadence.interval);
      setMonthDays(nextMode === 'month' ? parseMonthDayField(nextFields[2]) : [1]);
    }
  }, [value]);

  const emitMode = (nextMode: 'week' | 'month') => {
    setMode(nextMode);
    onChange?.(withCadence(nextMode === 'week' ? buildCronExpr(weekdays, time) : buildMonthlyCronExpr(monthDays, time), interval, nextMode));
  };
  const toggleMonthDay = (day: number) => {
    const next = monthDays.includes(day) ? monthDays.filter((item) => item !== day) : sortMonthDays([...monthDays, day]);
    if (!next.length) return;
    setMonthDays(next);
    onChange?.(withCadence(buildMonthlyCronExpr(next, time), interval, 'month'));
  };

  return (
    <div className='visual-scheduler'>
      <span>每</span><Input type='number' min={1} max={52} value={interval} onChange={(event) => { const next = Math.max(1, Number(event.target.value) || 1); setInterval(next); onChange?.(withCadence(mode === 'month' ? buildMonthlyCronExpr(monthDays, time) : buildCronExpr(weekdays, time), next, mode)); }} className='schedule-interval' />
      <Select value={mode} onChange={emitMode} options={[{ value: 'week', label: '周' }, { value: 'month', label: '月' }]} className='schedule-unit' />
      {mode === 'week' ? WEEKDAY_VALUES.map((d) => (
        <Button
          key={d}
          size='small'
          type={weekdays.includes(d) ? 'primary' : 'default'}
          onClick={() => toggleDay(d)}
          style={{ minWidth: 32, borderRadius: 6, padding: '0 6px' }}
        >
          {t(`taskCenter.weekdayShort${d}`)}
        </Button>
      )) : <div className='month-day-picker'>
        <Button onClick={() => setMonthPanelOpen((open) => !open)}>{formatMonthDays(monthDays)} <span>⌄</span></Button>
        {monthPanelOpen ? <div className='month-day-panel'>{[...Array.from({ length: 31 }, (_, index) => index + 1), -4, -3, -2, -1].map((day) => <Button key={day} size='small' type={monthDays.includes(day) ? 'primary' : 'text'} onClick={() => toggleMonthDay(day)}>{day}</Button>)}</div> : null}
      </div>}
      <TimePicker
        value={time}
        onChange={handleTimeChange}
        format='HH:mm'
        allowClear={false}
        style={{ width: 80 }}
      />
      <span style={{ fontSize: 12, color: '#888' }}>
        {`(${Intl.DateTimeFormat().resolvedOptions().timeZone})`}
      </span>
    </div>
  );
}

function scheduleFrequency(cron: string): number {
  const cadence = parseCadence(cron);
  const fields = cadence.cron.trim().split(/\s+/);
  if (fields[2] && fields[2] !== '*') return fields[2].split(',').length * 12 / cadence.interval;
  if (fields[4] && fields[4] !== '*') return fields[4].split(',').length * 52 / cadence.interval;
  return 365;
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
    selectChatConversationFilter('task');
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
      title: t('taskCenter.sequence'),
      key: 'sequence',
      width: 56,
      align: 'center',
      render: (_value, record, index) => {
        const sequence = (page - 1) * 10 + index + 1;
        return record.conversation_id ? (
          <Button
            type='link'
            style={{ padding: 0, height: 'auto' }}
            onClick={() => handleOpenConversation(record.conversation_id)}
          >
            {sequence}
          </Button>
        ) : sequence;
      },
    },
    {
      title: t('taskCenter.statusCol'),
      dataIndex: 'status',
      width: 150,
      filters: statusOptions,
      filteredValue: statusFilter,
      onFilter: (value, record) => record.status === value,
      render: (v: string, record: Task) => (
        <div className='schedule-history-status'><Tag color={v === 'succeeded' ? 'green' : v === 'failed' ? 'red' : 'blue'}>
          {v === 'waiting_inputs' ? t('taskCenter.statusWaitingInputs') : t(`taskCenter.status${capitalize(v)}`) || v}
        </Tag><small>{taskStatusDescription(record, t)}</small></div>
      ),
    },
    {
      title: t('taskCenter.createdAt'),
      dataIndex: 'created_at',
      width: 140,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    {
      title: t('taskCenter.finishedAt'),
      dataIndex: 'finished_at',
      width: 140,
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
  const [workspaceView, setWorkspaceView] = useState<'tasks' | 'groups'>('groups');
  const [groupFilter, setGroupFilter] = useState<string | undefined>();
  const [groups, setGroups] = useState<AutomationGroup[]>([]);
  const [creationType, setCreationType] = useState<'task' | 'group'>('task');
  const [activeBatchTask, setActiveBatchTask] = useState('');
  const [batchGroupName, setBatchGroupName] = useState('');
  const [batchTasks, setBatchTasks] = useState<BatchScheduleDraft[]>([]);
  const [selectedSchedule, setSelectedSchedule] = useState<Schedule | null>(null);
  // Filter state
  const [statusFilter, setStatusFilter] = useState<'all' | 'enabled' | 'disabled'>('enabled');
  const [keyword, setKeyword] = useState('');
  // Edit modal state
  const [editTarget, setEditTarget] = useState<Schedule | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Schedule | null>(null);
  const [deletingScheduleId, setDeletingScheduleId] = useState<string | null>(null);
  const [deleteGroupTarget, setDeleteGroupTarget] = useState<AutomationGroup | null>(null);
  const [deletingGroupId, setDeletingGroupId] = useState<string | null>(null);
  // Incremented each time the modal opens to give VisualScheduler a fresh key,
  // forcing it to re-initialise its internal useState from the new value prop.
  const [modalKey, setModalKey] = useState(0);

  const localTimezone = useRef(Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai');

  const fetchSchedules = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await listSchedules(statusFilter === 'all' || statusFilter === 'disabled');
      setSchedules(resp.items ?? []);
      const groupResp = await listAutomationGroups();
      setGroups(groupResp.items ?? []);
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
    if (groupFilter !== undefined && (s.group_id || '') !== groupFilter) return false;
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

  const handleDeleteSchedule = async () => {
    if (!deleteTarget || deletingScheduleId) return;
    const schedule = deleteTarget;
    setDeletingScheduleId(schedule.id);
    try {
      await deleteSchedule(schedule.id);
      setDeleteTarget(null);
      if (selectedSchedule?.id === schedule.id) setSelectedSchedule(null);
      message.success(t('taskCenter.scheduleDeleteSuccess'));
      await fetchSchedules();
    } catch {
      message.error(t('taskCenter.scheduleDeleteFailed'));
    } finally {
      setDeletingScheduleId(null);
    }
  };

  const handleDeleteGroup = async () => {
    if (!deleteGroupTarget || deletingGroupId) return;
    const group = deleteGroupTarget;
    setDeletingGroupId(group.id);
    try {
      await deleteAutomationGroup(group.id);
      setDeleteGroupTarget(null);
      if (groupFilter === group.id) setGroupFilter(undefined);
      message.success(t('taskCenter.groupDeleteSuccess'));
      await fetchSchedules();
    } catch {
      message.error(t('taskCenter.groupDeleteFailed'));
    } finally {
      setDeletingGroupId(null);
    }
  };

  const handleOpenEdit = (record: Schedule) => {
    setEditTarget(record);
    form.setFieldsValue({
      name: record.name || '',
      prompt_template: record.prompt_template,
      remark: record.remark,
      cron_expr: record.cron_expr,
      kb_ids: record.kb_ids ?? [],
      group_id: record.group_id,
      source_schedule_ids: record.dependencies?.map((dependency) => dependency.source_schedule_id) ?? [],
    });
    setFileList([]);
    setUploadedPaths(record.file_ids ?? []);
    setModalKey((k) => k + 1);
    setModalOpen(true);
  };

  const handleCreate = async () => {
    try {
      const values = await form.validateFields();
      setSubmitting(true);
      const mentionedSourceIDs = schedules
        .filter((schedule) => schedule.id !== editTarget?.id && schedule.name && values.prompt_template.includes(`@${schedule.name}`))
        .map((schedule) => schedule.id);
      const sourceScheduleIDs = Array.from(new Set<string>([...(values.source_schedule_ids ?? []), ...mentionedSourceIDs]));
      const payload = {
        name: values.name?.trim() || '',
        remark: values.remark ?? '',
        cron_expr: values.cron_expr || buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)),
        prompt_template: values.prompt_template,
        timezone: localTimezone.current,
        kb_ids: values.kb_ids ?? [],
        file_ids: uploadedPaths,
        group_id: values.group_id,
        dependencies: sourceScheduleIDs.map((sourceId: string) => ({
          source_schedule_id: sourceId,
          window_type: 'between_target_fires',
          content_types: ['final_answer', 'artifacts'],
          incomplete_policy: 'wait_then_run_with_warning',
          max_wait_seconds: 7200,
        })),
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
    setCreationType('task');
    setModalKey((k) => k + 1);
    setModalOpen(true);
  };

  const openBatchModal = () => {
    setBatchGroupName('');
    const first = { client_key: `task_${Date.now()}`, name: '', cron_expr: buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)), prompt_template: '', dependencies: [] };
    setBatchTasks([first]);
    setActiveBatchTask(first.client_key);
    setCreationType('group');
    setEditTarget(null);
    setModalOpen(true);
  };

  const handleBatchCreate = async () => {
    if (!batchGroupName.trim() || batchTasks.some((task) => !task.name.trim() || !task.prompt_template.trim())) {
      message.warning('请填写任务组名称和每个任务的名称、任务内容'); return;
    }
    setSubmitting(true);
    try {
      await batchCreateAutomationGroup({ group: { name: batchGroupName.trim(), timezone: localTimezone.current }, tasks: batchTasks });
      message.success('任务组和关联任务已创建'); setModalOpen(false); void fetchSchedules();
    } finally { setSubmitting(false); }
  };

  const renderScheduleCard = (schedule: Schedule) => (
    <article
      draggable={workspaceView === 'groups'}
      onDragStart={(event) => event.dataTransfer.setData('text/schedule-id', schedule.id)}
      className={`schedule-card ${schedule.enabled ? '' : 'is-disabled'}`}
      key={schedule.id}
      onClick={() => setSelectedSchedule(schedule)}
    >
      <div className='schedule-card-identity'>
        <span className='schedule-icon'><CalendarOutlined /></span>
        <div><h3>{schedule.name || schedule.prompt_template.slice(0, 24)}</h3><p>{schedule.prompt_template}</p>{schedule.dependencies?.length ? <small>收集：{schedule.dependencies.map((dependency) => schedules.find((source) => source.id === dependency.source_schedule_id)?.name?.trim() || dependency.source_name || '未命名任务').join('、')} · 上次执行至本次执行</small> : null}</div>
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
        <div>
          <Button className='schedule-run-button' icon={<PlayCircleOutlined />} onClick={() => void handleRunNow(schedule.id)}>{viewMode === 'large' ? t('taskCenter.scheduleRunNow') : null}</Button>
          <Dropdown
            trigger={['click']}
            menu={{ items: [
              { key: 'edit', label: t('taskCenter.scheduleEdit'), onClick: () => handleOpenEdit(schedule) },
              { type: 'divider' },
              { key: 'delete', label: t('taskCenter.scheduleDelete'), danger: true, onClick: () => setDeleteTarget(schedule) },
            ] }}
          >
            <Button icon={<EllipsisOutlined />} aria-label={t('taskCenter.scheduleActions')} />
          </Dropdown>
        </div>
      </div>
    </article>
  );

  const openGroupTasks = (groupID: string) => {
    setGroupFilter(groupID);
    setWorkspaceView('tasks');
    setViewMode('compact');
  };

  const renderGroupCard = (group: AutomationGroup) => {
    const items = schedules.filter((schedule) => (schedule.group_id || '') === group.id);
    const visibleItems = items.filter((schedule) => statusFilter === 'all' || (statusFilter === 'enabled' ? schedule.enabled : !schedule.enabled));
    if (keyword && !group.name.toLowerCase().includes(keyword.toLowerCase()) && !visibleItems.some((schedule) => `${schedule.name} ${schedule.prompt_template}`.toLowerCase().includes(keyword.toLowerCase()))) return null;
    if (statusFilter !== 'all' && visibleItems.length === 0) return null;
    const nextSchedule = items.filter((schedule) => schedule.enabled && schedule.next_run_at).sort((a, b) => dayjs(a.next_run_at).valueOf() - dayjs(b.next_run_at).valueOf())[0];
    const recentSchedule = items.filter((schedule) => schedule.last_run_at).sort((a, b) => dayjs(b.last_run_at).valueOf() - dayjs(a.last_run_at).valueOf())[0];
    const taskNames = items.map((schedule) => schedule.name || schedule.prompt_template);
    const taskNamesTooltip = (
      <div className='schedule-group-task-tooltip'>
        {taskNames.map((name, index) => <div key={items[index].id}>{name}</div>)}
      </div>
    );
    return <article className='schedule-group-card' key={group.id || 'ungrouped'} onDragOver={(event) => event.preventDefault()} onDrop={(event) => { event.preventDefault(); const scheduleID = event.dataTransfer.getData('text/schedule-id'); if (scheduleID) void moveSchedule(scheduleID, group.id || undefined, items.length).then(fetchSchedules); }}>
      <header><span className='schedule-group-icon'><AppstoreOutlined /></span><div><h3>{group.name}</h3><p>{group.remark || (group.id ? '集中管理组内的定时任务' : '尚未加入任务组的定时任务')}</p></div><span className={`schedule-status-chip ${items.some((schedule) => schedule.enabled) ? 'enabled' : 'disabled'}`}>{items.some((schedule) => schedule.enabled) ? '启用中' : '已停用'}</span></header>
      <div className='schedule-group-meta'><span>包含 {items.length} 个任务</span><span>最近执行：{recentSchedule?.last_run_at ? dayjs(recentSchedule.last_run_at).format('MM/DD HH:mm') : '—'}</span><span>下次执行：{nextSchedule?.next_run_at ? dayjs(nextSchedule.next_run_at).format('MM/DD HH:mm') : '—'}</span></div>
      <footer>
        <div className='schedule-group-task-tags'>
          {items.length > 0 ? <>
            <Tooltip title={taskNamesTooltip} placement='topLeft'>
              <Tag className='schedule-group-task-tag' tabIndex={0} aria-label={taskNames.join(', ')}>{taskNames[0]}</Tag>
            </Tooltip>
            {items.length > 1 ? (
              <Tooltip title={taskNamesTooltip} placement='topLeft'>
                <Tag className='schedule-group-task-count' tabIndex={0} aria-label={taskNames.join(', ')}>+{items.length - 1}</Tag>
              </Tooltip>
            ) : null}
          </> : <span>-</span>}
        </div>
        <Space size={8}>
          <Button className='schedule-group-tasks-button' onClick={() => openGroupTasks(group.id)}>查看组内任务</Button>
          <Button className='schedule-group-delete-button' danger type='default' icon={<DeleteOutlined />} aria-label={t('taskCenter.groupDelete')} onClick={() => setDeleteGroupTarget(group)}>
            {t('taskCenter.groupDelete')}
          </Button>
        </Space>
      </footer>
    </article>;
  };

  const ungroupedSchedules = displaySchedules
    .filter((schedule) => !schedule.group_id)
    .sort((a, b) => (a.group_position || 0) - (b.group_position || 0));

  const dependencyLabel = (schedule: Schedule) => `${schedule.name || schedule.prompt_template.slice(0, 24)} · ${describeCron(schedule.cron_expr, t)}`;
  const batchDependencyOptions = (task: BatchScheduleDraft, index: number) => [
    {
      label: '本任务组内',
      options: batchTasks.slice(0, index)
        .map((source, sourceIndex) => ({ value: `client:${source.client_key}`, label: `${source.name || `任务 ${sourceIndex + 1}`} · ${describeCron(source.cron_expr, t)}`, disabled: scheduleFrequency(source.cron_expr) < scheduleFrequency(task.cron_expr) })),
    },
    {
      label: '其他已有任务',
      options: schedules
        .map((source) => ({ value: `schedule:${source.id}`, label: dependencyLabel(source), disabled: scheduleFrequency(source.cron_expr) < scheduleFrequency(task.cron_expr) })),
    },
  ].filter((group) => group.options.length > 0);

  return (
    <div className='schedule-plans'>
      <div className='schedule-toolbar'>
        <Segmented className='schedule-workspace-toggle' value={workspaceView} onChange={(value) => { const next = value as 'tasks' | 'groups'; setWorkspaceView(next); setViewMode(next === 'groups' ? 'large' : 'compact'); if (next === 'groups') setGroupFilter(undefined); }} options={[{ value: 'groups', label: '分组展示' }, { value: 'tasks', label: '逐条展示' }]} />
        <Input
          prefix={<SearchOutlined style={{ color: '#bbb' }} />}
          placeholder={t('taskCenter.scheduleSearchPlaceholder')}
          allowClear
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
        />
        <Space.Compact>
          {(['all', 'enabled', 'disabled'] as const).map((v) => (
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
        <Segmented className='schedule-view-toggle' value={viewMode} onChange={(value) => setViewMode(value as 'large' | 'compact')} options={[
          { value: 'large', label: t('taskCenter.largeCards'), icon: <AppstoreOutlined /> },
          { value: 'compact', label: t('taskCenter.smallCards'), icon: <UnorderedListOutlined /> },
        ]} />
        <Button type='primary' icon={<PlusOutlined />} onClick={handleOpenModal}>新建定时任务</Button>
      </div>
      <Spin spinning={loading}>
        <section className='schedule-board'>
          {workspaceView === 'tasks' && displaySchedules.length ? (
            <div className='schedule-task-result'>
              {groupFilter !== undefined ? <div className='schedule-filter-note'><span>正在查看：{groups.find((group) => group.id === groupFilter)?.name || '其他任务'}</span><Button type='link' onClick={() => setGroupFilter(undefined)}>查看全部</Button></div> : null}
              <div className={`schedule-grid ${viewMode}`}>
              {displaySchedules.map(renderScheduleCard)}
              </div>
            </div>
          ) : workspaceView === 'groups' ? (
            <div className='schedule-group-view'>
              {groups.length ? <div className={`schedule-group-grid ${viewMode}`}>
                {groups.map((group) => renderGroupCard(group))}
              </div> : null}
              {ungroupedSchedules.length ? <section className='ungrouped-schedule-section' onDragOver={(event) => event.preventDefault()} onDrop={(event) => { event.preventDefault(); const scheduleID = event.dataTransfer.getData('text/schedule-id'); if (scheduleID) void moveSchedule(scheduleID, undefined, ungroupedSchedules.length).then(fetchSchedules); }}>
                <header><div><h3>其他任务</h3><p>尚未加入任务组的定时任务</p></div><span>{ungroupedSchedules.length} 个任务</span></header>
                <div className={`schedule-grid ${viewMode}`}>{ungroupedSchedules.map(renderScheduleCard)}</div>
              </section> : null}
              {!groups.length && !ungroupedSchedules.length ? <Empty className='schedule-empty' description={t('taskCenter.empty')} /> : null}
            </div>
          ) : <Empty className='schedule-empty' description={t('taskCenter.empty')} />}
        </section>
      </Spin>
      <Drawer className='schedule-detail-drawer' width={460} open={Boolean(selectedSchedule)} onClose={() => setSelectedSchedule(null)} title={selectedSchedule?.name || t('taskCenter.scheduleName')} footer={selectedSchedule ? <div className='schedule-detail-actions'><Button danger size='large' disabled={Boolean(deletingScheduleId)} onClick={() => setDeleteTarget(selectedSchedule)}>{t('taskCenter.scheduleDelete')}</Button><Button type='primary' size='large' onClick={() => handleOpenEdit(selectedSchedule)}>{t('taskCenter.scheduleEdit')}</Button></div> : null}>
        {selectedSchedule && <div className='schedule-detail-content'>
          <section><h3>{t('taskCenter.scheduleDescription')}</h3><p>{selectedSchedule.prompt_template}</p></section>
          <section><h3>{t('taskCenter.scheduleTriggerPeriod')}</h3><p>{describeCron(selectedSchedule.cron_expr, t)} · {selectedSchedule.timezone}</p></section>
          <section><h3>{t('taskCenter.nextRunAt')}</h3><p>{selectedSchedule.next_run_at ? dayjs(selectedSchedule.next_run_at).format('YYYY/MM/DD HH:mm:ss') : '—'}</p></section>
          <section><h3>{t('taskCenter.lastRun')}</h3><p>{selectedSchedule.last_run_at ? dayjs(selectedSchedule.last_run_at).format('YYYY/MM/DD HH:mm:ss') : '—'}</p></section>
          <section><h3>{t('taskCenter.scheduleTaskCount')}</h3><ExpandedScheduleTasks scheduleId={selectedSchedule.id} /></section>
        </div>}
      </Drawer>
      <Modal
        title={t('taskCenter.scheduleDeleteConfirmTitle')}
        open={Boolean(deleteTarget)}
        onCancel={() => { if (!deletingScheduleId) setDeleteTarget(null); }}
        onOk={() => void handleDeleteSchedule()}
        okText={t('taskCenter.scheduleDeleteOk')}
        cancelText={t('taskCenter.scheduleDeleteCancel')}
        okButtonProps={{ danger: true }}
        confirmLoading={Boolean(deletingScheduleId)}
      >
        <p>{t('taskCenter.scheduleDeleteConfirmContent')}</p>
      </Modal>
      <Modal
        title={t('taskCenter.groupDeleteConfirmTitle')}
        open={Boolean(deleteGroupTarget)}
        onCancel={() => { if (!deletingGroupId) setDeleteGroupTarget(null); }}
        onOk={() => void handleDeleteGroup()}
        okText={t('taskCenter.groupDeleteOk')}
        cancelText={t('taskCenter.groupDeleteCancel')}
        okButtonProps={{ danger: true }}
        confirmLoading={Boolean(deletingGroupId)}
      >
        <p>{t('taskCenter.groupDeleteConfirmContent')}</p>
      </Modal>
      <Modal
        title={editTarget?.name || t('taskCenter.scheduleNewTitle')}
        open={modalOpen}
        zIndex={1100}
        onOk={() => void (creationType === 'group' ? handleBatchCreate() : handleCreate())}
        onCancel={() => {
          setModalOpen(false);
          setEditTarget(null);
          form.resetFields();
          setFileList([]);
          setUploadedPaths([]);
        }}
        okText={editTarget ? t('taskCenter.scheduleSaveBtn') : t('taskCenter.scheduleCreateBtn')}
        confirmLoading={submitting || uploading}
        width={920}
        className='schedule-create-modal'
      >
        {!editTarget ? <div className='creation-type-picker'>
          <button type='button' className={creationType === 'task' ? 'is-selected' : ''} onClick={() => setCreationType('task')}><span><FileTextOutlined /></span><div><strong>任务</strong><small>创建一条任务，可加入任务组</small></div>{creationType === 'task' ? <CheckCircleFilled /> : null}</button>
          <button type='button' className={creationType === 'group' ? 'is-selected' : ''} onClick={openBatchModal}><span><AppstoreOutlined /></span><div><strong>任务组</strong><small>创建任务组，并可同时添加任务</small></div>{creationType === 'group' ? <CheckCircleFilled /> : null}</button>
        </div> : null}
        {creationType === 'task' || editTarget ? <>
        <Form key={modalKey} form={form} layout='horizontal' labelCol={{ flex: '145px' }} wrapperCol={{ flex: 1 }} labelAlign='left' colon={false} size='small'>
          <Form.Item name='name' label={<FieldLabel>{t('taskCenter.scheduleNameInputLabel')}</FieldLabel>}>
            <Input placeholder='请输入任务名称' maxLength={100} />
          </Form.Item>
          <Form.Item name='prompt_template' label={<FieldLabel>{t('taskCenter.scheduleDescription')}</FieldLabel>} rules={[{ required: true, message: t('taskCenter.scheduleDescriptionRequired') }]}>
            <Input.TextArea rows={3} maxLength={500} showCount placeholder={t('taskCenter.scheduleDescriptionPlaceholder')} />
          </Form.Item>
          <Form.Item name='remark' label={<FieldLabel>备注</FieldLabel>}>
            <Input placeholder={t('taskCenter.scheduleRemarkPlaceholder')} />
          </Form.Item>
          <Form.Item label={<FieldLabel>附件</FieldLabel>}>
            <Upload
              fileList={fileList}
              maxCount={3}
              accept='.png,.jpg,.jpeg,.pdf,.docx,.doc,.pptx'
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
              <Space><Button size='small' icon={<UploadOutlined />}>{t('taskCenter.scheduleUploadFileBtn')}</Button><span className='field-help-inline'>最多上传 3 个文件</span></Space>
            </Upload>
          </Form.Item>
          <Form.Item
            name='kb_ids'
            label={<FieldLabel>知识库</FieldLabel>}
            valuePropName='value'
          >
            <KnowledgeSelect
              options={kbOptions}
              embeddingReady={embeddingReady}
            />
          </Form.Item>
          <Form.Item name='cron_expr' label={<FieldLabel>{t('taskCenter.scheduleExecutionTime')}</FieldLabel>} rules={[{ required: true }]}>
            <VisualScheduler />
          </Form.Item>
          <Form.Item name='group_id' label={<FieldLabel>加入到</FieldLabel>}>
            <Select allowClear options={groups.map((group) => ({ value: group.id, label: group.name }))} placeholder='其他任务' />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(previous, current) => previous.cron_expr !== current.cron_expr}>{({ getFieldValue }) => <Form.Item name='source_schedule_ids' label={<FieldLabel>依赖任务</FieldLabel>} extra='可选择执行更频繁或与当前任务同频的任务。每次运行会汇总自上次运行后产生的结果；若同时运行，会等待依赖任务完成。'>
            <Select mode='multiple' allowClear optionFilterProp='label' options={schedules.filter((schedule) => schedule.id !== editTarget?.id).map((schedule) => ({ value: schedule.id, label: dependencyLabel(schedule), disabled: scheduleFrequency(schedule.cron_expr) < scheduleFrequency(getFieldValue('cron_expr') || '* * * * *') }))} placeholder='选择一个或多个依赖任务' />
          </Form.Item>}</Form.Item>
        </Form>
        </> : <div className='group-create-editor'>
          <CreateFieldRow label='任务组名称' required><Input value={batchGroupName} onChange={(event) => setBatchGroupName(event.target.value)} placeholder='请输入任务组名称' maxLength={128} /></CreateFieldRow>
          <div className={`group-task-heading ${batchTasks.length >= 2 ? 'has-tabs' : ''}`}>
            {batchTasks.length < 2 ? <p>组内任务</p> : null}
            <Space>
              {batchTasks.length === 1 ? <Button danger onClick={() => { setBatchTasks([]); setActiveBatchTask(''); }}>删除任务</Button> : null}
              <Button icon={<PlusOutlined />} onClick={() => { const next = { client_key: `task_${Date.now()}`, name: '', cron_expr: buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)), prompt_template: '', dependencies: [] }; setBatchTasks((items) => [...items, next]); setActiveBatchTask(next.client_key); }}>添加任务</Button>
            </Space>
          </div>
          <Tabs className={batchTasks.length === 1 ? 'single-task-tabs' : ''} type='editable-card' activeKey={activeBatchTask} onChange={setActiveBatchTask} tabBarExtraContent={batchTasks.length >= 2 ? <Button icon={<PlusOutlined />} onClick={() => { const next = { client_key: `task_${Date.now()}`, name: '', cron_expr: buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)), prompt_template: '', dependencies: [] }; setBatchTasks((items) => [...items, next]); setActiveBatchTask(next.client_key); }}>添加任务</Button> : null} onEdit={(target, action) => {
            if (action === 'add') { const next = { client_key: `task_${Date.now()}`, name: '', cron_expr: buildCronExpr([1, 2, 3, 4, 5], dayjs().hour(9).minute(0)), prompt_template: '', dependencies: [] }; setBatchTasks((items) => [...items, next]); setActiveBatchTask(next.client_key); }
            else { const remaining = batchTasks.filter((task) => task.client_key !== target); setBatchTasks(remaining); setActiveBatchTask(remaining[0]?.client_key || ''); }
          }} hideAdd items={batchTasks.map((task, index) => ({ key: task.client_key, label: task.name || `任务 ${index + 1}`, closable: batchTasks.length > 1, children: <section className='batch-task-card'>
            <CreateFieldRow label='任务名称' required><Input value={task.name} onChange={(event) => setBatchTasks((items) => items.map((item) => item.client_key === task.client_key ? { ...item, name: event.target.value } : item))} placeholder='请输入任务名称' /></CreateFieldRow>
            <CreateFieldRow label='任务描述' required><Input.TextArea value={task.prompt_template} onChange={(event) => setBatchTasks((items) => items.map((item) => item.client_key === task.client_key ? { ...item, prompt_template: event.target.value } : item))} placeholder='描述你希望系统定期执行的任务' rows={3} maxLength={500} showCount /></CreateFieldRow>
            <CreateFieldRow label='备注'><Input value={task.remark} onChange={(event) => setBatchTasks((items) => items.map((item) => item.client_key === task.client_key ? { ...item, remark: event.target.value } : item))} placeholder='请输入备注信息' /></CreateFieldRow>
            <CreateFieldRow label='附件'><Upload maxCount={3} accept='.png,.jpg,.jpeg,.pdf,.docx,.doc,.xlsx,.xls,.pptx,.ppt' customRequest={async ({ file, onSuccess, onError, onProgress }) => {
              setUploading(true);
              try {
                const path = await uploadFileInChunks(file as File, { onProgress: (progress) => onProgress?.({ percent: progress.percentage }) });
                setBatchTasks((items) => items.map((item) => item.client_key === task.client_key ? { ...item, file_ids: [...(item.file_ids || []), path] } : item));
                onSuccess?.(path);
              } catch (error) {
                if (!(error as { isAxiosError?: boolean })?.isAxiosError) message.error(localizeErrorCode('2000509'));
                onError?.(error as Error);
              } finally { setUploading(false); }
            }} onRemove={(file) => { const indexToRemove = file.response ? (task.file_ids || []).indexOf(String(file.response)) : -1; if (indexToRemove >= 0) setBatchTasks((items) => items.map((item) => item.client_key === task.client_key ? { ...item, file_ids: (item.file_ids || []).filter((_, fileIndex) => fileIndex !== indexToRemove) } : item)); }}>
              <Space><Button icon={<UploadOutlined />}>上传附件</Button><span className='field-help-inline'>最多上传 3 个文件</span></Space>
            </Upload></CreateFieldRow>
            <CreateFieldRow label='知识库'><KnowledgeSelect value={task.kb_ids} onChange={(kbIDs) => setBatchTasks((items) => items.map((item) => item.client_key === task.client_key ? { ...item, kb_ids: kbIDs } : item))} options={kbOptions} embeddingReady={embeddingReady} /></CreateFieldRow>
            <CreateFieldRow label='执行时间' required><VisualScheduler value={task.cron_expr} onChange={(cronExpr) => setBatchTasks((items) => items.map((item) => item.client_key === task.client_key ? { ...item, cron_expr: cronExpr, dependencies: (item.dependencies || []).filter((dependency) => { const internalSource = batchTasks.find((candidate) => candidate.client_key === dependency.source_client_key); const externalSource = schedules.find((candidate) => candidate.id === dependency.source_schedule_id); const source = internalSource || externalSource; return !source || scheduleFrequency(source.cron_expr) >= scheduleFrequency(cronExpr); }) } : item))} /></CreateFieldRow>
            <CreateFieldRow label='依赖任务'><div><Select mode='multiple' allowClear optionFilterProp='label' placeholder='选择一个或多个依赖任务' value={(task.dependencies || []).map((dependency) => dependency.source_client_key ? `client:${dependency.source_client_key}` : `schedule:${dependency.source_schedule_id}`)} options={batchDependencyOptions(task, index)} onChange={(keys: string[]) => setBatchTasks((items) => items.map((item) => item.client_key === task.client_key ? { ...item, dependencies: keys.map((key) => ({ source_client_key: key.startsWith('client:') ? key.slice(7) : undefined, source_schedule_id: key.startsWith('schedule:') ? key.slice(9) : '', window_type: 'between_target_fires', content_types: ['final_answer', 'artifacts'], incomplete_policy: 'wait_then_run_with_warning', max_wait_seconds: 7200 })) } : item))} /><div className='field-help'>可选择执行更频繁或与当前任务同频的任务。每次运行会汇总自上次运行后产生的结果；若同时运行，会等待依赖任务完成。</div></div></CreateFieldRow>
          </section> }))} />
        </div>}
      </Modal>
    </div>
  );
}
