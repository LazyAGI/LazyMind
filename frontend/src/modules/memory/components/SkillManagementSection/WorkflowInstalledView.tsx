import { getLocalizedErrorMessage } from "@/components/request";
import { useState, useEffect, useCallback, useRef } from 'react';
import { Alert, Button, Empty, Input, Popconfirm, Radio, Select, Spin, Table, Tag, Tooltip, message } from 'antd';
import { CloudDownloadOutlined, CopyOutlined, DeleteOutlined, EditOutlined, EyeOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { SelectProps } from 'antd';
import { useNavigate } from 'react-router-dom';
import { getLocalizedTablePagination } from '@/components/ui/pagination';
import {
  listWorkflowDrafts,
  copyWorkflowDraft,
  copyBuiltinWorkflow,
  deleteWorkflowDraft,
  updateWorkflowDraftContent,
  listBuiltinWorkflows,
  listUserWorkflowSettings,
  setUserWorkflowCallMode,
} from '@/modules/workflow/workflowDraftApi';
import type { WorkflowDraftRecord, BuiltinWorkflow, WorkflowCallMode, UserWorkflowSetting } from '@/modules/workflow/workflowDraftApi';
import WorkflowInfoModal from '@/modules/workflow/components/StateGraphEditor/WorkflowInfoModal';
import { parseWorkflowYaml } from '@/modules/workflow/components/StateGraphEditor/core/workflowParser';
import { serializeWorkflowModel } from '@/modules/workflow/components/StateGraphEditor/core/workflowSerializer';
import { createEmptyWorkflowModel } from '@/modules/workflow/components/StateGraphEditor/core/workflowModel';
import { parseScenario, serializeScenario } from '@/modules/workflow/components/StateGraphEditor/ScenarioEditor';
import { createEmptyModel } from '@/modules/workflow/components/StateGraphEditor/core/model';
import type { WorkflowModel } from '@/modules/workflow/components/StateGraphEditor/core/workflowModel';
import type { ScenarioData } from '@/modules/workflow/components/StateGraphEditor/ScenarioEditor';
import { useCloudResources } from '../../hooks/useCloudResources';
import { downloadCloudResource, type CloudResourceItem } from '../../cloudResourceApi';
import { isDesktopRuntime } from '@/runtime/mode';
import i18n from '@/i18n';

interface WorkflowInstalledViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  onNewWorkflow: () => void;
  tableScroll?: { x?: number; y?: number };
  listContentRef?: React.RefObject<HTMLDivElement>;
}

// Unified row type for the combined table.
type WorkflowRow =
  | ({ _type: 'draft' } & WorkflowDraftRecord)
  | ({ _type: 'builtin' } & BuiltinWorkflow & { updated_at?: never; generate_status?: never })
  | ({ _type: 'published'; id: string } & UserWorkflowSetting)
  | ({ _type: 'cloud'; id: string; name: string } & CloudResourceItem);

type TypeFilter = 'all' | 'builtin' | 'draft';
type CallModeOption = { value: WorkflowCallMode; label: string; title: string };

const PAGE_SIZE = 10;

