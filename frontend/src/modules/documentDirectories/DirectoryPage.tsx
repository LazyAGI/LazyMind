import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ChangeEvent } from 'react';
import { ArrowLeftOutlined, CloudOutlined, FileTextOutlined, FolderOpenOutlined, FolderOutlined, InfoCircleOutlined, MoreOutlined, PlusOutlined, PushpinFilled, PushpinOutlined, SearchOutlined } from '@ant-design/icons';
import { Alert, Badge, Breadcrumb, Button, Collapse, Drawer, Dropdown, Empty, Form, Input, Modal, message, Pagination, Popover, Select, Space, Spin, Switch, Table, Tag, Tooltip, Typography, theme, type TableColumnsType } from 'antd';
import { useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import type { DocumentDirectoryCommand } from '@/api/generated/core-client';
import { directoryAction, getDirectory, listBatches, listDirectories, type DocumentDirectory, type DocumentDirectoryBatch, type DocumentDirectoryDetail, type DocumentDirectoryEntry } from './api';
import MailReader from './MailReader';
import LocalFileReader from './LocalFileReader';
import SourceTree from './SourceTree';
import ExcludedEntries from './ExcludedEntries';
import './DirectoryPage.css';
import { latestDirectoryBatches } from './batches';

const PAGE_SIZE = 5;

export default function DirectoryPage() {
  const { t } = useTranslation();
  const { directoryId } = useParams();
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const [viewMode, setViewMode] = useState<'all' | 'groups' | 'source'>('all');
  const [mailEntry, setMailEntry] = useState<DocumentDirectoryEntry>();
  const [localEntry, setLocalEntry] = useState<DocumentDirectoryEntry>();
  const [selectedGroup, setSelectedGroup] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [stateFilter, setStateFilter] = useState('all');
  const [selectedEntry, setSelectedEntry] = useState<DocumentDirectoryEntry>();
  const [page, setPage] = useState(1);
  const [directories, setDirectories] = useState<DocumentDirectory[]>([]);
  const [loadedDetail, setDetail] = useState<DocumentDirectoryDetail>();
  const [detailQuery, setDetailQuery] = useState('');
  const [batches, setBatches] = useState<DocumentDirectoryBatch[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [excludedOpen, setExcludedOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<DocumentDirectory>();
  const [editor, setEditor] = useState<'create' | 'update' | 'group' | 'reorganize'>();
  const [form] = Form.useForm();
  const pageContainer = useRef<HTMLDivElement>(null);
  const editingGroup = useRef<string>();
  const queryKey = JSON.stringify([directoryId, page, viewMode, selectedGroup, query, stateFilter]);
  const owner = useMemo(() => ({ key: queryKey }), [queryKey]);
  const activeOwner = useRef(owner);
  activeOwner.current = owner;
  const inFlight = useRef<{ owner: typeof owner; promise: Promise<void> }>();
  const detail = loadedDetail?.directory.id === directoryId ? loadedDetail : undefined;
  const refresh = useCallback(() => {
    if (activeOwner.current !== owner) return Promise.resolve();
    if (inFlight.current?.owner === owner) return inFlight.current.promise;
    const promise = (async () => {
      const [dirs, runs, view] = await Promise.all([listDirectories(), listBatches(), directoryId ? getDirectory(directoryId, { offset: (page - 1) * PAGE_SIZE, limit: PAGE_SIZE, groupId: viewMode === 'groups' && selectedGroup !== null ? selectedGroup : undefined, query, state: stateFilter }) : undefined]);
      if (activeOwner.current !== owner) return;
      setDirectories(dirs); setBatches(runs); setDetail(view); setDetailQuery(queryKey); setLoading(false);
    })().finally(() => { if (inFlight.current?.owner === owner) inFlight.current = undefined; });
    inFlight.current = { owner, promise };
    return promise;
  }, [directoryId, page, viewMode, selectedGroup, query, stateFilter, queryKey, owner]);
  useEffect(() => {
    if (pageContainer.current) pageContainer.current.scrollTop = 0;
    let disposed = false;
    let timer: number | undefined;
    if (!loadedDetail || loadedDetail.directory.id !== directoryId) setLoading(true);
    setSaving(false);
    const poll = async () => {
      try { await refresh(); }
      catch { if (!disposed && activeOwner.current === owner) setLoading(false); }
      finally { if (!disposed) timer = window.setTimeout(() => void poll(), 5000); }
    };
    void poll();
    return () => { disposed = true; window.clearTimeout(timer); };
  }, [refresh, owner]);
  useEffect(() => { setSelectedGroup(null); setPage(1); setQuery(''); setStateFilter('all'); setViewMode('all'); setSelectedEntry(undefined); setExcludedOpen(false); setDeleteTarget(undefined); setEditor(undefined); setMailEntry(undefined); setLocalEntry(undefined); }, [directoryId]);
  useEffect(() => {
    if (!directoryId) return;
    const touch = () => {
      if (!document.hidden) void directoryAction({ operation: 'touch', directory_id: directoryId }).catch(() => {});
    };
    touch();
    const timer = window.setInterval(touch, 60_000);
    document.addEventListener('visibilitychange', touch);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', touch);
    };
  }, [directoryId]);
  const act = async (command: DocumentDirectoryCommand) => {
    const operationOwner = owner;
    setSaving(true);
    try {
      const result = await directoryAction({ directory_id: directoryId, version: detail?.directory.version, ...command });
      if (activeOwner.current !== operationOwner) throw new Error('Directory view changed');
      const updatedEntry = result.entry;
      if (updatedEntry) setSelectedEntry(current => current?.id === updatedEntry.id ? updatedEntry : current);
      if (result.evicted) message.info(`${t('directories.evicted')}: ${result.evicted}`);
      if (command.operation === 'delete' && directoryId) navigate('/document-directories');
      else if (command.operation === 'create' && result.directory) navigate(`/document-directories/${result.directory.id}`);
      else {
        await refresh();
        if (activeOwner.current !== operationOwner) throw new Error('Directory view changed');
      }
      return result;
    } finally { if (activeOwner.current === operationOwner) setSaving(false); }
  };
  const run = (command: DocumentDirectoryCommand) => { void act(command).catch(() => {}); };
  const edit = (kind: typeof editor) => { editingGroup.current = undefined; form.resetFields(); if (kind === 'update') form.setFieldsValue(detail?.directory); setEditor(kind); };
  const blocked = saving || detail?.organizing;
  const folderBatch = detail?.folder ? batches.find(b => b.id === detail.folder?.batch_id) : undefined;
  const folderProgress = (batch?: DocumentDirectoryBatch) => {
    const result = batch?.result;
    if (!result?.collection_state) return null;
    const failed = ['failed', 'canceled'].includes(batch?.status || '') ? batch?.status : undefined;
    const collection = result.collection_state === 'complete' ? 'complete' : failed || result.collection_state;
    const classification = result.collection_state === 'complete' && failed ? failed : result.classification_state || 'pending';
    return <div>{t('directories.folderProgress', { collection: t(`directories.folderState_${collection}`), collected: result.collected || 0, classification: t(`directories.folderState_${classification}`), processed: result.processed || 0 })}
      {result.grouped !== undefined && <div>{t('directories.folderClassificationCounts', { grouped: result.grouped, unclassified: result.unclassified })}</div>}</div>;
  };
  const d = detail?.directory;
  const entries = detail?.entries || [];
  const groupName = (id: string): string => {
    const group = detail?.groups.find(g => g.id === id);
    return group ? `${group.parent_id ? groupName(group.parent_id) + ' / ' : ''}${group.name}` : t('directories.unclassified');
  };
  const groupOptions = [{ value: '', label: t('directories.unclassified') }, ...(detail?.groups || []).map(g => ({ value: g.id, label: groupName(g.id) }))];
  const ungroupedCount = detail?.group_counts?.[''] ?? entries.filter(e => !e.group_id).length;
  const defaultGroup = detail?.groups.find(g => (detail?.group_counts?.[g.id] ?? entries.filter(e => e.group_id === g.id).length) > 0)?.id || '';
  const activeGroup = selectedGroup !== null && (selectedGroup ? detail?.groups.some(g => g.id === selectedGroup) : ungroupedCount > 0) ? selectedGroup : defaultGroup;
  useEffect(() => {
    if (viewMode === 'groups' && detail && selectedGroup !== activeGroup) { setSelectedGroup(activeGroup); setPage(1); }
  }, [viewMode, detail, selectedGroup, activeGroup]);
  const pendingCount = entries.filter(e => !e.confirmed).length;
  const documentCount = detail?.group_counts ? Object.values(detail.group_counts).reduce((sum, count) => sum + count, 0) : entries.length;
  const entryTitle = (e: DocumentDirectoryEntry) => e.title || (e.source === 'local' ? e.resource_id.split(/[\\/]/).pop() : '') || e.resource_id;
  const sourceLabel = (source: string) => t(`directories.source_${source}`, { defaultValue: source });
  const entryURL = (e: DocumentDirectoryEntry) => {
    const legacy = e.source === 'notion' && typeof e.locator?.id === 'string' && /^https?:\/\/(?:[a-z0-9-]+\.)*notion\.(?:so|com|site)\//i.test(e.locator.id) ? e.locator.id : '';
    const url = String(e.locator?.url || e.locator?.web_url || legacy);
    return /^https?:\/\//.test(url) ? url : undefined;
  };
  const inaccessible = (e: DocumentDirectoryEntry) => ['unavailable', 'denied', 'deleted'].includes(e.access_state);
  // Coverage is chronological request history, not a current connection-health check.
  // A later result for the same request replaces its earlier failure.
  const latestSourceRequests = new Map((d?.coverage || [])
    .filter(c => ['local', 'notion', 'feishu', 'mail', 'google_drive', 'agent_conversations'].includes(c.source) && ['search', 'read', 'metadata'].includes(c.operation) && ['success', 'zero', 'partial', 'denied', 'failed', 'error', 'unavailable'].includes(c.status))
    // Legacy records may include failed shell commands or omit the document locator.
    // Without a request target, neither a source failure nor its recovery is reliable.
    .filter(c => ['query', 'keyword', 'keywords', 'path', 'root', 'target', 'pattern', 'node_id'].some(key => c.scope?.[key]))
    .map(c => [JSON.stringify([c.source, c.operation, c.query || '', c.scope || {}]), c] as const));
  const sourceNotices = [...new Set([...latestSourceRequests.values()]
    .filter(c => ['denied', 'failed', 'error', 'unavailable'].includes(c.status))
    .map(c => t(`directories.sourceIssue_${c.operation}_${c.status === 'denied' ? 'denied' : 'failed'}`, { source: sourceLabel(c.source) })))];
  const inaccessibleCount = entries.filter(inaccessible).length;
  const entryTotal = detail?.total ?? 0;
  const matchesQuery = detailQuery === queryKey;
  const currentPage = matchesQuery ? Math.min(page, Math.max(1, Math.ceil(entryTotal / PAGE_SIZE))) : page;
  const pageReady = matchesQuery && page === currentPage &&
    (viewMode !== 'groups' || selectedGroup !== null && selectedGroup === activeGroup);
  useEffect(() => { if (page !== currentPage) setPage(currentPage); }, [page, currentPage]);
  const selected = detail && selectedEntry && (entries.find(e => e.id === selectedEntry.id) ?? selectedEntry);
  const readers = <>
    <LocalFileReader directoryId={directoryId} entry={localEntry} onClose={() => setLocalEntry(undefined)} />
    <MailReader directoryId={directoryId} entry={mailEntry} onClose={() => setMailEntry(undefined)} />
  </>;
  const readState = (e: DocumentDirectoryEntry) => t(`directories.read_${e.read_state}`, { defaultValue: e.read_state });
  const columns: TableColumnsType<DocumentDirectoryEntry> = [
    { title: t('directories.document'), key: 'title', render: (_: unknown, e: DocumentDirectoryEntry) => <div className="directory-document">
      <span className="directory-item-icon">{e.source === 'feishu' || e.source === 'google_drive' ? <CloudOutlined /> : <FileTextOutlined />}</span>
      <div className="directory-document-text">
        <div className="directory-document-title"><Tooltip title={entryTitle(e)}>{e.source === 'local' ? <button type="button" className="directory-title-button" onClick={() => setLocalEntry(e)}>{entryTitle(e)}</button> : e.source === 'mail' ? <button type="button" className="directory-title-button" onClick={() => setMailEntry(e)}>{entryTitle(e)}</button> : entryURL(e) ? <a href={entryURL(e)} target="_blank" rel="noreferrer">{entryTitle(e)}</a> : <button type="button" className="directory-title-button" onClick={() => setSelectedEntry(e)}>{entryTitle(e)}</button>}</Tooltip>{e.pinned && <Tooltip title={t('directories.entryPin')}><PushpinFilled /></Tooltip>}</div>
        <div className="directory-document-summary">
          <Typography.Text type="secondary" className="directory-document-meta">{sourceLabel(e.source)} · {groupName(e.group_id)}</Typography.Text>
          {(!e.confirmed || inaccessible(e) || e.content_state === 'changed') && <span className="directory-entry-states">
            {!e.confirmed && <Tooltip title={t('directories.confirmationHelp')}><Tag bordered={false} color="orange">{t(detail?.folder ? 'directories.classificationUnconfirmed' : 'directories.unconfirmed')}</Tag></Tooltip>}
            {inaccessible(e) && <Tag bordered={false} color="red">{t(`directories.access_${e.access_state}`)}</Tag>}
            {e.content_state === 'changed' && <Tag bordered={false} color="blue">{t('directories.content_changed')}</Tag>}
          </span>}
        </div>
      </div>
    </div> },
    { title: t('directories.actions'), key: 'actions', width: 94, align: 'center', render: (_, e) => <Button type="link" size="small" onClick={() => setSelectedEntry(e)}>{t('directories.details')}</Button> },
  ];
  const visibleBatches = latestDirectoryBatches(batches, directoryId);
  const unreadDirectoryIds = new Set(visibleBatches.filter(b => ['succeeded', 'failed', 'canceled'].includes(b.status)).map(b => b.directory_id));
  const statusBatches = visibleBatches.filter(b => ['pending', 'running', 'canceled'].includes(b.status));
  const failedBatches = visibleBatches.filter(b => b.status === 'failed');
  const acknowledging = useRef(new Set<string>());
  useEffect(() => {
    if (loading || document.hidden || directoryId && !detail) return;
    for (const batch of latestDirectoryBatches(batches, directoryId)) {
      if (batch.status !== 'succeeded' || !directoryId && batch.directory_id || acknowledging.current.has(batch.id)) continue;
      acknowledging.current.add(batch.id);
      void directoryAction({ operation: 'acknowledge', batch_id: batch.id }).then(() => {
        setBatches(previous => previous.map(b => b.id === batch.id ? { ...b, acknowledged: true } : b));
      }).catch(() => {}).finally(() => acknowledging.current.delete(batch.id));
    }
  }, [batches, directoryId, detail, loading]);
  const directoryStyles = {
    '--directory-panel': token.colorBgContainer,
    '--directory-border': token.colorBorderSecondary,
    '--directory-muted': token.colorTextSecondary,
    '--directory-hover': token.controlItemBgHover,
    '--directory-selected': token.controlItemBgActive,
    '--directory-primary': token.colorPrimary,
    '--directory-radius': `${token.borderRadiusLG}px`,
  } as CSSProperties;
  const pinButton = (item: DocumentDirectory, disabled: boolean | undefined, onClick: () => void) => {
    const label = t(item.pinned ? 'directories.directoryUnpin' : 'directories.directoryPin');
    return <Tooltip title={label}>
      <Button type="text" className="directory-pin-button" aria-label={label} aria-pressed={item.pinned}
        icon={item.pinned ? <PushpinFilled /> : <PushpinOutlined />} disabled={disabled} onClick={onClick} />
    </Tooltip>;
  };
  const overviewColumns: TableColumnsType<DocumentDirectory> = [
    { title: t('directories.nameAndIntent'), key: 'name', render: (_, item) => {
      const unread = unreadDirectoryIds.has(item.id);
      return <div className="directory-document">
        <span className="directory-item-icon"><FolderOutlined /></span>
        <div className="directory-document-text">
          <div className="directory-document-title">
            <Tooltip title={item.name}>
              <button type="button" className="directory-title-button" aria-description={unread ? t('directories.unreadResult') : undefined}
                onClick={() => navigate(`/document-directories/${item.id}`)}>{item.name}</button>
            </Tooltip>
            {unread && <Badge status="success" aria-hidden="true" />}
          </div>
          <Typography.Text className="directory-document-meta" type="secondary" ellipsis={{ tooltip: item.intent }}>{item.intent}</Typography.Text>
          <time className="directory-inline-date" dateTime={item.last_used_at}>{new Date(item.last_used_at).toLocaleString()}</time>
        </div>
      </div>;
    } },
    { title: t('directories.lastUsed'), key: 'lastUsed', width: 152, className: 'directory-date-column', render: (_, item) =>
      <time dateTime={item.last_used_at}>{new Date(item.last_used_at).toLocaleString()}</time> },
    { title: t('directories.actions'), key: 'actions', width: 104, align: 'center', render: (_, item) => <Space size={0}>
      {pinButton(item, saving, () => run({ operation: 'update', directory_id: item.id, version: item.version, pinned: !item.pinned }))}
      <Dropdown trigger={['click']} menu={{ items: [{ key: 'delete', label: t('directories.delete'), danger: true,
        disabled: saving || batches.some(b => b.directory_id === item.id && ['pending', 'running'].includes(b.status)),
        onClick: () => setDeleteTarget(item) }] }}>
        <Button type="text" aria-label={`${item.name} ${t('directories.more')}`} icon={<MoreOutlined />} />
      </Dropdown>
    </Space> },
  ];
  return <div ref={pageContainer} className="directory-page" style={directoryStyles}>
    <div className="directory-page-inner">
      <header className={directoryId ? 'directory-detail-header' : undefined}>
        <div className="directory-header">
          {directoryId ? <Breadcrumb className="directory-breadcrumb" items={[
            { title: <Button type="text" icon={<ArrowLeftOutlined />} aria-label={t('directories.back')}
              onClick={() => navigate('/document-directories')}>{t('directories.title')}</Button> },
            ...(d ? [{ title: <Typography.Title level={1} title={d.name} aria-current="page">{d.name}</Typography.Title> }] : []),
          ]} /> : <Typography.Title level={1}>{t('directories.title')}</Typography.Title>}
          <Space wrap className="directory-primary-actions">{d ? <>
          {pinButton(d, blocked, () => run({ operation: 'update', pinned: !d.pinned }))}
          <Space size={8}><span>{t('directories.autoAdd')}</span><Switch size="small" aria-label={t('directories.autoAdd')}
            disabled={blocked} checked={d.auto_add} onChange={(auto_add: boolean) => run({ operation: 'update', auto_add })} /></Space>
          <Dropdown trigger={['click']} menu={{ items: [
            { key: 'edit', label: t('directories.edit'), disabled: blocked, onClick: () => edit('update') },
            { key: 'reorganize', label: t('directories.reorganize'), disabled: blocked, onClick: () => edit('reorganize') },
            { key: 'excluded', label: t('directories.excluded'), onClick: () => setExcludedOpen(true) },
            { type: 'divider' },
            { key: 'delete', label: t('directories.delete'), danger: true, disabled: blocked, onClick: () => setDeleteTarget(d) },
          ] }}><Button type="text" aria-label={t('directories.more')} icon={<MoreOutlined />} /></Dropdown>
          </> : !directoryId && <>
            <Popover trigger="click" placement="bottomRight" title={t('directories.retentionRules')}
              content={<ul className="directory-retention-rules">
                <li>{t('directories.retentionLimit')}</li><li>{t('directories.retentionExpiry')}</li><li>{t('directories.retentionOriginals')}</li>
              </ul>}>
              <Button type="text" icon={<InfoCircleOutlined />}>{t('directories.retentionRules')}</Button>
            </Popover>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => edit('create')}>{t('directories.create')}</Button>
          </>}</Space>
        </div>
        {d ? <>
          <Typography.Paragraph type="secondary" className="directory-intent">{d.intent}</Typography.Paragraph>
          <Space wrap>
            <Tag bordered={false} color={d.pinned ? 'blue' : undefined}>{t(d.pinned ? 'directories.pinned' : 'directories.temporaryStatus')}</Tag>
            <Tag bordered={false}>{d.organization}</Tag>
            {detail?.folder && <Tag bordered={false}>{t('directories.folderMembership')}</Tag>}
          </Space>
        </> : !directoryId && <Typography.Paragraph type="secondary" className="directory-intent">{t('directories.intro')}</Typography.Paragraph>}
      </header>
      {failedBatches.length > 0 && <Collapse size="small" items={[{
        key: 'failed',
        label: <Typography.Text type="danger">{t('directories.failedResults', { count: failedBatches.length })}</Typography.Text>,
        children: <div className="directory-failed-batches">{failedBatches.map(batch => <div key={batch.id} className="directory-failed-batch">
          <div className="directory-failed-batch-copy">
            <Typography.Text strong>{directories.find(x => x.id === batch.directory_id)?.name || String(batch.result?.directory_name || '') || t('directories.automaticOrganization')}</Typography.Text>
            <Typography.Text type="secondary"><time dateTime={batch.created_at}>{t('directories.taskCreatedAt', { time: new Date(batch.created_at).toLocaleString() })}</time></Typography.Text>
          </div>
          <Space wrap>
            <Button size="small" disabled={saving} onClick={() => run({ operation: 'retry', batch_id: batch.id })}>{t('directories.retry')}</Button>
            <Button size="small" disabled={saving} onClick={() => run({ operation: 'acknowledge', batch_id: batch.id })}>{t('directories.dismissResult')}</Button>
          </Space>
          {folderProgress(batch)}
        </div>)}</div>,
      }]} />}
      {statusBatches.map(batch => <Alert key={batch.id} showIcon type="info"
        message={<Space>{!directoryId && <span>{directories.find(x => x.id === batch.directory_id)?.name || String(batch.result?.directory_name || '')}</span>}{t(batch.status === 'canceled' ? 'directories.folderState_canceled' : batch.status === 'pending' ? 'directories.organizePending' : 'directories.organizing')}{folderProgress(batch)}</Space>}
        action={<Space><Button size="small" disabled={saving} onClick={() => run({ operation: batch.status === 'canceled' ? 'retry' : 'cancel', batch_id: batch.id })}>{t(batch.status === 'canceled' ? 'directories.retry' : 'directories.cancel')}</Button>{batch.status === 'canceled' && <Button size="small" disabled={saving} onClick={() => run({ operation: 'acknowledge', batch_id: batch.id })}>{t('directories.dismissResult')}</Button>}</Space>} />)}
      {folderBatch?.status === 'succeeded' && <Alert type="success" message={folderProgress(folderBatch)} />}
      {loading ? <div className="directory-loading"><Spin /></div> : !directoryId ? <>
        {[true, false].map(pinned => {
          const items = directories.filter(item => item.pinned === pinned);
          const heading = `directory-list-${pinned ? 'pinned' : 'temporary'}`;
          return <section className="directory-list-section" aria-labelledby={heading} key={heading}>
            <div className="directory-list-heading">
              <h2 id={heading}>{t(pinned ? 'directories.pinned' : 'directories.temporary')}</h2>
              <Typography.Text type="secondary">{t('directories.directoryCount', { count: items.length })}</Typography.Text>
            </div>
            <div className="directory-table-panel directory-overview-table">
              {items.length > 0 ? <Table size="middle" rowKey="id" tableLayout="fixed" columns={overviewColumns} dataSource={items} pagination={false} /> :
                <Typography.Paragraph type="secondary" className="directory-empty-section">{t(pinned ? 'directories.noPinned' : 'directories.noDirectories')}</Typography.Paragraph>}
            </div>
          </section>;
        })}
      </> : detail && d ? <>
        {detail.organizing && !statusBatches.some(b => ['pending', 'running'].includes(b.status)) && <Alert type="info" showIcon message={t('directories.organizing')} />}
        {(sourceNotices.length > 0 || inaccessibleCount > 0) && <Alert type="warning" showIcon message={<>
          {sourceNotices.map(notice => <div key={notice}>{notice}</div>)}
          {inaccessibleCount > 0 && <div>{t('directories.inaccessibleDocuments', { count: inaccessibleCount })}</div>}
        </>} />}
        <div className="directory-workspace">
          <nav className="directory-groups" aria-label={t('directories.group')}>
            <button type="button" className="directory-group-button" aria-pressed={viewMode === 'all'}
              onClick={() => { setViewMode('all'); setPage(1); }}>
              <FileTextOutlined /><span>{t('directories.allDocuments')}</span><span className="directory-group-count">{documentCount}</span>
            </button>
            {detail.folder && <button type="button" className="directory-group-button" aria-pressed={viewMode === 'source'}
              onClick={() => { setViewMode('source'); setPage(1); }}>
              <FolderOpenOutlined /><span>{t('directories.sourceTree')}</span>
            </button>}
            <div className="directory-section-heading">
              <Typography.Text type="secondary">{t('directories.group')}</Typography.Text>
              <Tooltip title={t('directories.addGroup')}>
                <Button type="text" size="small" icon={<PlusOutlined />} aria-label={t('directories.addGroup')} disabled={blocked} onClick={() => edit('group')} />
              </Tooltip>
            </div>
            {groupOptions.filter(g => g.value !== '' || ungroupedCount > 0).map(g => {
              const count = detail?.group_counts?.[g.value] ?? entries.filter(e => e.group_id === g.value).length;
              const group = detail.groups.find(item => item.id === g.value);
              return <div key={g.value} className="directory-group-row">
                <button type="button" className="directory-group-button" aria-pressed={viewMode === 'groups' && activeGroup === g.value}
                  onClick={() => { setViewMode('groups'); setSelectedGroup(g.value); setPage(1); }}>
                  <FolderOutlined /><span>{g.label}</span><span className="directory-group-count">{count}</span>
                </button>
                {group && <Dropdown trigger={['click']} menu={{ items: [
                  { key: 'edit', label: t('directories.editGroup'), disabled: blocked,
                    onClick: () => { edit('group'); editingGroup.current = group.id; form.setFieldsValue(group); } },
                  { key: 'delete', label: t('directories.removeGroup'), danger: true,
                    disabled: blocked || count > 0 || detail.groups.some(item => item.parent_id === group.id),
                    onClick: () => run({ operation: 'delete_group', group_id: group.id }) },
                ] }}><Button type="text" size="small" aria-label={`${g.label} ${t('directories.more')}`} icon={<MoreOutlined />} /></Dropdown>}
              </div>;
            })}
          </nav>
          {viewMode === 'source' && directoryId ? <SourceTree directoryId={directoryId} /> :
            <section className="directory-table-panel directory-documents" aria-labelledby="directory-documents-heading">
              <div className="directory-document-toolbar">
                <div className="directory-document-toolbar-heading">
                  <h2 id="directory-documents-heading">{viewMode === 'groups' ? groupName(activeGroup) : t('directories.allDocuments')}</h2>
                  {matchesQuery && <Typography.Text type="secondary">
                    {t('directories.documentCount', { count: entryTotal })}
                    {pendingCount > 0 && ` · ${t(detail.total !== undefined ? 'directories.pagePendingCount' : 'directories.pendingCount', { count: pendingCount })}`}
                  </Typography.Text>}
                </div>
                <div className="directory-document-filters">
                  <Input allowClear prefix={<SearchOutlined />} aria-label={t('directories.search')} placeholder={t('directories.search')} value={query} onChange={(e: ChangeEvent<HTMLInputElement>) => { setQuery(e.target.value); setPage(1); }} />
                  <Select aria-label={t('directories.filterState')} value={stateFilter} onChange={(value: string) => { setStateFilter(value); setPage(1); }}
                    options={['all', 'unconfirmed', 'unread', 'inaccessible'].map(value => ({ value, label: t(`directories.filter_${value}`) }))} />
                </div>
              </div>
              <Table size="middle" tableLayout="fixed" rowKey="id" loading={!pageReady} dataSource={pageReady ? entries : []} columns={columns} pagination={false}
                locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('directories.noMatchingDocuments')} /> }} />
              <Pagination className="directory-pagination" current={currentPage} onChange={setPage} total={entryTotal} pageSize={PAGE_SIZE} showSizeChanger={false} hideOnSinglePage={false} />
            </section>}
        </div>

      </> : <Empty />}
    </div>
    <Modal open={!!deleteTarget} title={t('directories.deleteConfirm')} okText={t('directories.delete')} cancelText={t('directories.cancel')}
      okButtonProps={{ danger: true }} confirmLoading={saving} onCancel={() => setDeleteTarget(undefined)}
      onOk={() => { if (deleteTarget) void act({ operation: 'delete', directory_id: deleteTarget.id, version: deleteTarget.version }).then(() => setDeleteTarget(undefined)).catch(() => {}); }}>
      <Typography.Text>{deleteTarget?.name}</Typography.Text>
    </Modal>
    <Drawer open={excludedOpen} title={t('directories.excluded')} width="min(460px, 100vw)" onClose={() => setExcludedOpen(false)}>
      {excludedOpen && d && <ExcludedEntries key={d.id} directoryId={d.id} version={d.version} blocked={blocked} restore={id => run({ operation: 'restore', entry_id: id })} />}
    </Drawer>
    <Drawer open={!!selected} title={t('directories.entryDetails')} width="min(460px, 100vw)" rootStyle={directoryStyles}
      push={false} onClose={() => setSelectedEntry(undefined)} footer={selected && <Button danger type="text" disabled={blocked}
        onClick={() => { void act({ operation: 'exclude', entry_id: selected.id }).then(() => setSelectedEntry(undefined)).catch(() => {}); }}>{t('directories.exclude')}</Button>}>
      {selected && <>
        <div className="directory-entry-detail">
          <div className="directory-document">
            <span className="directory-item-icon"><FileTextOutlined /></span>
            <div className="directory-document-text">
              <Typography.Title level={4}>{entryTitle(selected)}</Typography.Title>
              <Space wrap size={[0, 4]}>
                <Tag bordered={false}>{sourceLabel(selected.source)}</Tag>
                <Tag bordered={false} color={selected.confirmed ? 'green' : 'orange'}>{t(detail?.folder ? 'directories.folderMembership' : selected.confirmed ? 'directories.confirmed' : 'directories.unconfirmed')}</Tag>
              </Space>
            </div>
          </div>
          <Space wrap>
            {selected.source === 'local' && <Button type="primary" icon={<FileTextOutlined />} onClick={() => setLocalEntry(selected)}>{t('directories.previewFile')}</Button>}
            {selected.source === 'mail' && <Button type="primary" onClick={() => setMailEntry(selected)}>{t('directories.viewMail')}</Button>}
            {entryURL(selected) && <Button type="primary" href={entryURL(selected)} target="_blank" rel="noreferrer">{t('directories.openOriginal')}</Button>}
          </Space>
          <section className="directory-entry-section">
            <Typography.Title level={5}>{t('directories.evidence')}</Typography.Title>
            <Typography.Paragraph>{String(selected.evidence?.reason || t('directories.noEvidence'))}</Typography.Paragraph>
            <Typography.Paragraph type="secondary">{t(detail?.folder ? 'directories.folderContract' : selected.confirmed ? 'directories.confirmedHelp' : 'directories.confirmationHelp')}</Typography.Paragraph>
          </section>
          <section className="directory-entry-section">
            <Typography.Title level={5}>{t('directories.entryGroup')}</Typography.Title>
            <div className="directory-entry-group">
              <Select aria-label={t('directories.entryGroup')} disabled={blocked} value={selected.group_id} options={groupOptions}
                onChange={(group_id: string) => run({ operation: 'entry', entry_id: selected.id, group_id })} />
              <Button className="directory-pin-button" aria-label={t(selected.pinned ? 'directories.entryUnpin' : 'directories.entryPin')}
                aria-pressed={selected.pinned} icon={selected.pinned ? <PushpinFilled /> : <PushpinOutlined />}
                disabled={blocked} onClick={() => run({ operation: 'entry_pin', entry_id: selected.id, pinned: !selected.pinned })}>{t(selected.pinned ? 'directories.entryUnpin' : 'directories.entryPin')}</Button>
            </div>
            <Typography.Text type="secondary">{t('directories.entryPinHelp')}</Typography.Text>
          </section>
          <Collapse ghost className="directory-entry-source" items={[{
            key: 'source', label: <Typography.Text strong>{t('directories.entrySourceDetails')}</Typography.Text>,
            children: <div className="directory-entry-section">
              <Typography.Text strong>{t('directories.aiReadState')}</Typography.Text>
              <div><Tag>{readState(selected)}</Tag></div>
              <Typography.Paragraph type="secondary">{t('directories.aiReadStateHelp')}</Typography.Paragraph>
              <Space wrap size={[0, 4]}><Tag>{t(`directories.access_${selected.access_state}`, { defaultValue: selected.access_state })}</Tag><Tag>{t(`directories.content_${selected.content_state || 'unknown'}`)}</Tag></Space>
              <Typography.Text strong>{t('directories.location')}</Typography.Text>
              <Typography.Paragraph copyable={{ text: JSON.stringify(selected.locator || { resource_id: selected.resource_id }) }}>{selected.resource_id}</Typography.Paragraph>
              <Typography.Text type="secondary">{selected.namespace}</Typography.Text>
            </div>,
          }]} />
        </div>
        {readers}
      </>}
    </Drawer>
    {!selected && readers}
    <Modal open={!!editor} title={editor === 'group' ? t(editingGroup.current ? 'directories.editGroup' : 'directories.addGroup') : t(`directories.${editor || 'create'}`)} onCancel={() => setEditor(undefined)} confirmLoading={saving} onOk={() => { void form.validateFields().then(values => act({ operation: editor!, group_id: editor === 'group' ? editingGroup.current : undefined, parent_id: editor === 'group' ? detail?.groups.find(g => g.id === editingGroup.current)?.parent_id || '' : undefined, ...values })).then(() => setEditor(undefined)).catch(() => {}); }}>
      <Form form={form} layout={editor === 'create' || editor === 'update' ? 'horizontal' : 'vertical'}
        labelCol={{ xs: { span: 24 }, sm: { span: 5 } }} wrapperCol={{ xs: { span: 24 }, sm: { span: 19 } }} labelAlign="left" colon={false}>
        {editor !== 'reorganize' && <Form.Item name="name" label={t(editor === 'group' ? 'directories.name' : 'directories.directoryName')} rules={[{ required: true, whitespace: true, max: 255 }]}><Input /></Form.Item>}
        {(editor === 'create' || editor === 'update') && <Form.Item name="intent" label={t('directories.intent')} rules={[{ required: true, whitespace: true, max: 4000 }]}>
          <Input.TextArea rows={3} placeholder={t('directories.intentPlaceholder')} />
        </Form.Item>}
        {editor === 'reorganize' && <><Alert message={t('directories.newView')} /><Form.Item name="organization" label={t('directories.organization')} rules={[{ required: true, whitespace: true }]}><Input /></Form.Item></>}
      </Form>
    </Modal>
  </div>;
}
