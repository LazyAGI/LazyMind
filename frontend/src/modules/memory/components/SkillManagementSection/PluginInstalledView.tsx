import { useState, useEffect, useCallback } from 'react';
import { Button, Empty, Input, Popconfirm, Radio, Switch, Table, Tag, Tooltip, message } from 'antd';
import { DeleteOutlined, EditOutlined, EyeOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { useNavigate } from 'react-router-dom';
import { getLocalizedTablePagination } from '@/components/ui/pagination';
import {
  listPluginDrafts,
  deletePluginDraft,
  updatePluginDraftContent,
  listBuiltinPlugins,
  listUserPluginSettings,
  setUserPluginEnabled,
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

const PAGE_SIZE = 10;

export default function PluginInstalledView({ t, onNewPlugin }: PluginInstalledViewProps) {
  const navigate = useNavigate();
  const [draftRecords, setDraftRecords] = useState<PluginDraftRecord[]>([]);
  const [builtinPlugins, setBuiltinPlugins] = useState<BuiltinPlugin[]>([]);
  const [enabledByRef, setEnabledByRef] = useState<Record<string, boolean>>({});
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
      const [draftsResp, builtins, pluginSettings] = await Promise.all([
        listPluginDrafts({ page: 1, pageSize: 200 }),
        listBuiltinPlugins(),
        listUserPluginSettings(),
      ]);
      setDraftRecords(draftsResp.records ?? []);
      setBuiltinPlugins(builtins);
      setEnabledByRef(Object.fromEntries(pluginSettings.map((item) => [item.plugin_ref, item.enabled])));
    } catch {
      message.error(t('admin.memoryPluginLoadFailed'));
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
      message.success(t('admin.memoryPluginDeleteSuccess'));
      void loadList();
    } catch {
      message.error(t('admin.memoryPluginDeleteFailed'));
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

  const handleEnabledChange = async (pluginRef: string, enabled: boolean) => {
    const previous = enabledByRef[pluginRef] ?? false;
    setEnabledByRef((current) => ({ ...current, [pluginRef]: enabled }));
    try { await setUserPluginEnabled(pluginRef, enabled); message.success(enabled ? 'Plugin 已默认启用' : 'Plugin 已默认关闭'); }
    catch { setEnabledByRef((current) => ({ ...current, [pluginRef]: previous })); message.error('Plugin 默认启用状态保存失败'); }
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
    message.success(t('admin.memoryPluginSaveSuccess'));
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
      title: t('admin.memoryPluginColId'),
      key: 'plugin_id',
      width: 240,
      render: (_: unknown, row: PluginRow) => {
        const pluginId = row._type === 'builtin' ? row.id : getDraftPluginId(row);
        const href =
          row._type === 'builtin'
            ? `/memory-management/plugins/builtin/${row.id}`
            : `/memory-management/plugins/${row.id}`;
        return (
          <Tooltip title={pluginId} mouseEnterDelay={0.4}>
          <Button
            type="link"
            style={{ fontFamily: 'monospace', padding: 0, display: 'block', width: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', textAlign: 'left' }}
            onClick={() => navigate(href)}
          >
            {pluginId}
          </Button>
          </Tooltip>
        );
      },
    },
    {
      title: t('admin.memoryPluginColName'),
      key: 'name',
      width: 220,
      render: (_: unknown, row: PluginRow) => {
        const href =
          row._type === 'builtin'
            ? `/memory-management/plugins/builtin/${row.id}`
            : `/memory-management/plugins/${row.id}`;
        return (
          <Tooltip title={row.name} mouseEnterDelay={0.4}>
          <Button type="link" style={{ padding: 0, display: 'block', width: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', textAlign: 'left' }} onClick={() => navigate(href)}>
            {row.name}
          </Button>
          </Tooltip>
        );
      },
    },
    {
      title: t('admin.memoryPluginColType'),
      key: 'type',
      width: 110,
      render: (_: unknown, row: PluginRow) => {
        if (row._type === 'builtin') return <Tag color="blue">{t('admin.memoryPluginTypeBuiltin')}</Tag>;
        if (row.source_type === 'skill') {
          const skillLabel = row.source_skill_name || row.source_skill_id || t('admin.memoryPluginTypeSkillUnknown');
          const skillId = row.source_skill_id;
          const tooltipContent = skillId ? (
            <span>
              {t('admin.memoryPluginTypeSkillTooltipPrefix')}{' '}
              <Button
                type="link"
                size="small"
                style={{ color: '#fff', padding: 0, height: 'auto', textDecoration: 'underline' }}
                onClick={(e) => { e.stopPropagation(); navigate(`/memory-management/skills/${skillId}`); }}
              >
                {skillLabel}
              </Button>
              {t('admin.memoryPluginTypeSkillTooltipSuffix') ? ` ${t('admin.memoryPluginTypeSkillTooltipSuffix')}` : ''}
            </span>
          ) : t('admin.memoryPluginTypeSkillTooltipNoId', { name: skillLabel });
          return (
            <Tooltip title={tooltipContent}>
              <Tag color="purple" style={{ cursor: 'default' }}>{t('admin.memoryPluginTypeSkill')}</Tag>
            </Tooltip>
          );
        }
        if (row.source_type === 'ai') return <Tag color="blue">{t('admin.memoryPluginTypeAi')}</Tag>;
        return <Tag>{t('admin.memoryPluginTypeCustom')}</Tag>;
      },
    },
    {
      title: t('admin.memoryPluginColStatus'),
      key: 'generate_status',
      width: 130,
      render: (_: unknown, row: PluginRow) => {
        if (row._type === 'builtin') return null;
        const status = row.generate_status;
        if (status === 'generating') return <Tag color="processing">{t('admin.memoryPluginStatusGenerating')}</Tag>;
        if (status === 'failed') return <Tag color="error">{t('admin.memoryPluginStatusFailed')}</Tag>;
        if (row.published) return <div style={{ display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}><Tag color="success" style={{ marginInlineEnd: 0 }}>已发布</Tag><Tag style={{ marginInlineEnd: 0 }}>v{row.current_revision_no}</Tag></div>;
        return <Tag>未发布</Tag>;
      },
    },
    {
      title: t('admin.memoryPluginColUpdatedAt'),
      key: 'updated_at',
      width: 180,
      render: (_: unknown, row: PluginRow) => {
        if (row._type === 'builtin') return '—';
        return <span style={{ whiteSpace: 'nowrap' }}>{new Date(row.updated_at).toLocaleString('zh-CN')}</span>;
      },
    },
    {
      title: '默认启用',
      key: 'default_enabled',
      width: 110,
      align: 'center',
      render: (_: unknown, row: PluginRow) => {
        const pluginRef = row._type === 'builtin' ? `builtin:${row.id}` : row.published_plugin_ref;
        if (!pluginRef) return <Tooltip title="发布后才可启用"><Switch size="small" disabled /></Tooltip>;
        return <Switch size="small" checked={enabledByRef[pluginRef] ?? (row._type === 'builtin')} onChange={(enabled) => void handleEnabledChange(pluginRef, enabled)} />;
      },
    },
    {
      title: t('common.actions'),
      key: 'actions',
      width: 96,
      render: (_: unknown, row: PluginRow) => {
        if (row._type === 'builtin') {
          return (
            <Tooltip title={t('admin.memoryPluginActionView')}>
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
            <Tooltip title={t('admin.memoryPluginActionEdit')}>
              <Button
                type="text"
                size="small"
                icon={<EditOutlined />}
                onClick={() => openInfoModal(row)}
              />
            </Tooltip>
            <Popconfirm
              title={t('admin.memoryPluginDeleteConfirm')}
              okText={t('admin.memoryPluginDeleteOk')}
              cancelText={t('common.cancel')}
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
          placeholder={t('admin.memoryPluginSearchPlaceholder')}
          className="memory-skill-installed-search"
        />
        <Radio.Group
          value={typeFilter}
          onChange={(e) => { setTypeFilter(e.target.value as TypeFilter); setPage(1); }}
          size="small"
          style={{ flexShrink: 0 }}
        >
          <Radio.Button value="all">{t('admin.memoryPluginFilterAll')}</Radio.Button>
          <Radio.Button value="builtin">{t('admin.memoryPluginFilterBuiltin')}</Radio.Button>
          <Radio.Button value="draft">{t('admin.memoryPluginFilterCustom')}</Radio.Button>
        </Radio.Group>
        <Button onClick={handleReset}>{t('admin.memoryReset')}</Button>
      </div>

      <div className="memory-list-content">
        {filteredRows.length === 0 && !loading ? (
          <Empty
            description={t('admin.memoryPluginEmptyDesc')}
            style={{ marginTop: 60 }}
          >
            <Button type="primary" icon={<PlusOutlined />} onClick={onNewPlugin}>
              {t('admin.memoryPluginNewButton')}
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
            tableLayout="fixed"
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={t('admin.memoryPluginEmptyNoResult')}
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