export default function WorkflowInstalledView({
  t,
  onNewWorkflow,
  tableScroll,
  listContentRef,
}: WorkflowInstalledViewProps) {
  const navigate = useNavigate();
  const desktop = isDesktopRuntime();
  const cloud = useCloudResources("workflow");
  const [publishedWorkflows, setPublishedWorkflows] = useState<UserWorkflowSetting[]>([]);
  const [loadFailed, setLoadFailed] = useState(false);
  const [downloading, setDownloading] = useState<string>();
  const listRequest = useRef(0);
  const currentLocale = i18n.resolvedLanguage || i18n.language;
  const [draftRecords, setDraftRecords] = useState<WorkflowDraftRecord[]>([]);
  const [builtinWorkflows, setBuiltinWorkflows] = useState<BuiltinWorkflow[]>([]);
  const [callModeByRef, setCallModeByRef] = useState<Record<string, WorkflowCallMode>>({});
  const [callModePendingByRef, setCallModePendingByRef] = useState<Record<string, boolean>>({});
  const [loading, setLoading] = useState(false);
  const [copyingRowKey, setCopyingRowKey] = useState('');
  const [page, setPage] = useState(1);
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all');
  const [infoModalRecord, setInfoModalRecord] = useState<WorkflowDraftRecord | null>(null);
  const [infoModalWorkflowModel, setInfoModalWorkflowModel] = useState<WorkflowModel>(createEmptyWorkflowModel());
  const [infoModalScenarioData, setInfoModalScenarioData] = useState<ScenarioData>({ overview: '', stepDescriptions: {}, notes: '' });

  const loadList = useCallback(async () => {
    const request = ++listRequest.current;
    setLoading(true);
    const loadDrafts = async () => {
      const first = await listWorkflowDrafts({ page: 1, pageSize: desktop ? 100 : 200 });
      const records = [...(first.records ?? [])];
      if (desktop) {
        for (let page = 2; page <= Math.ceil(first.total / 100); page++) {
          if (request !== listRequest.current) return [];
          const next = await listWorkflowDrafts({ page, pageSize: 100 });
          records.push(...next.records);
        }
      }
      return records;
    };
    const [drafts, builtins, settings] = await Promise.allSettled([loadDrafts(), listBuiltinWorkflows(), listUserWorkflowSettings()]);
    if (request !== listRequest.current) return;
    setDraftRecords(drafts.status === 'fulfilled' ? drafts.value : []);
    setBuiltinWorkflows(builtins.status === 'fulfilled' ? builtins.value : []);
    const workflowSettings = settings.status === 'fulfilled' ? settings.value : [];
    setPublishedWorkflows(desktop ? workflowSettings.filter((item) => item.source_type !== 'builtin' && item.status === 'published') : []);
    setCallModeByRef(Object.fromEntries(workflowSettings.map((item) => [item.workflow_ref, item.call_mode ?? (item.enabled ? 'auto' : 'disabled')])));
    setLoadFailed([drafts, builtins, settings].some((result) => result.status === 'rejected'));
    setLoading(false);
  }, [desktop]);

  useEffect(() => {
    void loadList();
    return () => { listRequest.current++ };
  }, [loadList]);

  const handleDelete = async (id: string) => {
    try {
      await deleteWorkflowDraft(id);
      message.success(t('admin.memoryWorkflowDeleteSuccess'));
      void loadList();
    } catch {
    }
  };

  const handleCopy = async (row: WorkflowRow) => {
    if (row._type !== 'builtin' && row._type !== 'draft') return;
    const rowKey = row._type === 'builtin' ? `builtin:${row.id}` : row.id;
    setCopyingRowKey(rowKey);
    try {
      const copyName = t('admin.memoryWorkflowCopyName', { name: row.name });
      const copied = row._type === 'builtin'
        ? await copyBuiltinWorkflow(row.id, copyName)
        : await copyWorkflowDraft(row.id, copyName);
      message.success(t('admin.memoryWorkflowCopySuccess'));
      navigate(`/memory-management/workflows/${copied.id}`);
    } catch {
      message.error(t('admin.memoryWorkflowCopyFailed'));
    } finally {
      setCopyingRowKey('');
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

  const handleCallModeChange = async (workflowRef: string, callMode: WorkflowCallMode) => {
    const previous = callModeByRef[workflowRef] ?? 'disabled';
    setCallModeByRef((current) => ({ ...current, [workflowRef]: callMode }));
    setCallModePendingByRef((current) => ({ ...current, [workflowRef]: true }));
    try {
      await setUserWorkflowCallMode(workflowRef, callMode);
      message.success(t('admin.memoryWorkflowCallModeUpdated'));
    } catch (error) {
      setCallModeByRef((current) => ({ ...current, [workflowRef]: previous }));
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setCallModePendingByRef((current) => ({ ...current, [workflowRef]: false }));
    }
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

  const rowHref = (row: WorkflowRow) => {
    if (row._type === 'cloud') return `/memory-management/workflows/cloud/${encodeURIComponent(row.resource_id)}`;
    if (row._type === 'published') return `/memory-management/workflows/published/${encodeURIComponent(row.workflow_ref)}`;
    return row._type === 'builtin' ? `/memory-management/workflows/builtin/${row.id}` : `/memory-management/workflows/${row.id}`;
  };
  const rowWorkflowId = (row: WorkflowRow) => row._type === 'draft' ? getDraftWorkflowId(row) : row._type === 'published' ? row.workflow_id : row._type === 'cloud' ? '—' : row.id;
  const draftRefs = new Set(draftRecords.map((row) => row.published_workflow_ref).filter(Boolean));
  // Preserve local authoring rows and append only unbound Cloud resources.
  const allRows: WorkflowRow[] = [
    ...builtinWorkflows.map((b): WorkflowRow => ({ _type: 'builtin', ...b })),
    ...draftRecords.map((d): WorkflowRow => ({ _type: 'draft', ...d })),
    ...publishedWorkflows.filter((row) => !draftRefs.has(row.workflow_ref)).map((row): WorkflowRow => ({ _type: 'published', id: row.workflow_ref, ...row })),
    ...cloud.items.filter((row) => !row.local_exists && row.resource_type === 'workflow').map((row): WorkflowRow => ({ _type: 'cloud', id: row.resource_id, name: row.resource_name, ...row })),
  ];

  const q = query.trim().toLowerCase();
  const filteredRows = allRows.filter((row) => {
    if (typeFilter === 'builtin' && row._type !== 'builtin') return false;
    if (typeFilter === 'draft' && row._type === 'builtin') return false;
    if (q) {
      const name = row._type === 'builtin' ? row.name : row.name;
      const id = rowWorkflowId(row);
      if (!name.toLowerCase().includes(q) && !id.toLowerCase().includes(q)) return false;
    }
    return true;
  });

  // Client-side pagination.
  const currentPage = Math.min(page, Math.max(1, Math.ceil(filteredRows.length / PAGE_SIZE)));
  useEffect(() => { setPage((current) => Math.min(current, Math.max(1, Math.ceil(filteredRows.length / PAGE_SIZE)))) }, [filteredRows.length]);
  const pageStart = (currentPage - 1) * PAGE_SIZE;
  const pageRows = filteredRows.slice(pageStart, pageStart + PAGE_SIZE);

  const columns: ColumnsType<WorkflowRow> = [
    {
      title: t('admin.memoryWorkflowColId'),
      key: 'workflow_id',
      width: 240,
      render: (_: unknown, row: WorkflowRow) => {
        const workflowId = rowWorkflowId(row);
        const href = rowHref(row);
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
        const href = rowHref(row);
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
        if (row._type === 'cloud') return <Tag color="blue">{t('admin.memoryResourceCloud')}</Tag>;
        if (row._type === 'published') return <Tag>{t('admin.memoryResourceLocal')}</Tag>;
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
        if (row._type === 'cloud') return null;
        if (row._type === 'published') return <Tag color="success">{t('admin.memoryWorkflowStatusPublished')} v{row.revision_no}</Tag>;
        const status = row.generate_status;
        if (status === 'generating') return <Tag color="processing">{t('admin.memoryWorkflowStatusGenerating')}</Tag>;
        if (status === 'failed') return <Tag color="error">{t('admin.memoryWorkflowStatusFailed')}</Tag>;
        if (row.published) return <div style={{ display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}><Tag color="success" style={{ marginInlineEnd: 0 }}>{t('admin.memoryWorkflowStatusPublished')}</Tag><Tag style={{ marginInlineEnd: 0 }}>v{row.current_revision_no}</Tag></div>;
        return <Tag>{t('admin.memoryWorkflowStatusUnpublished')}</Tag>;
      },
    },
    {
      title: t('admin.memoryWorkflowColUpdatedAt'),
      key: 'updated_at',
      width: 180,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'builtin' || row._type === 'published') return '—';
        return <span style={{ whiteSpace: 'nowrap' }}>{new Date(row.updated_at).toLocaleString(currentLocale)}</span>;
      },
    },
    {
      title: t('admin.memoryWorkflowColCallMode'),
      key: 'call_mode',
      width: 180,
      align: 'center',
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'cloud') return null;
        const workflowRef = row._type === 'builtin' ? `builtin:${row.id}` : row._type === 'published' ? row.workflow_ref : row.published_workflow_ref;
        const callMode = callModeByRef[workflowRef] ?? (row._type === 'builtin' ? 'auto' : 'disabled');
        const options: CallModeOption[] = [
          { value: 'auto', label: t('admin.memoryWorkflowCallModeAuto'), title: t('admin.memoryWorkflowCallModeAutoDesc') },
          { value: 'manual', label: t('admin.memoryWorkflowCallModeManual'), title: t('admin.memoryWorkflowCallModeManualDesc') },
          { value: 'disabled', label: t('admin.memoryWorkflowCallModeDisabled'), title: t('admin.memoryWorkflowCallModeDisabledDesc') },
        ];
        const renderOption: NonNullable<SelectProps<WorkflowCallMode, CallModeOption>['optionRender']> = (option) => (
          <div className="memory-skill-call-mode-option">
            <div className="memory-skill-call-mode-option__label">{option.data.label}</div>
            <div className="memory-skill-call-mode-option__description">{option.data.title}</div>
          </div>
        );
        const select = (
          <Select<WorkflowCallMode, CallModeOption>
            size="small"
            value={callMode}
            options={options}
            className={`memory-skill-call-mode-select is-${callMode}`}
            variant="borderless"
            popupMatchSelectWidth={300}
            classNames={{ popup: { root: 'memory-skill-call-mode-dropdown' } }}
            loading={Boolean(callModePendingByRef[workflowRef])}
            disabled={!workflowRef || Boolean(callModePendingByRef[workflowRef])}
            aria-label={`${t('admin.memoryWorkflowColCallMode')}: ${row.name}`}
            style={{ width: 160, textAlign: 'left' }}
            optionRender={renderOption}
            onChange={(value: WorkflowCallMode) => void handleCallModeChange(workflowRef, value)}
          />
        );
        return workflowRef ? select : <Tooltip title={t('admin.memoryWorkflowCallModePublishHint')}>{select}</Tooltip>;
      },
    },
    {
      title: t('common.actions'),
      key: 'actions',
      width: 120,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'cloud' || row._type === 'published') {
          return <div className="workflow-list-actions">
            <Button type="text" size="small" icon={<EyeOutlined />} aria-label={t('admin.memoryCloudViewDetail')} onClick={() => navigate(rowHref(row))} />
            {row._type === 'cloud' ? <Button type="text" size="small" icon={<CloudDownloadOutlined />} aria-label={t('admin.memoryCloudDownload')} loading={downloading === row.id}
              disabled={!['download_required', 'local_missing'].includes(row.presence_status)} onClick={async () => {
                if (downloading === row.id) return;
                setDownloading(row.id);
                try { await downloadCloudResource('workflow', row.resource_id); await loadList(); await cloud.reload(); message.success(t('admin.memoryCloudDownloadSuccess', { name: row.name })) }
                catch { message.error(t('admin.memoryCloudDownloadFailed')) }
                finally { setDownloading(undefined) }
              }} /> : null}
          </div>;
        }
        if (row._type === 'builtin') {
          return (
            <div className="workflow-list-actions">
              <Tooltip title={t('admin.memoryWorkflowActionView')}>
                <Button
                  type="text"
                  size="small"
                  icon={<EyeOutlined />}
                  onClick={() => navigate(`/memory-management/workflows/builtin/${row.id}`)}
                />
              </Tooltip>
              <Tooltip title={t('admin.memoryWorkflowActionCopy')}>
                <Button
                  type="text"
                  size="small"
                  aria-label={t('admin.memoryWorkflowActionCopy')}
                  icon={<CopyOutlined />}
                  loading={copyingRowKey === `builtin:${row.id}`}
                  disabled={Boolean(copyingRowKey)}
                  onClick={() => void handleCopy(row)}
                />
              </Tooltip>
            </div>
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
            <Tooltip title={t('admin.memoryWorkflowActionCopy')}>
              <Button
                type="text"
                size="small"
                aria-label={t('admin.memoryWorkflowActionCopy')}
                icon={<CopyOutlined />}
                loading={copyingRowKey === row.id}
                disabled={Boolean(copyingRowKey)}
                onClick={() => void handleCopy(row)}
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
      current: currentPage,
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

      {loadFailed ? <Alert type="error" showIcon message={t('admin.memoryResourceLocalLoadFailed')} action={<Button onClick={() => void loadList()}>{t('common.retry')}</Button>} /> : null}
      {cloud.error ? <Alert type="error" showIcon message={t('admin.memoryCloudLoadFailed')} action={<Button onClick={() => void cloud.reload()}>{t('common.retry')}</Button>} /> : null}
      {cloud.loading ? <div role="status"><Spin size="small" /> {t('admin.memoryCloudLoading')}</div> : null}
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
            rowKey={(row) => `${row._type}:${row.id}`}
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
