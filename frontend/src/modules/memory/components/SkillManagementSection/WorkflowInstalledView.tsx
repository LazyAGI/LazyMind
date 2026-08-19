import { useState, useEffect, useCallback } from 'react';
import { Button, Empty, Input, Popconfirm, Radio, Switch, Table, Tag, Tooltip, message } from 'antd';
import { DeleteOutlined, EditOutlined, EyeOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { useNavigate } from 'react-router-dom';
import { getLocalizedTablePagination } from '@/components/ui/pagination';
import {
  listWorkflowDrafts,
  deleteWorkflowDraft,
  updateWorkflowDraftContent,
  listBuiltinWorkflows,
  listUserWorkflowSettings,
  setUserWorkflowEnabled,
} from '@/modules/workflow/workflowDraftApi';
import type { WorkflowDraftRecord, BuiltinWorkflow } from '@/modules/workflow/workflowDraftApi';
import WorkflowInfoModal from '@/modules/workflow/components/StateGraphEditor/WorkflowInfoModal';
import { parseWorkflowYaml } from '@/modules/workflow/components/StateGraphEditor/core/workflowParser';
import { serializeWorkflowModel } from '@/modules/workflow/components/StateGraphEditor/core/workflowSerializer';
import { createEmptyWorkflowModel } from '@/modules/workflow/components/StateGraphEditor/core/workflowModel';
import { parseScenario, serializeScenario } from '@/modules/workflow/components/StateGraphEditor/ScenarioEditor';
import { createEmptyModel } from '@/modules/workflow/components/StateGraphEditor/core/model';
import type { WorkflowModel } from '@/modules/workflow/components/StateGraphEditor/core/workflowModel';
import type { ScenarioData } from '@/modules/workflow/components/StateGraphEditor/ScenarioEditor';

interface WorkflowInstalledViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  onNewWorkflow: () => void;
  tableScroll?: { x?: number; y?: number };
  listContentRef?: React.RefObject<HTMLDivElement>;
}

// Unified row type for the combined table.
type WorkflowRow =
  | ({ _type: 'draft' } & WorkflowDraftRecord)
  | ({ _type: 'builtin' } & BuiltinWorkflow & { updated_at?: never; generate_status?: never });

type TypeFilter = 'all' | 'builtin' | 'draft';

const PAGE_SIZE = 10;

