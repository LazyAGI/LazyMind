import { getLocalizedErrorMessage } from "@/components/request";
import { useState, useEffect, useCallback, useRef } from 'react';
import { Button, Empty, Popconfirm, Select, Table, Tooltip, message } from 'antd';
import ExpandableTitleSearch from './ExpandableTitleSearch';
import { OriginMark, StatusMark } from './OriginMark';
import SkillModuleState from './SkillModuleState';
import { ApartmentOutlined, CloudDownloadOutlined, CopyOutlined, DeleteOutlined, EditOutlined, EyeOutlined, PlusOutlined } from '@ant-design/icons';
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
  searchInput?: string;
  query?: string;
  onSearchInputChange?: (value: string) => void;
  onSearch?: (value: string) => void;
  hideSearch?: boolean;
}

// Unified row type for the combined table.
type WorkflowRow =
  | ({ _type: 'draft' } & WorkflowDraftRecord)
  | ({ _type: 'builtin' } & BuiltinWorkflow & { updated_at?: never; generate_status?: never })
  | ({ _type: 'published'; id: string } & UserWorkflowSetting)
  | ({ _type: 'cloud'; id: string; name: string } & CloudResourceItem);

type TypeFilter = 'all' | 'builtin' | 'skill' | 'ai' | 'custom';

function matchesWorkflowTypeFilter(row: WorkflowRow, filter: TypeFilter): boolean {
  if (filter === 'all') {
    return true;
  }
  if (filter === 'builtin') {
    return row._type === 'builtin';
  }
  if (row._type === 'builtin' || row._type === 'cloud') {
    return false;
  }
  if (filter === 'skill') {
    return row.source_type === 'skill';
  }
  if (filter === 'ai') {
    return row.source_type === 'ai';
  }
  return row.source_type !== 'skill' && row.source_type !== 'ai';
}
type CallModeOption = { value: WorkflowCallMode; label: string; title: string };

const defaultPageSizeOptions = [20, 50, 100];

