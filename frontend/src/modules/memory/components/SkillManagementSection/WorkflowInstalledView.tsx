import { getLocalizedErrorMessage } from "@/components/request";
import { useState, useEffect, useCallback, useRef } from 'react';
import { Button, Empty, Popconfirm, Select, Table, Tooltip, message } from 'antd';
import ExpandableTitleSearch from './ExpandableTitleSearch';
import { OriginMark, StatusMark } from './OriginMark';
import SkillModuleState from './SkillModuleState';
import { CloudDownloadOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
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
import WorkflowIcon from '@/modules/workflow/components/WorkflowIcon';
import { getWorkflowIcon, getWorkflowPriority } from '@/modules/workflow/workflowPresentation';
import { useWorkflowPageSize } from '../../hooks/useWorkflowPageSize';
import { CHAT_HOME_PATH } from '@/modules/chat/constants/chat';

interface WorkflowInstalledViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  onNewWorkflow: () => void;
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

export default function WorkflowInstalledView({
  t,
  onNewWorkflow,
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
  const listContentRef = useRef<HTMLDivElement>(null);
  const pageSize = useWorkflowPageSize(listContentRef);
  const previousPageSize = useRef(pageSize);
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
      const fetchPageSize = 100; // The API only accepts page sizes up to 100.
      const first = await listWorkflowDrafts({ page: 1, pageSize: fetchPageSize });
      const records = [...(first.records ?? [])];
      for (let page = 2; page <= Math.ceil(first.total / fetchPageSize); page++) {
        if (request !== listRequest.current) return [];
        const next = await listWorkflowDrafts({ page, pageSize: fetchPageSize });
        records.push(...next.records);
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

  // Stable sorting of the complete matching result must precede pagination.
  filteredRows.sort((left, right) =>
    getWorkflowPriority(left._type === 'builtin' ? left.id : undefined)
    - getWorkflowPriority(right._type === 'builtin' ? right.id : undefined));

  const renderUseAction = (row: WorkflowRow, asLogo = false) => {
    const workflowRef = row._type === 'builtin' ? `builtin:${row.id}`
      : row._type === 'published' ? row.workflow_ref
      : row._type === 'draft' && row.published ? row.published_workflow_ref : undefined;
    const unavailable = row._type === 'cloud' ? t('admin.memoryWorkflowUseDownloadHint')
      : !workflowRef ? t('admin.memoryWorkflowCallModePublishHint')
      : loadFailed ? t('admin.memoryResourceLocalLoadFailed')
      : callModeByRef[workflowRef] === 'disabled' ? t('chat.mentionWorkflowDisabled') : undefined;
    return <Tooltip placement="top" title={unavailable || t('admin.memoryWorkflowActionUse')}>
      <Button type="text" size="small"
        className={asLogo ? 'memory-table-main-icon memory-workflow-icon' : undefined}
        aria-label={asLogo ? `${t('admin.memoryWorkflowActionUse')}: ${row.name}` : t('admin.memoryWorkflowActionUse')}
        disabled={Boolean(unavailable)}
        icon={<WorkflowIcon name={asLogo ? getWorkflowIcon(row._type === 'builtin' ? row.id : undefined) : 'circle-arrow-right'} size={asLogo ? 20 : 16} />}
        onClick={() => {
          if (!workflowRef || unavailable) return;
          navigate(CHAT_HOME_PATH, { state: { workflowLaunch: { workflowRef, name: row.name } } });
        }} />
    </Tooltip>;
  };

  // Client-side pagination.
  const currentPage = Math.min(page, Math.max(1, Math.ceil(filteredRows.length / pageSize)));
  useEffect(() => {
    const oldPageSize = previousPageSize.current;
    previousPageSize.current = pageSize;
    // Keep the first previously visible item on screen when the viewport changes.
    setPage(current => Math.min(
      Math.floor(((current - 1) * oldPageSize) / pageSize) + 1,
      Math.max(1, Math.ceil(filteredRows.length / pageSize)),
    ));
  }, [filteredRows.length, pageSize]);
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
            {renderUseAction(row, true)}
            <div className="memory-table-main-copy">
              <div className="memory-table-main-title">
                <Tooltip title={row.name} mouseEnterDelay={0.4}>
                  <button type="button" className="memory-term-link" onClick={() => navigate(href)}>
                    {row.name}
                  </button>
                </Tooltip>
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
      width: 164,
      render: (_: unknown, row: WorkflowRow) => {
        const canCopy = row._type === 'builtin' || row._type === 'draft';
        const detailAction = row._type === 'draft' ? 'admin.memoryWorkflowActionEdit' : 'admin.memoryWorkflowActionView';
        const rowKey = row._type === 'builtin' ? `builtin:${row.id}` : row.id;
        return (
          <div className="memory-resource-actions memory-workflow-actions">
            {renderUseAction(row)}
            <Tooltip placement="top" title={t(detailAction)}>
              <Button type="text" size="small" aria-label={t(detailAction)}
                icon={row._type === 'draft' ? <EditOutlined /> : <WorkflowIcon name="eye" size={16} />}
                onClick={() => row._type === 'draft' ? openInfoModal(row) : navigate(rowHref(row))} />
            </Tooltip>
            {canCopy && <Tooltip placement="top" title={t('admin.memoryWorkflowActionCopy')}>
              <Button type="text" size="small" aria-label={t('admin.memoryWorkflowActionCopy')}
                icon={<WorkflowIcon name="copy" size={16} />} loading={copyingRowKey === rowKey}
                disabled={Boolean(copyingRowKey)} onClick={() => void handleCopy(row)} />
            </Tooltip>}
            {row._type === 'cloud' && <Tooltip placement="top" title={t('admin.memoryCloudDownload')}>
              <Button type="text" size="small" icon={<CloudDownloadOutlined />} aria-label={t('admin.memoryCloudDownload')}
                loading={downloading === row.id} disabled={!['download_required', 'local_missing'].includes(row.presence_status)}
                onClick={async () => {
                  if (downloading === row.id) return;
                  setDownloading(row.id);
                  try {
                    await downloadCloudResource('workflow', row.resource_id);
                    await loadList();
                    await cloud.reload();
                    message.success(t('admin.memoryCloudDownloadSuccess', { name: row.name }));
                  } catch { message.error(t('admin.memoryCloudDownloadFailed')); }
                  finally { setDownloading(undefined); }
                }} />
            </Tooltip>}
            {row._type === 'draft' && <Popconfirm title={t('admin.memoryWorkflowDeleteConfirm')}
                okText={t('admin.memoryWorkflowDeleteOk')} cancelText={t('common.cancel')}
                okButtonProps={{ danger: true }} onConfirm={() => void handleDelete(row.id)}>
                <Button type="text" size="small" danger icon={<DeleteOutlined />} />
              </Popconfirm>}
          </div>
        );
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
  ];

  const pagination = getLocalizedTablePagination(
    {
      current: currentPage,
      pageSize,
      total: filteredRows.length,
      showSizeChanger: false,
      hideOnSinglePage: false,
      showTotal: (itemTotal) => `${t('common.totalItems', { total: itemTotal })} · ${pageSize} ${t('common.itemsPerPageSuffix')}`,
      onChange: (nextPage) => setPage(nextPage),
    },
    t,
  );

  return (
    <div className="memory-skill-installed memory-workflow-installed">
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
            scroll={{ x: 656, y: '100%' }}
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
