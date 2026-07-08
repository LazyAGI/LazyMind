import { useState, useEffect, useCallback } from 'react';
import { Button, Empty, Input, Popconfirm, Radio, Table, Tag, Tooltip, message } from 'antd';
import { DeleteOutlined, EditOutlined, EyeOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { useNavigate } from 'react-router-dom';
import { getLocalizedTablePagination } from '@/components/ui/pagination';
import {
  listPluginDrafts,
  deletePluginDraft,
  updatePluginDraftContent,
  listBuiltinPlugins,
} from '@/modules/plugin/pluginDraftApi';
import type { PluginDraftRecord, BuiltinPlugin } from '@/modules/plugin/pluginDraftApi';
import PluginInfoModal from '@/modules/plugin/components/StateGraphEditor/PluginInfoModal';
import { parsePluginYaml } from '@/modules/plugin/components/StateGraphEditor/core/pluginParser';
import { serializePluginModel } from '@/modules/plugin/components/StateGraphEditor/core/pluginSerializer';
import { createEmptyPluginModel } from '@/modules/plugin/components/StateGraphEditor/core/pluginModel';
import { parseScenario, serializeScenario } from '@/modules/plugin/components/StateGraphEditor/ScenarioEditor';
import { createEmptyModel } from '@/modules/plugin/components/StateGraphEditor/core/model';
import type { PluginModel } from '@/modules/plugin/components/StateGraphEditor/core/pluginModel';
import type { ScenarioData } from '@/modules/plugin/components/StateGraphEditor/ScenarioEditor';

interface PluginInstalledViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  onNewPlugin: () => void;
}

// Unified row type for the combined table.
type PluginRow =
  | ({ _type: 'draft' } & PluginDraftRecord)
  | ({ _type: 'builtin' } & BuiltinPlugin & { updated_at?: never; generate_status?: never });

type TypeFilter = 'all' | 'builtin' | 'draft';

const PAGE_SIZE = 20;