export default function WorkflowInstalledView({
  t,
  onNewWorkflow,
  tableScroll,
  listContentRef,
}: WorkflowInstalledViewProps) {
  const navigate = useNavigate();
  const [draftRecords, setDraftRecords] = useState<WorkflowDraftRecord[]>([]);
  const [builtinWorkflows, setBuiltinWorkflows] = useState<BuiltinWorkflow[]>([]);
  const [enabledByRef, setEnabledByRef] = useState<Record<string, boolean>>({});
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all');
  const [infoModalRecord, setInfoModalRecord] = useState<WorkflowDraftRecord | null>(null);
  const [infoModalWorkflowModel, setInfoModalWorkflowModel] = useState<WorkflowModel>(createEmptyWorkflowModel());
  const [infoModalScenarioData, setInfoModalScenarioData] = useState<ScenarioData>({ overview: '', stepDescriptions: {}, notes: '' });

  const loadList = useCallback(async () => {
    setLoading(true);
    try {
      const [draftsResp, builtins, workflowSettings] = await Promise.all([
        listWorkflowDrafts({ page: 1, pageSize: 200 }),
        listBuiltinWorkflows(),
        listUserWorkflowSettings(),
      ]);
      setDraftRecords(draftsResp.records ?? []);
      setBuiltinWorkflows(builtins);
      setEnabledByRef(Object.fromEntries(workflowSettings.map((item) => [item.workflow_ref, item.enabled])));
    } catch {
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadList();
  }, [loadList]);

  const handleDelete = async (id: string) => {
    try {
      await deleteWorkflowDraft(id);
      message.success(t('admin.memoryWorkflowDeleteSuccess'));
      void loadList();
    } catch {
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

  const handleEnabledChange = async (workflowRef: string, enabled: boolean) => {
    const previous = enabledByRef[workflowRef] ?? false;
    setEnabledByRef((current) => ({ ...current, [workflowRef]: enabled }));
    try { await setUserWorkflowEnabled(workflowRef, enabled); message.success(enabled ? 'Workflow 已默认启用' : 'Workflow 已默认关闭'); }
    catch { setEnabledByRef((current) => ({ ...current, [workflowRef]: previous })); }
  };

  const openInfoModal = (record: WorkflowDraftRecord) => {
    const pm = parseWorkflowYaml(record.workflow_yaml_content) ?? createEmptyWorkflowModel();
    if (!pm.name && record.name) pm.name = record.name;
    const graphModel = createEmptyModel();
    const sd = parseScenario(record.scenario_content ?? '', graphModel.nodes);
    setInfoModalWorkflowModel(pm);
    setInfoModalScenarioData(sd);
    setInfoModalRecord(record);
  };

  const handleInfoSave = async (pm: WorkflowModel, sd: ScenarioData) => {
    if (!infoModalRecord) return;
    const workflowYaml = serializeWorkflowModel(pm);
    const scenarioContent = serializeScenario([], sd);
    await updateWorkflowDraftContent(infoModalRecord.id, {
      workflow_yaml_content: workflowYaml,
      scenario_content: scenarioContent,
      version: infoModalRecord.version,
    });
    message.success(t('admin.memoryWorkflowSaveSuccess'));
    void loadList();
  };

  const getDraftWorkflowId = (record: WorkflowDraftRecord): string => {
    if (!record.workflow_yaml_content) return '—';
    const pm = parseWorkflowYaml(record.workflow_yaml_content);
    return pm?.id || '—';
  };

  // Build combined rows and apply filters.
  const allRows: WorkflowRow[] = [
    ...builtinWorkflows.map((b): WorkflowRow => ({ _type: 'builtin', ...b })),
    ...draftRecords.map((d): WorkflowRow => ({ _type: 'draft', ...d })),
  ];

  const q = query.trim().toLowerCase();
  const filteredRows = allRows.filter((row) => {
    if (typeFilter === 'builtin' && row._type !== 'builtin') return false;
    if (typeFilter === 'draft' && row._type !== 'draft') return false;
    if (q) {
      const name = row._type === 'builtin' ? row.name : row.name;
      const id = row._type === 'builtin' ? row.id : getDraftWorkflowId(row);
      if (!name.toLowerCase().includes(q) && !id.toLowerCase().includes(q)) return false;
    }
    return true;
  });

  // Client-side pagination.
  const pageStart = (page - 1) * PAGE_SIZE;
  const pageRows = filteredRows.slice(pageStart, pageStart + PAGE_SIZE);

  const columns: ColumnsType<WorkflowRow> = [
    {
      title: t('admin.memoryWorkflowColId'),
      key: 'workflow_id',
      width: 240,
      render: (_: unknown, row: WorkflowRow) => {
        const workflowId = row._type === 'builtin' ? row.id : getDraftWorkflowId(row);
        const href =
          row._type === 'builtin'
            ? `/memory-management/workflows/builtin/${row.id}`
            : `/memory-management/workflows/${row.id}`;
        return (
          <Tooltip title={workflowId} mouseEnterDelay={0.4}>
          <Button
            type="link"
            style={{ fontFamily: 'monospace', padding: 0, display: 'block', width: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', textAlign: 'left' }}
            onClick={() => navigate(href)}
          >
            {workflowId}
          </Button>
          </Tooltip>
        );
      },
    },
    {
      title: t('admin.memoryWorkflowColName'),
      key: 'name',
      width: 220,
      render: (_: unknown, row: WorkflowRow) => {
        const href =
          row._type === 'builtin'
            ? `/memory-management/workflows/builtin/${row.id}`
            : `/memory-management/workflows/${row.id}`;
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
      title: t('admin.memoryWorkflowColType'),
      key: 'type',
      width: 110,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'builtin') return <Tag color="blue">{t('admin.memoryWorkflowTypeBuiltin')}</Tag>;
        if (row.source_type === 'skill') {
          const skillLabel = row.source_skill_name || row.source_skill_id || t('admin.memoryWorkflowTypeSkillUnknown');
          const skillId = row.source_skill_id;
          const tooltipContent = skillId ? (
            <span>
              {t('admin.memoryWorkflowTypeSkillTooltipPrefix')}{' '}
              <Button
                type="link"
                size="small"
                style={{ color: '#fff', padding: 0, height: 'auto', textDecoration: 'underline' }}
                onClick={(e) => { e.stopPropagation(); navigate(`/memory-management/skills/${skillId}`); }}
              >
                {skillLabel}
              </Button>
              {t('admin.memoryWorkflowTypeSkillTooltipSuffix') ? ` ${t('admin.memoryWorkflowTypeSkillTooltipSuffix')}` : ''}
            </span>
          ) : t('admin.memoryWorkflowTypeSkillTooltipNoId', { name: skillLabel });
          return (
            <Tooltip title={tooltipContent}>
              <Tag color="purple" style={{ cursor: 'default' }}>{t('admin.memoryWorkflowTypeSkill')}</Tag>
            </Tooltip>
          );
        }
        if (row.source_type === 'ai') return <Tag color="blue">{t('admin.memoryWorkflowTypeAi')}</Tag>;
        return <Tag>{t('admin.memoryWorkflowTypeCustom')}</Tag>;
      },
    },
    {
      title: t('admin.memoryWorkflowColStatus'),
      key: 'generate_status',
      width: 130,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'builtin') return null;
        const status = row.generate_status;
        if (status === 'generating') return <Tag color="processing">{t('admin.memoryWorkflowStatusGenerating')}</Tag>;
        if (status === 'failed') return <Tag color="error">{t('admin.memoryWorkflowStatusFailed')}</Tag>;
        if (row.published) return <div style={{ display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}><Tag color="success" style={{ marginInlineEnd: 0 }}>已发布</Tag><Tag style={{ marginInlineEnd: 0 }}>v{row.current_revision_no}</Tag></div>;
        return <Tag>未发布</Tag>;
      },
    },
    {
      title: t('admin.memoryWorkflowColUpdatedAt'),
      key: 'updated_at',
      width: 180,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'builtin') return '—';
        return <span style={{ whiteSpace: 'nowrap' }}>{new Date(row.updated_at).toLocaleString('zh-CN')}</span>;
      },
    },
    {
      title: '默认启用',
      key: 'default_enabled',
      width: 110,
      align: 'center',
      render: (_: unknown, row: WorkflowRow) => {
        const workflowRef = row._type === 'builtin' ? `builtin:${row.id}` : row.published_workflow_ref;
        if (!workflowRef) return <Tooltip title="发布后才可启用"><Switch size="small" disabled /></Tooltip>;
        return <Switch size="small" checked={enabledByRef[workflowRef] ?? (row._type === 'builtin')} onChange={(enabled) => void handleEnabledChange(workflowRef, enabled)} />;
      },
    },
    {
      title: t('common.actions'),
      key: 'actions',
      width: 96,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'builtin') {
          return (
            <Tooltip title={t('admin.memoryWorkflowActionView')}>
              <Button
                type="text"
                size="small"
                icon={<EyeOutlined />}
                onClick={() => navigate(`/memory-management/workflows/builtin/${row.id}`)}
              />
            </Tooltip>
          );
        }
        return (
          <div className="workflow-list-actions">
            <Tooltip title={t('admin.memoryWorkflowActionEdit')}>
              <Button
                type="text"
                size="small"
                icon={<EditOutlined />}
                onClick={() => openInfoModal(row)}
              />
            </Tooltip>
            <Popconfirm
              title={t('admin.memoryWorkflowDeleteConfirm')}
              okText={t('admin.memoryWorkflowDeleteOk')}
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
          placeholder={t('admin.memoryWorkflowSearchPlaceholder')}
          className="memory-skill-installed-search"
        />
        <Radio.Group
          value={typeFilter}
          onChange={(e) => { setTypeFilter(e.target.value as TypeFilter); setPage(1); }}
          size="small"
          style={{ flexShrink: 0 }}
        >
          <Radio.Button value="all">{t('admin.memoryWorkflowFilterAll')}</Radio.Button>
          <Radio.Button value="builtin">{t('admin.memoryWorkflowFilterBuiltin')}</Radio.Button>
          <Radio.Button value="draft">{t('admin.memoryWorkflowFilterCustom')}</Radio.Button>
        </Radio.Group>
        <Button onClick={handleReset}>{t('admin.memoryReset')}</Button>
      </div>

      <div className="memory-list-content" ref={listContentRef}>
        {filteredRows.length === 0 && !loading ? (
          <Empty
            description={t('admin.memoryWorkflowEmptyDesc')}
            style={{ marginTop: 60 }}
          >
            <Button type="primary" icon={<PlusOutlined />} onClick={onNewWorkflow}>
              {t('admin.memoryWorkflowNewButton')}
            </Button>
          </Empty>
        ) : (
          <Table<WorkflowRow>
            className="admin-page-table memory-table memory-skill-installed-table"
            rowKey={(row) => (row._type === 'builtin' ? `builtin_${row.id}` : row.id)}
            loading={loading}
            dataSource={pageRows}
            columns={columns}
            pagination={pagination}
            tableLayout="fixed"
            scroll={tableScroll}
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={t('admin.memoryWorkflowEmptyNoResult')}
                />
              ),
            }}
          />
        )}
      </div>

      {infoModalRecord && (
        <WorkflowInfoModal
          open={!!infoModalRecord}
          onCancel={() => setInfoModalRecord(null)}
          workflowModel={infoModalWorkflowModel}
          scenarioData={infoModalScenarioData}
          onSave={handleInfoSave}
        />
      )}
    </div>
  );
}
