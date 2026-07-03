import { useState, useEffect, useCallback } from 'react';
import {
  Button,
  Modal,
  Input,
  Table,
  Tooltip,
  Popconfirm,
  message,
  Empty,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  PlusOutlined,
  EditOutlined,
  DeleteOutlined,
} from '@ant-design/icons';
import {
  listPluginDrafts,
  createPluginDraft,
  deletePluginDraft,
} from '../../pluginDraftApi';
import type { PluginDraftRecord } from '../../pluginDraftApi';
import { useNavigate } from 'react-router-dom';
import './index.scss';

export default function PluginListPage() {
  const navigate = useNavigate();
  const [records, setRecords] = useState<PluginDraftRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const pageSize = 20;

  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState('');
  const [creating, setCreating] = useState(false);

  const loadList = useCallback(async (p = 1) => {
    setLoading(true);
    try {
      const resp = await listPluginDrafts({ page: p, pageSize });
      setRecords(resp.records ?? []);
      setTotal(resp.total ?? 0);
    } catch {
      message.error('加载插件列表失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadList(page);
  }, [page, loadList]);

  const handleCreate = async () => {
    if (!newName.trim()) {
      message.warning('请输入插件名称');
      return;
    }
    setCreating(true);
    try {
      const draft = await createPluginDraft({ name: newName.trim(), content: '' });
      message.success('插件草稿已创建');
      setCreateOpen(false);
      setNewName('');
      navigate(`/memory-management/plugins/${draft.id}`);
    } catch {
      message.error('创建失败，请重试');
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await deletePluginDraft(id);
      message.success('已删除');
      void loadList(page);
    } catch {
      message.error('删除失败');
    }
  };

  const columns: ColumnsType<PluginDraftRecord> = [
    {
      title: '插件名称',
      dataIndex: 'name',
      key: 'name',
      render: (name: string, record) => (
        <Button type="link" onClick={() => navigate(`/memory-management/plugins/${record.id}`)}>
          {name}
        </Button>
      ),
    },
    {
      title: '最后更新',
      dataIndex: 'updated_at',
      key: 'updated_at',
      render: (val: string) => new Date(val).toLocaleString('zh-CN'),
      width: 180,
    },
    {
      title: '操作',
      key: 'actions',
      width: 120,
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

  return (
    <div className="plugin-list-page">
      <div className="plugin-list-header">
        <div>
          <h3 className="plugin-list-title">我的插件</h3>
          <p className="plugin-list-subtitle">创建和管理自定义插件草稿</p>
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => setCreateOpen(true)}
        >
          新建插件
        </Button>
      </div>

      {records.length === 0 && !loading ? (
        <Empty
          description="还没有插件草稿，点击「新建插件」开始创建"
          style={{ marginTop: 80 }}
        >
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
            新建插件
          </Button>
        </Empty>
      ) : (
        <Table
          loading={loading}
          dataSource={records}
          columns={columns}
          rowKey="id"
          pagination={{
            current: page,
            pageSize,
            total,
            onChange: (p) => setPage(p),
            showTotal: (t) => `共 ${t} 条`,
          }}
        />
      )}

      <Modal
        title="新建插件草稿"
        open={createOpen}
        onOk={() => void handleCreate()}
        onCancel={() => { setCreateOpen(false); setNewName(''); }}
        confirmLoading={creating}
        okText="创建"
        cancelText="取消"
      >
        <Input
          autoFocus
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          placeholder="插件名称（如：我的审阅工作流）"
          onPressEnter={() => void handleCreate()}
        />
      </Modal>
    </div>
  );
}