export default function PluginInstalledView({ t, onNewPlugin }: PluginInstalledViewProps) {
  const navigate = useNavigate();
  const [draftRecords, setDraftRecords] = useState<PluginDraftRecord[]>([]);
  const [builtinPlugins, setBuiltinPlugins] = useState<BuiltinPlugin[]>([]);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all');
  const [infoModalRecord, setInfoModalRecord] = useState<PluginDraftRecord | null>(null);
  const [infoModalPluginModel, setInfoModalPluginModel] = useState<PluginModel>(createEmptyPluginModel());
  const [infoModalScenarioData, setInfoModalScenarioData] = useState<ScenarioData>({ overview: '', stepDescriptions: {}, notes: '' });

  const loadList = useCallback(async () => {
    setLoading(true);
    try {
      const [draftsResp, builtins] = await Promise.all([
        listPluginDrafts({ page: 1, pageSize: 200 }),
        listBuiltinPlugins(),
      ]);
      setDraftRecords(draftsResp.records ?? []);
      setBuiltinPlugins(builtins);
    } catch {
      message.error('加载插件列表失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadList();
  }, [loadList]);

  const handleDelete = async (id: string) => {
    try {
      await deletePluginDraft(id);
      message.success('已删除');
      void loadList();
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
    setTypeFilter('all');
    setPage(1);
  };

  const openInfoModal = (record: PluginDraftRecord) => {
    const pm = parsePluginYaml(record.plugin_yaml_content) ?? createEmptyPluginModel();
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
    void loadList();
  };

  const getDraftPluginId = (record: PluginDraftRecord): string => {
    if (!record.plugin_yaml_content) return '—';
    const pm = parsePluginYaml(record.plugin_yaml_content);
    return pm?.id || '—';
  };

  // Build combined rows and apply filters.
  const allRows: PluginRow[] = [
    ...builtinPlugins.map((b): PluginRow => ({ _type: 'builtin', ...b })),
    ...draftRecords.map((d): PluginRow => ({ _type: 'draft', ...d })),
  ];

  const q = query.trim().toLowerCase();
  const filteredRows = allRows.filter((row) => {
    if (typeFilter === 'builtin' && row._type !== 'builtin') return false;
    if (typeFilter === 'draft' && row._type !== 'draft') return false;
    if (q) {
      const name = row._type === 'builtin' ? row.name : row.name;
      const id = row._type === 'builtin' ? row.id : getDraftPluginId(row);
      if (!name.toLowerCase().includes(q) && !id.toLowerCase().includes(q)) return false;
    }
    return true;
  });

  // Client-side pagination.
  const pageStart = (page - 1) * PAGE_SIZE;
  const pageRows = filteredRows.slice(pageStart, pageStart + PAGE_SIZE);

  const columns: ColumnsType<PluginRow> = [
    {
      title: '插件标识',
      key: 'plugin_id',
      render: (_: unknown, row: PluginRow) => {
        const pluginId = row._type === 'builtin' ? row.id : getDraftPluginId(row);
        const href =
          row._type === 'builtin'
            ? `/memory-management/plugins/builtin/${row.id}`
            : `/memory-management/plugins/${row.id}`;
        return (
          <Button
            type="link"
            style={{ fontFamily: 'monospace', padding: 0 }}
            onClick={() => navigate(href)}
          >
            {pluginId}
          </Button>
        );
      },
    },
    {
      title: '显示名称',
      key: 'name',
      render: (_: unknown, row: PluginRow) => {
        const href =
          row._type === 'builtin'
            ? `/memory-management/plugins/builtin/${row.id}`
            : `/memory-management/plugins/${row.id}`;
        return (
          <Button type="link" style={{ padding: 0 }} onClick={() => navigate(href)}>
            {row.name}
          </Button>
        );
      },
    },
    {
      title: '类型',
      key: 'type',
      width: 90,
      render: (_: unknown, row: PluginRow) =>
        row._type === 'builtin' ? (
          <Tag color="blue">内置</Tag>
        ) : (
          <Tag>自定义</Tag>
        ),
    },
    {
      title: '状态',
      key: 'generate_status',
      width: 100,
      render: (_: unknown, row: PluginRow) => {
        if (row._type === 'builtin') return null;
        const status = row.generate_status;
        if (status === 'generating') return <Tag color="processing">生成中</Tag>;
        if (status === 'failed') return <Tag color="error">生成失败</Tag>;
        return null;
      },
    },
    {
      title: '最后更新',
      key: 'updated_at',
      width: 180,
      render: (_: unknown, row: PluginRow) => {
        if (row._type === 'builtin') return '—';
        return new Date(row.updated_at).toLocaleString('zh-CN');
      },
    },
    {
      title: '操作',
      key: 'actions',
      width: 96,
      render: (_: unknown, row: PluginRow) => {
        if (row._type === 'builtin') {
          return (
            <Tooltip title="查看插件">
              <Button
                type="text"
                size="small"
                icon={<EyeOutlined />}
                onClick={() => navigate(`/memory-management/plugins/builtin/${row.id}`)}
              />
            </Tooltip>
          );
        }
        return (
          <div className="plugin-list-actions">
            <Tooltip title="修改插件信息">
              <Button
                type="text"
                size="small"
                icon={<EditOutlined />}
                onClick={() => openInfoModal(row)}
              />
            </Tooltip>
            <Popconfirm
              title="确认删除此插件草稿？"
              okText="删除"
              cancelText="取消"
              okButtonProps={{ danger: true }}
              onConfirm={() => void handleDelete(row.id)}
            >
              <Button type="text" size="small" danger icon={<DeleteOutlined />} />
            </Popconfirm>
          </div>
        );
      },
    },
  ];

  const pagination = getLocalizedTablePagination(
    {
      current: page,
      pageSize: PAGE_SIZE,
      total: filteredRows.length,
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
        <Radio.Group
          value={typeFilter}
          onChange={(e) => { setTypeFilter(e.target.value as TypeFilter); setPage(1); }}
          size="small"
          style={{ flexShrink: 0 }}
        >
          <Radio.Button value="all">全部</Radio.Button>
          <Radio.Button value="builtin">内置</Radio.Button>
          <Radio.Button value="draft">自定义</Radio.Button>
        </Radio.Group>
        <Button onClick={handleReset}>{t('admin.memoryReset')}</Button>
      </div>

      <div className="memory-list-content">
        {filteredRows.length === 0 && !loading ? (
          <Empty
            description="还没有插件，点击「新建插件」开始创建"
            style={{ marginTop: 60 }}
          >
            <Button type="primary" icon={<PlusOutlined />} onClick={onNewPlugin}>
              新建插件
            </Button>
          </Empty>
        ) : (
          <Table<PluginRow>
            className="admin-page-table memory-table memory-skill-installed-table"
            rowKey={(row) => (row._type === 'builtin' ? `builtin_${row.id}` : row.id)}
            loading={loading}
            dataSource={pageRows}
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
