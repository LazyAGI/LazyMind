import { useState, useEffect, useCallback } from 'react';
import {
  Button,
  Table,
  Tooltip,
  Popconfirm,
  Tag,
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
  deletePluginDraft,
  updatePluginDraftContent,
} from '../../pluginDraftApi';
import type { PluginDraftRecord } from '../../pluginDraftApi';
import { useNavigate } from 'react-router-dom';
import NewPluginModal from '../../components/NewPluginModal';
import PluginInfoModal from '../../components/StateGraphEditor/PluginInfoModal';
import { parsePluginYaml } from '../../components/StateGraphEditor/core/pluginParser';
import { serializePluginModel } from '../../components/StateGraphEditor/core/pluginSerializer';
import { createEmptyPluginModel } from '../../components/StateGraphEditor/core/pluginModel';
import { parseScenario, serializeScenario } from '../../components/StateGraphEditor/ScenarioEditor';
import { createEmptyModel } from '../../components/StateGraphEditor/core/model';
import type { PluginModel } from '../../components/StateGraphEditor/core/pluginModel';
import type { ScenarioData } from '../../components/StateGraphEditor/ScenarioEditor';
import './index.scss';

export default function PluginListPage() {
  const navigate = useNavigate();
  const [records, setRecords] = useState<PluginDraftRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const pageSize = 20;

  const [createOpen, setCreateOpen] = useState(false);
  const [infoModalRecord, setInfoModalRecord] = useState<PluginDraftRecord | null>(null);
  const [infoModalPluginModel, setInfoModalPluginModel] = useState<PluginModel>(createEmptyPluginModel());
  const [infoModalScenarioData, setInfoModalScenarioData] = useState<ScenarioData>({ overview: '', stepDescriptions: {}, notes: '' });

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

  const handleCreated = (draftId: string) => {
    setCreateOpen(false);
    navigate(`/memory-management/plugins/${draftId}`);
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

  const openInfoModal = (record: PluginDraftRecord) => {
    const pm = parsePluginYaml(record.plugin_yaml_content) ?? createEmptyPluginModel();
    // Fallback: use the draft's name field if plugin_yaml_content has no name set
    if (!pm.name && record.name) pm.name = record.name;
    const graphModel = createEmptyModel();
    const sd = parseScenario(record.scenario_content ?? '', graphModel.nodes);
    setInfoModalPluginModel(pm);
    setInfoModalScenarioData(sd);
    setInfoModalRecord(record);
  };

  const handleInfoSave = async (pm: PluginModel, sd: ScenarioData) => {
    if (!infoModalRecord) return;
    const pluginYaml = serializePluginModel(pm);
    const scenarioContent = serializeScenario([], sd);
    await updatePluginDraftContent(infoModalRecord.id, {
      plugin_yaml_content: pluginYaml,
      scenario_content: scenarioContent,
    });
    message.success('已保存');
    void loadList(page);
  };

  const getPluginId = (record: PluginDraftRecord): string => {
    if (!record.plugin_yaml_content) return '—';
    const pm = parsePluginYaml(record.plugin_yaml_content);
    return pm?.id || '—';
  };

  const columns: ColumnsType<PluginDraftRecord> = [
    {
      title: '插件标识',
      key: 'plugin_id',
      render: (_: unknown, record: PluginDraftRecord) => {
        const pluginId = getPluginId(record);
        return (
          <Button
            type="link"
            style={{ fontFamily: 'monospace', padding: 0 }}
            onClick={() => navigate(`/memory-management/plugins/${record.id}`)}
          >
            {pluginId}
          </Button>
        );
      },
    },
    {
      title: '显示名称',
      dataIndex: 'name',
      key: 'name',
      render: (name: string, record: PluginDraftRecord) => (
        <Button
          type="link"
          style={{ padding: 0 }}
          onClick={() => navigate(`/memory-management/plugins/${record.id}`)}
        >
          {name}
        </Button>
      ),
    },
    {
      title: '状态',
      dataIndex: 'generate_status',
      key: 'generate_status',
      width: 100,
      render: (status: string) => {
        if (status === 'generating') return <Tag color="processing">生成中</Tag>;
        if (status === 'failed') return <Tag color="error">生成失败</Tag>;
        return null;
      },
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
      width: 96,
      render: (_: unknown, record: PluginDraftRecord) => (
        <div className="plugin-list-actions">
          <Tooltip title="修改插件信息">
            <Button
              type="text"
              size="small"
              icon={<EditOutlined />}
              onClick={() => openInfoModal(record)}
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

      <NewPluginModal
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onCreated={handleCreated}
      />

      {infoModalRecord && (
        <PluginInfoModal
          open={!!infoModalRecord}
          onCancel={() => setInfoModalRecord(null)}
          pluginModel={infoModalPluginModel}
          scenarioData={infoModalScenarioData}
          onSave={handleInfoSave}
        />
      )}
    </div>
  );
}