export default function WorkflowInstalledView({
  t,
  onNewWorkflow,
  tableScroll,
  listContentRef,
  searchInput: searchInputProp,
  query: queryProp,
  onSearchInputChange,
  onSearch,
  hideSearch = false,
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
  const [pageSize, setPageSize] = useState(defaultPageSizeOptions[0]);
  const [localSearchInput, setLocalSearchInput] = useState('');
  const [localQuery, setLocalQuery] = useState('');
  const searchInput = searchInputProp ?? localSearchInput;
  const query = queryProp ?? localQuery;
  const setSearchInput = onSearchInputChange ?? setLocalSearchInput;
  const setQuery = onSearch ?? setLocalQuery;
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
    setSearchInput(value);
    setQuery(value);
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

  useEffect(() => {
    setPage(1);
  }, [query]);

  const q = query.trim().toLowerCase();
  const filteredRows = allRows.filter((row) => {
    if (!matchesWorkflowTypeFilter(row, typeFilter)) return false;
    if (q) {
      const name = row._type === 'builtin' ? row.name : row.name;
      const id = rowWorkflowId(row);
      if (!name.toLowerCase().includes(q) && !id.toLowerCase().includes(q)) return false;
    }
    return true;
  });

  // Client-side pagination.
  const currentPage = Math.min(page, Math.max(1, Math.ceil(filteredRows.length / pageSize)));
  useEffect(() => { setPage((current) => Math.min(current, Math.max(1, Math.ceil(filteredRows.length / pageSize)))) }, [filteredRows.length, pageSize]);
  const pageStart = (currentPage - 1) * pageSize;
  const pageRows = filteredRows.slice(pageStart, pageStart + pageSize);

  const columns: ColumnsType<WorkflowRow> = [
    {
      title: t('admin.memoryNameDesc'),
      key: 'name',
      width: 280,
      render: (_: unknown, row: WorkflowRow) => {
        const href = rowHref(row);
        const workflowId = rowWorkflowId(row);
        return (
          <div className="memory-table-main memory-table-main-with-icon">
            <span className="memory-table-main-icon" aria-hidden="true">
              <ApartmentOutlined />
            </span>
            <div className="memory-table-main-copy">
              <div className="memory-table-main-title">
                <button type="button" className="memory-term-link" onClick={() => navigate(href)}>
                  {row.name}
                </button>
              </div>
              <Tooltip title={workflowId} mouseEnterDelay={0.4}>
                <div className="memory-table-main-desc">{workflowId}</div>
              </Tooltip>
            </div>
          </div>
        );
      },
    },
    {
      title: t('admin.memorySkillOrigin'),
      key: 'type',
      width: 110,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'builtin') return <OriginMark kind="builtin">{t('admin.memoryWorkflowTypeBuiltin')}</OriginMark>;
        if (row._type === 'cloud') return <OriginMark kind="cloud">{t('admin.memoryResourceCloud')}</OriginMark>;
        if (row._type === 'published') return <OriginMark kind="local">{t('admin.memoryResourceLocal')}</OriginMark>;
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
              <OriginMark kind="skill">{t('admin.memoryWorkflowTypeSkill')}</OriginMark>
            </Tooltip>
          );
        }
        if (row.source_type === 'ai') return <OriginMark kind="ai">{t('admin.memoryWorkflowTypeAi')}</OriginMark>;
        return <OriginMark kind="custom">{t('admin.memoryWorkflowTypeCustom')}</OriginMark>;
      },
    },
    {
      title: t('admin.memoryWorkflowColStatus'),
      key: 'generate_status',
      width: 130,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'builtin') return null;
        if (row._type === 'cloud') return null;
        if (row._type === 'published') {
          return (
            <span className="memory-status-cluster">
              <StatusMark kind="published">{t('admin.memoryWorkflowStatusPublished')}</StatusMark>
              <StatusMark kind="revision">v{row.revision_no}</StatusMark>
            </span>
          );
        }
        const status = row.generate_status;
        if (status === 'generating') {
          return <StatusMark kind="generating">{t('admin.memoryWorkflowStatusGenerating')}</StatusMark>;
        }
        if (status === 'failed') {
          return <StatusMark kind="failed">{t('admin.memoryWorkflowStatusFailed')}</StatusMark>;
        }
        if (row.published) {
          return (
            <span className="memory-status-cluster">
              <StatusMark kind="published">{t('admin.memoryWorkflowStatusPublished')}</StatusMark>
              <StatusMark kind="revision">v{row.current_revision_no}</StatusMark>
            </span>
          );
        }
        return <StatusMark kind="unpublished">{t('admin.memoryWorkflowStatusUnpublished')}</StatusMark>;
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
      width: 110,
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
            style={{ width: '100%', maxWidth: 110, textAlign: 'left' }}
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
      width: 132,
      render: (_: unknown, row: WorkflowRow) => {
        if (row._type === 'cloud' || row._type === 'published') {
          return <div className="memory-resource-actions">
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
            <div className="memory-resource-actions">
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
          <div className="memory-resource-actions">
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
      pageSize,
      total: filteredRows.length,
      showSizeChanger: true,
      pageSizeOptions: defaultPageSizeOptions,
      showTotal: (itemTotal) => t('common.totalItems', { total: itemTotal }),
      onChange: (nextPage, nextPageSize) => {
        setPage(nextPage);
        if (nextPageSize && nextPageSize !== pageSize) {
          setPageSize(nextPageSize);
          setPage(1);
        }
      },
      onShowSizeChange: (_current, nextPageSize) => {
        setPageSize(nextPageSize);
        setPage(1);
      },
    },
    t,
  );

  return (
    <div className="memory-skill-installed">
      <div className="memory-skill-installed-filters">
        <div className="memory-skill-source-tabs" role="tablist">
          {([
            ['all', 'admin.memoryWorkflowFilterAll'],
            ['builtin', 'admin.memoryWorkflowFilterBuiltin'],
            ['skill', 'admin.memoryWorkflowFilterCustom'],
            ['ai', 'admin.memoryWorkflowFilterAi'],
            ['custom', 'admin.memoryWorkflowFilterBlank'],
          ] as const).map(([value, labelKey]) => (
            <button
              type="button"
              role="tab"
              aria-selected={typeFilter === value}
              className={typeFilter === value ? 'is-active' : undefined}
              key={value}
              onClick={() => {
                setTypeFilter(value);
                setPage(1);
              }}
            >
              {t(labelKey)}
            </button>
          ))}
        </div>
        {hideSearch ? null : (
          <ExpandableTitleSearch
            value={searchInput}
            placeholder={t('admin.memoryWorkflowSearchPlaceholder')}
            onChange={setSearchInput}
            onSearch={handleSearch}
          />
        )}
      </div>

      {loadFailed ? (
        <SkillModuleState tone="error" retryLabel={t('common.retry')} onRetry={() => void loadList()}>
          {t('admin.memoryResourceLocalLoadFailed')}
        </SkillModuleState>
      ) : null}
      {cloud.error ? (
        <SkillModuleState tone="error" retryLabel={t('common.retry')} onRetry={() => void cloud.reload()}>
          {t('admin.memoryCloudLoadFailed')}
        </SkillModuleState>
      ) : null}
      {cloud.loading ? (
        <SkillModuleState tone="loading">{t('admin.memoryCloudLoading')}</SkillModuleState>
      ) : null}
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
