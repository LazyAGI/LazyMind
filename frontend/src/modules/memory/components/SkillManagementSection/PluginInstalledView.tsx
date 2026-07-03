import { useState, useEffect, useCallback } from 'react';
import { Button, Empty, Input, Popconfirm, Table, Tooltip, message } from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { useNavigate } from 'react-router-dom';
import { getLocalizedTablePagination } from '@/components/ui/pagination';
import {
  listPluginDrafts,
  deletePluginDraft,
} from '@/modules/plugin/pluginDraftApi';
import type { PluginDraftRecord } from '@/modules/plugin/pluginDraftApi';

interface PluginInstalledViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  onNewPlugin: () => void;
}

const PAGE_SIZE = 20;

export default function PluginInstalledView({ t, onNewPlugin }: PluginInstalledViewProps) {
  const navigate = useNavigate();
  const [records, setRecords] = useState<PluginDraftRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');

  const loadList = useCallback(async (p: number, q: string) => {
    setLoading(true);
    try {
      const resp = await listPluginDrafts({ page: p, pageSize: PAGE_SIZE });
      const filtered = q.trim()
        ? (resp.records ?? []).filter((r) =>
            r.name.toLowerCase().includes(q.trim().toLowerCase()),
          )
        : (resp.records ?? []);
      setRecords(filtered);
      setTotal(q.trim() ? filtered.length : (resp.total ?? 0));
    } catch {
      message.error('加载插件列表失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadList(page, query);
  }, [page, query, loadList]);

  const handleDelete = async (id: string) => {
    try {
      await deletePluginDraft(id);
      message.success('已删除');
      void loadList(page, query);
    } catch {
      message.error('删除失败');
    }
  };

  const handleSearch = (value: string) => {
    setQuery(value);
    setPage(1);
  };

  const handleReset = () => {
    setSearchInput('');
    setQuery('');
    setPage(1);
  };

  const columns: ColumnsType<PluginDraftRecord> = [
    {
      title: '插件名称',
      dataIndex: 'name',
      key: 'name',
      render: (name: string, record) => (
        <Button
          type="link"
          style={{ padding: 0, height: 'auto', fontWeight: 500 }}
          onClick={() => navigate(`/memory-management/plugins/${record.id}`)}
        >
          {name}
        </Button>
      ),
    },
    {
      title: '最后更新',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 180,
      render: (val: string) => new Date(val).toLocaleString('zh-CN'),
    },
    {
      title: '操作',
      key: 'actions',
      width: 100,
      render: (_: unknown, record: PluginDraftRecord) => (
        <div className="plugin-list-actions">
          <Tooltip title="编辑">
            <Button
              type="text"
              size="small"
              icon={<EditOutlined />}
              onClick={() => navigate(`/memory-management/plugins/${record.id}`)}
            />
          </Tooltip>
          <Popconfirm
            title="确认删除此插件草稿？"
            okText="删除"
            cancelText="取消"
            okButtonProps={{ danger: true }}
            onConfirm={() => void handleDelete(record.id)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </div>
      ),
    },
  ];

  const pagination = getLocalizedTablePagination(
    {
      current: page,
      pageSize: PAGE_SIZE,
      total,
      showSizeChanger: false,
      showTotal: (itemTotal) => t('common.totalItems', { total: itemTotal }),
      onChange: (p) => setPage(p),
    },
    t,
  );

  return (
    <div className="memory-skill-installed">
      <div className="memory-skill-installed-filters">
        <Input.Search
          allowClear
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          onSearch={handleSearch}
          placeholder="搜索插件名称..."
          className="memory-skill-installed-search"
        />
        <Button onClick={handleReset}>{t('admin.memoryReset')}</Button>
      </div>

      <div className="memory-list-content">
        {records.length === 0 && !loading ? (
          <Empty
            description="还没有插件草稿，点击「新建插件」开始创建"
            style={{ marginTop: 60 }}
          >
            <Button type="primary" icon={<PlusOutlined />} onClick={onNewPlugin}>
              新建插件
            </Button>
          </Empty>
        ) : (
          <Table<PluginDraftRecord>
            className="admin-page-table memory-table memory-skill-installed-table"
            rowKey="id"
            loading={loading}
            dataSource={records}
            columns={columns}
            pagination={pagination}
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description="暂无插件"
                />
              ),
            }}
          />
        )}
      </div>
    </div>
  );
}
