import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Badge, Button, Input, Select, Space, Table, Tag, Tooltip, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useTranslation } from 'react-i18next';
import { debounce } from 'lodash';
import { cancelTask, listTasks } from './api';
import type { StepInfo, Task } from './api';
import { CHAT_RESUME_CONVERSATION_KEY } from '@/modules/chat/constants/chat';

const PAGE_SIZE = 20;

const STATUS_BADGE: Record<string, 'processing' | 'success' | 'error' | 'default' | 'warning'> = {
  running: 'processing',
  completed: 'success',
  succeeded: 'success',
  failed: 'error',
  canceled: 'default',
  interrupted: 'warning',
};

const STEP_STATUS_COLOR: Record<string, string> = {
  completed: 'green',
  succeeded: 'green',
  running: 'blue',
  failed: 'red',
  canceled: 'default',
};

function StepsCell({ steps }: { steps: StepInfo[] }) {
  if (!steps || steps.length === 0) return <span>—</span>;
  const done = steps.filter((s) => s.status === 'completed' || s.status === 'succeeded').length;
  const tooltipContent = (
    <div style={{ maxWidth: 320 }}>
      {steps.map((s, i) => (
        <div key={i} style={{ marginBottom: 4 }}>
          <Tag color={STEP_STATUS_COLOR[s.status] ?? 'default'} style={{ marginRight: 4 }}>
            {s.status}
          </Tag>
          <span style={{ fontSize: 12 }}>{s.step_id}</span>
          {s.artifact && (
            <span style={{ fontSize: 11, color: '#888', marginLeft: 4 }}>
              [{s.artifact}]
            </span>
          )}
        </div>
      ))}
    </div>
  );
  return (
    <Tooltip title={tooltipContent}>
      <span style={{ cursor: 'default' }}>
        {done}/{steps.length}
      </span>
    </Tooltip>
  );
}

interface TaskListProps {
  /** When provided, filters tasks by schedule_id (used when opened from ScheduleList). */
  scheduleId?: string;
}

export default function TaskList({ scheduleId }: TaskListProps = {}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState<string>('');
  const [keyword, setKeyword] = useState('');
  const [inputKeyword, setInputKeyword] = useState('');
  const [loading, setLoading] = useState(false);

  const fetchTasks = useCallback(
    async (p: number, status: string, kw: string) => {
      setLoading(true);
      try {
        const resp = await listTasks({
          status: status || undefined,
          task_type: scheduleId ? 'scheduled' : undefined,
          keyword: kw || undefined,
          page: p,
          page_size: PAGE_SIZE,
        });
        setTasks(resp.items ?? []);
        setTotal(resp.total ?? 0);
      } catch {
        message.error(t('taskCenter.loadError'));
      } finally {
        setLoading(false);
      }
    },
    [t, scheduleId],
  );

  useEffect(() => {
    void fetchTasks(page, statusFilter, keyword);
  }, [fetchTasks, page, statusFilter, keyword]);

  const debouncedSetKeyword = useRef(
    debounce((v: string) => {
      setKeyword(v);
      setPage(1);
    }, 300),
  ).current;

  const handleInputChange = (v: string) => {
    setInputKeyword(v);
    debouncedSetKeyword(v);
  };

  const handleCancel = async (id: string) => {
    try {
      await cancelTask(id);
      message.success(t('taskCenter.cancelSuccess'));
      void fetchTasks(page, statusFilter, keyword);
    } catch {
      message.error(t('taskCenter.cancelError'));
    }
  };

  const handleOpenConversation = (conversationId: string) => {
    sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, conversationId);
    navigate('/agent/chat/home');
  };

  const columns: ColumnsType<Task> = useMemo(
    () => [
      {
        title: t('taskCenter.tasks'),
        dataIndex: 'conversation_title',
        render: (v: string, record: Task) => {
          const displayTitle = v || record.title || t('taskCenter.noTitle');
          return (
            <div>
              <Button
                type='link'
                style={{ padding: 0, textAlign: 'left', height: 'auto', whiteSpace: 'normal' }}
                onClick={() => handleOpenConversation(record.conversation_id)}
              >
                {displayTitle}
              </Button>
              {record.schedule_name && (
                <Tag color='blue' style={{ marginLeft: 6, fontSize: 11 }}>
                  {record.schedule_name}
                </Tag>
              )}
            </div>
          );
        },
      },
      {
        title: t('taskCenter.taskType'),
        dataIndex: 'task_type',
        width: 120,
        render: (v: string) => {
          const map: Record<string, string> = {
            plugin_run: t('taskCenter.typePluginRun'),
            background_chat: t('taskCenter.typeBackgroundChat'),
            scheduled: t('taskCenter.typeScheduled'),
          };
          return map[v] ?? v;
        },
      },
      {
        title: t('taskCenter.steps') || '步骤',
        dataIndex: 'steps',
        width: 80,
        render: (steps: StepInfo[]) => <StepsCell steps={steps} />,
      },
      {
        title: 'Status',
        dataIndex: 'status',
        width: 110,
        render: (v: string) => (
          <Badge status={STATUS_BADGE[v] ?? 'default'} text={t(`taskCenter.status${capitalize(v)}`)} />
        ),
      },
      {
        title: t('taskCenter.createdAt'),
        dataIndex: 'created_at',
        width: 180,
        render: (v: string) => new Date(v).toLocaleString(),
      },
      {
        title: t('taskCenter.finishedAt'),
        dataIndex: 'finished_at',
        width: 180,
        render: (v?: string) => (v ? new Date(v).toLocaleString() : '—'),
      },
      {
        title: '',
        key: 'actions',
        width: 90,
        render: (_: unknown, record: Task) =>
          record.status === 'running' ? (
            <Button size='small' danger onClick={() => handleCancel(record.id)}>
              {t('taskCenter.cancel')}
            </Button>
          ) : null,
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t, page, statusFilter, keyword],
  );

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Input.Search
          placeholder={t('taskCenter.searchPlaceholder') || '搜索任务名称'}
          value={inputKeyword}
          onChange={(e) => handleInputChange(e.target.value)}
          onSearch={(v) => { setKeyword(v); setPage(1); }}
          allowClear
          style={{ width: 220 }}
        />
        <Select
          value={statusFilter}
          style={{ width: 120 }}
          onChange={(v) => { setStatusFilter(v); setPage(1); }}
          options={[
            { value: '', label: t('taskCenter.statusAll') },
            { value: 'running', label: t('taskCenter.statusRunning') },
            { value: 'completed', label: t('taskCenter.statusCompleted') || '已完成' },
            { value: 'failed', label: t('taskCenter.statusFailed') },
            { value: 'canceled', label: t('taskCenter.statusCanceled') },
          ]}
        />
      </Space>
      <Table<Task>
        rowKey='id'
        loading={loading}
        dataSource={tasks}
        columns={columns}
        pagination={{
          current: page,
          pageSize: PAGE_SIZE,
          total,
          onChange: (p) => setPage(p),
          showTotal: (n) => `共 ${n} 条`,
        }}
      />
    </div>
  );
}

function capitalize(s: string) {
  if (!s) return '';
  return s.charAt(0).toUpperCase() + s.slice(1);
}
