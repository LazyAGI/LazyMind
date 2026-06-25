import { useCallback, useEffect, useState } from 'react';
import { Badge, Button, Select, Space, Table, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useTranslation } from 'react-i18next';
import { cancelTask, listTasks } from './api';
import type { Task } from './api';

const PAGE_SIZE = 20;

const STATUS_BADGE: Record<string, 'processing' | 'success' | 'error' | 'default' | 'warning'> = {
  running: 'processing',
  succeeded: 'success',
  failed: 'error',
  canceled: 'default',
  interrupted: 'warning',
};

export default function TaskList() {
  const { t } = useTranslation();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState<string>('');
  const [loading, setLoading] = useState(false);

  const fetchTasks = useCallback(async (p: number, status: string) => {
    setLoading(true);
    try {
      const resp = await listTasks({
        status: status || undefined,
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
  }, [t]);

  useEffect(() => {
    void fetchTasks(page, statusFilter);
  }, [fetchTasks, page, statusFilter]);

  const handleCancel = async (id: string) => {
    try {
      await cancelTask(id);
      message.success(t('taskCenter.cancelSuccess'));
      void fetchTasks(page, statusFilter);
    } catch {
      message.error(t('taskCenter.cancelError'));
    }
  };

  const columns: ColumnsType<Task> = [
    {
      title: t('taskCenter.tasks'),
      dataIndex: 'title',
      render: (v: string) => v || t('taskCenter.noTitle'),
      ellipsis: true,
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
  ];

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Select
          value={statusFilter}
          style={{ width: 120 }}
          onChange={(v) => { setStatusFilter(v); setPage(1); }}
          options={[
            { value: '', label: t('taskCenter.statusAll') },
            { value: 'running', label: t('taskCenter.statusRunning') },
            { value: 'succeeded', label: t('taskCenter.statusSucceeded') },
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
