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
  Upload,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { UploadFile } from 'antd/es/upload/interface';
import { PlusOutlined, UploadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import { cancelSchedule, createSchedule, listSchedules } from './api';
import type { Schedule } from './api';
import { KnowledgeBaseServiceApi } from '@/modules/chat/utils/request';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';

/* ────────────────────────────────────────────────
   Helper: build cron expression from picker state
──────────────────────────────────────────────── */
// weekdays: 0=Sun,1=Mon,...,6=Sat (standard cron weekday)
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

/* Parse cron back to { weekdays, time } for display in the picker */
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

/* ────────────────────────────────────────────────
   VisualScheduler sub-component
──────────────────────────────────────────────── */
interface VisualSchedulerProps {
  value?: string;
  onChange?: (cron: string) => void;
}

function VisualScheduler({ value, onChange }: VisualSchedulerProps) {
  const parsed = value ? parseCronExpr(value) : { weekdays: [1, 2, 3, 4, 5], time: dayjs().hour(9).minute(0).second(0) };
  const [weekdays, setWeekdays] = useState<number[]>(parsed.weekdays);
  const [time, setTime] = useState<dayjs.Dayjs>(parsed.time);

  // Sync outward when value changes externally (e.g. form reset).
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
    <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}>
      <span style={{ fontSize: 13, color: '#555' }}>系统将在</span>
      <span style={{ fontSize: 13, color: '#555' }}>每周</span>
      {WEEKDAY_VALUES.map((d) => (
        <Button
          key={d}
          size='small'
          type={weekdays.includes(d) ? 'primary' : 'default'}
          onClick={() => toggleDay(d)}
          style={{ minWidth: 36, borderRadius: 6 }}
        >
          {WEEKDAY_LABELS[d]}
        </Button>
      ))}
      <span style={{ fontSize: 13, color: '#555' }}>的</span>
      <TimePicker
        value={time}
        onChange={handleTimeChange}
        format='HH:mm'
        allowClear={false}
        size='small'
        style={{ width: 90 }}
      />
      <span style={{ fontSize: 13, color: '#555' }}>自动执行</span>
    </div>
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

  // Detect user's local timezone once.
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

  useEffect(() => {
    void fetchSchedules();
  }, [fetchSchedules]);

  // Load knowledge base list for the selector.
  useEffect(() => {
    KnowledgeBaseServiceApi()
      .datasetServiceListDatasets({ pageSize: 100 })
      .then((res) => {
        const datasets = res?.data?.datasets ?? [];
        setKbOptions(datasets.map((d) => ({ value: d.id ?? '', label: d.display_name ?? d.id ?? '' })));
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

  const handleFileUpload = async (file: File): Promise<string> => {
    return uploadFileInChunks(file);
  };

  const handleCreate = async () => {
    try {
      const values = await form.validateFields();
      setSubmitting(true);
      await createSchedule({
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
      const isValidation =
        err != null && typeof err === 'object' && 'errorFields' in err;
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
      title: t('taskCenter.promptTemplate'),
      dataIndex: 'prompt_template',
      ellipsis: true,
      render: (v: string) => (v?.length > 60 ? `${v.slice(0, 60)}…` : v),
    },
    {
      title: t('taskCenter.cronExpr'),
      dataIndex: 'cron_expr',
      width: 180,
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
          onClick={handleOpenModal}
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
        onCancel={() => { setModalOpen(false); form.resetFields(); setFileList([]); setUploadedPaths([]); }}
        confirmLoading={submitting || uploading}
        destroyOnHidden
        width={600}
      >
        <Form form={form} layout='vertical'>
          {/* Task description */}
          <Form.Item
            name='prompt_template'
            label='任务描述'
            rules={[{ required: true, message: '请输入任务描述' }]}
          >
            <Input.TextArea
              rows={4}
              placeholder='描述你希望系统定期执行的任务，例如：每周一早上9点帮我收集GitHub上最新的AI项目动态并总结'
            />
          </Form.Item>

          {/* Attachments (0–3) */}
          <Form.Item label={`附件（最多3张）`}>
            <Upload
              fileList={fileList}
              maxCount={3}
              accept='.png,.jpg,.jpeg,.pdf,.docx,.doc,.pptx'
              beforeUpload={(file) => {
                if (fileList.length >= 3) {
                  message.warning('最多上传3个附件');
                  return Upload.LIST_IGNORE;
                }
                return false; // prevent auto-upload; we handle manually
              }}
              onChange={({ fileList: newList }) => {
                setFileList(newList);
              }}
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
                setFileList((prev) => prev.filter((f) => f.uid !== file.uid));
                // Remove corresponding uploaded path by index (best effort).
                const idx = fileList.findIndex((f) => f.uid === file.uid);
                if (idx >= 0) {
                  setUploadedPaths((prev) => {
                    const next = [...prev];
                    next.splice(idx, 1);
                    return next;
                  });
                }
              }}
            >
              <Button icon={<UploadOutlined />}>上传文件</Button>
            </Upload>
          </Form.Item>

          {/* Knowledge base selector */}
          {kbOptions.length > 0 && (
            <Form.Item name='kb_ids' label='关联知识库（可选）'>
              <Select
                mode='multiple'
                allowClear
                placeholder='选择知识库'
                options={kbOptions}
              />
            </Form.Item>
          )}

          {/* Visual cron picker */}
          <Form.Item
            name='cron_expr'
            label='执行时间'
            rules={[{ required: true }]}
          >
            <VisualScheduler />
          </Form.Item>

          {/* Show detected timezone (read-only) */}
          <Form.Item label='时区'>
            <span style={{ color: '#888', fontSize: 13 }}>{localTimezone.current}（自动检测）</span>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
