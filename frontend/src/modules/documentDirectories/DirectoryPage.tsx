import { useCallback, useEffect, useRef, useState, type CSSProperties, type ChangeEvent } from 'react';
import { ArrowLeftOutlined, FileTextOutlined, FolderOutlined, MoreOutlined, PushpinFilled, PushpinOutlined, SearchOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Collapse, Drawer, Dropdown, Empty, Form, Input, Modal, message, Pagination, Popconfirm, Select, Space, Spin, Switch, Table, Tabs, Tag, Tooltip, Typography, theme } from 'antd';
import { useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import type { DocumentDirectoryCommand } from '@/api/generated/core-client';
import { directoryAction, getDirectory, listBatches, listDirectories, type DocumentDirectory, type DocumentDirectoryBatch, type DocumentDirectoryDetail, type DocumentDirectoryEntry } from './api';
import MailReader from './MailReader';
import LocalFileReader from './LocalFileReader';
import SourceTree from './SourceTree';
import ExcludedEntries from './ExcludedEntries';
import './DirectoryPage.css';

const PAGE_SIZE = 5;

export default function DirectoryPage() {
  const { t } = useTranslation();
  const { directoryId } = useParams();
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const [viewMode, setViewMode] = useState('groups');
  const [mailEntry, setMailEntry] = useState<DocumentDirectoryEntry>();
  const [localEntry, setLocalEntry] = useState<DocumentDirectoryEntry>();
  const [selectedGroup, setSelectedGroup] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [stateFilter, setStateFilter] = useState('all');
  const [selectedEntry, setSelectedEntry] = useState<string>();
  const [page, setPage] = useState(1);
  const [directories, setDirectories] = useState<DocumentDirectory[]>([]);
  const [detail, setDetail] = useState<DocumentDirectoryDetail>();
  const [batches, setBatches] = useState<DocumentDirectoryBatch[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [editor, setEditor] = useState<'create' | 'update' | 'group' | 'reorganize'>();
  const [form] = Form.useForm();
  const requestSequence = useRef(0);
  const pageContainer = useRef<HTMLDivElement>(null);
  const editingGroup = useRef<string>();
  const refresh = useCallback(async () => {
    const sequence = ++requestSequence.current;
    const [dirs, runs, view] = await Promise.all([listDirectories(), listBatches(), directoryId ? getDirectory(directoryId, { offset: (page - 1) * PAGE_SIZE, limit: PAGE_SIZE, groupId: viewMode === 'groups' && selectedGroup !== null ? selectedGroup : undefined, query, state: stateFilter }) : undefined]);
    if (sequence !== requestSequence.current) return;
    setDirectories(dirs); setBatches(runs); setDetail(view); setLoading(false);
  }, [directoryId, page, viewMode, selectedGroup, query, stateFilter]);
  useEffect(() => {
    if (pageContainer.current) pageContainer.current.scrollTop = 0;
    void refresh().catch(() => setLoading(false));
    const timer = window.setInterval(() => { void refresh().catch(() => {}); }, 5000);
    return () => { ++requestSequence.current; window.clearInterval(timer); };
  }, [refresh]);
  useEffect(() => { setSelectedGroup(null); setPage(1); setQuery(''); setStateFilter('all'); setViewMode('groups'); setSelectedEntry(undefined); }, [directoryId]);
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
    setSaving(true);
    try {
      const result = await directoryAction({ directory_id: directoryId, version: detail?.directory.version, ...command });
      if (result.evicted) message.info(`${t('directories.evicted')}: ${result.evicted}`);
      if (command.operation === 'delete') navigate('/document-directories');
      else if (command.operation === 'create' && result.directory) navigate(`/document-directories/${result.directory.id}`);
      else await refresh();
      return result;
    } finally { setSaving(false); }
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
  const entries = (detail?.entries || []).filter(e => !e.excluded);
  const groupName = (id: string): string => {
    const group = detail?.groups.find(g => g.id === id);
    return group ? `${group.parent_id ? groupName(group.parent_id) + ' / ' : ''}${group.name}` : t('directories.unclassified');
  };
  const groupOptions = [{ value: '', label: t('directories.unclassified') }, ...(detail?.groups || []).map(g => ({ value: g.id, label: groupName(g.id) }))];
  const ungroupedCount = detail?.group_counts?.[''] ?? entries.filter(e => !e.group_id).length;
  const defaultGroup = detail?.groups.find(g => (detail?.group_counts?.[g.id] ?? entries.filter(e => e.group_id === g.id).length) > 0)?.id || '';
  const activeGroup = selectedGroup !== null && (selectedGroup ? detail?.groups.some(g => g.id === selectedGroup) : ungroupedCount > 0) ? selectedGroup : defaultGroup;
  useEffect(() => {
    if (detail && selectedGroup !== activeGroup) { setSelectedGroup(activeGroup); setPage(1); }
  }, [detail, selectedGroup, activeGroup]);
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
  const filteredEntries = entries.filter(e =>
    (viewMode === 'all' || e.group_id === activeGroup) &&
    (stateFilter === 'all' || (stateFilter === 'unconfirmed' && !e.confirmed) ||
      (stateFilter === 'unread' && e.read_state === 'unread') || (stateFilter === 'inaccessible' && inaccessible(e))) &&
    `${e.title} ${e.resource_id} ${sourceLabel(e.source)}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));
  const entryTotal = detail?.total ?? filteredEntries.length;
  const currentPage = Math.min(page, Math.max(1, Math.ceil(entryTotal / PAGE_SIZE)));
  useEffect(() => { if (page !== currentPage) setPage(currentPage); }, [page, currentPage]);
  const selected = detail?.entries.find(e => e.id === selectedEntry);
  const readState = (e: DocumentDirectoryEntry) => t(`directories.read_${e.read_state}`, { defaultValue: e.read_state });
  const columns = [
    { title: t('directories.document'), key: 'title', render: (_: unknown, e: DocumentDirectoryEntry) => <div className="directory-document">
      <span className={`directory-source-icon directory-source-${e.source}`}><FileTextOutlined /></span>
      <div className="directory-document-text">
        <div className="directory-document-title"><Tooltip title={e.title || e.resource_id}>{e.source === 'local' ? <button className="directory-title-button" onClick={() => setLocalEntry(e)}>{entryTitle(e)}</button> : e.source === 'mail' ? <button className="directory-title-button" onClick={() => setMailEntry(e)}>{entryTitle(e)}</button> : entryURL(e) ? <a href={entryURL(e)} target="_blank" rel="noreferrer">{entryTitle(e)}</a> : <button className="directory-title-button" onClick={() => setSelectedEntry(e.id)}>{entryTitle(e)}</button>}</Tooltip>{e.pinned && <Tooltip title={t('directories.entryPin')}><PushpinFilled /></Tooltip>}</div>
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
    { title: t('directories.actions'), key: 'actions', width: 80, render: (_: unknown, e: DocumentDirectoryEntry) => <Button type="text" onClick={() => setSelectedEntry(e.id)}>{t('directories.details')}</Button> },
  ];
  // Keep only each directory's latest status; completed history needs no acknowledgement.
  const latestBatches = new Map<string, DocumentDirectoryBatch>();
  [...batches].sort((a, b) => b.created_at.localeCompare(a.created_at)).forEach(batch => {
    if (batches.some(child => child.parent_id === batch.id)) return;
    if (directoryId && batch.directory_id !== directoryId) return;
    const key = batch.directory_id || batch.id;
    if (!latestBatches.has(key)) latestBatches.set(key, batch);
  });
  const statusBatches = [...latestBatches.values()].filter(b => !b.acknowledged && !batches.find(parent => parent.id === b.parent_id)?.acknowledged && ['pending', 'running', 'failed', 'canceled'].includes(b.status));
  return <div ref={pageContainer} className="directory-page" style={{ '--directory-bg': token.colorBgLayout, '--directory-panel': token.colorBgContainer, '--directory-border': token.colorBorderSecondary, '--directory-muted': token.colorTextSecondary, '--directory-hover': token.controlItemBgHover, '--directory-selected': token.controlItemBgActive } as CSSProperties}>
    <div className="directory-page-inner">
      <header className="directory-header">
        <div className="directory-heading">
          {directoryId && <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/document-directories')}>{t('directories.back')}</Button>}
          <Typography.Title level={3}>{d?.name || t('directories.title')}</Typography.Title>
          <Typography.Paragraph type="secondary">{d?.intent || t('directories.intro')}</Typography.Paragraph>
          {detail?.folder && <Tag>{t('directories.folderMembership')}</Tag>}
        </div>
        <Space wrap className="directory-primary-actions">{d ? <>
          <Button disabled={blocked} onClick={() => run({ operation: 'organize' })}>{t('directories.organize')}</Button>
          <Dropdown trigger={['click']} menu={{ items: [
            { key: 'edit', label: t('directories.edit'), disabled: blocked, onClick: () => edit('update') },
            { key: 'reorganize', label: t('directories.reorganize'), disabled: blocked, onClick: () => edit('reorganize') },
          ] }}><Button aria-label={t('directories.more')} icon={<MoreOutlined />} /></Dropdown>
        </> : !directoryId && <Button onClick={() => edit('create')}>{t('directories.create')}</Button>}</Space>
      </header>
      {statusBatches.map(batch => <Alert key={batch.id} showIcon type={batch.status === 'failed' ? 'error' : 'info'}
        message={<Space>{!directoryId && <span>{directories.find(x => x.id === batch.directory_id)?.name || String(batch.result?.directory_name || '')}</span>}{t(batch.status === 'canceled' ? 'directories.folderState_canceled' : batch.status === 'failed' ? 'directories.organizeFailed' : batch.status === 'pending' ? 'directories.organizePending' : 'directories.organizing')}{folderProgress(batch)}</Space>}
        action={<Button size="small" disabled={saving} onClick={() => run({ operation: ['failed', 'canceled'].includes(batch.status) ? 'retry' : 'cancel', batch_id: batch.id })}>{t(['failed', 'canceled'].includes(batch.status) ? 'directories.retry' : 'directories.cancel')}</Button>} />)}
      {folderBatch?.status === 'succeeded' && <Alert type="success" message={folderProgress(folderBatch)} />}
      {loading ? <div className="directory-loading"><Spin /></div> : !directoryId ? <>
        <Typography.Paragraph type="secondary">{t('directories.retention')}</Typography.Paragraph>
        {[true, false].map(pinned => <section className="directory-list-section" key={String(pinned)}>
          <Typography.Title level={4}>{pinned ? t('directories.pinned') : t('directories.recent')} <Typography.Text type="secondary">{directories.filter(x => x.pinned === pinned).length}</Typography.Text></Typography.Title>
          {directories.filter(x => x.pinned === pinned).length === 0 ? <Typography.Paragraph type="secondary" className="directory-empty-section">{t(pinned ? 'directories.noPinned' : 'directories.noDirectories')}</Typography.Paragraph> : <div className="directory-card-grid">{directories.filter(x => x.pinned === pinned).map(x => <Card key={x.id} className="directory-overview-card">
            <div className="directory-card-heading"><FolderOutlined /><Button type="link" onClick={() => navigate(`/document-directories/${x.id}`)}>{x.name}</Button><Tooltip title={t(x.pinned ? 'directories.directoryUnpin' : 'directories.directoryPin')}><Button type="text" className="directory-pin-button" aria-label={t(x.pinned ? 'directories.directoryUnpin' : 'directories.directoryPin')} aria-pressed={x.pinned} icon={x.pinned ? <PushpinFilled /> : <PushpinOutlined />} disabled={saving} onClick={() => run({ operation: 'update', directory_id: x.id, version: x.version, pinned: !x.pinned })} /></Tooltip></div>
            <Typography.Paragraph type="secondary" ellipsis={{ rows: 2 }}>{x.intent}</Typography.Paragraph>
            <Tooltip title={x.organization}><Tag bordered={false} className="directory-card-organization">{x.organization}</Tag></Tooltip><div className="directory-card-footer">{t('directories.lastUsed')}: {new Date(x.last_used_at).toLocaleString()}</div>
          </Card>)}</div>}
        </section>)}
      </> : detail && d ? <>
        {detail.organizing && !statusBatches.some(b => ['pending', 'running'].includes(b.status)) && <Alert type="info" showIcon message={t('directories.organizing')} />}
        <div className="directory-summary"><Space wrap><Tag bordered={false}>{d.organization}</Tag><span>{t('directories.documentCount', { count: documentCount })}</span>{pendingCount > 0 && <span>{t(detail?.total !== undefined ? 'directories.pagePendingCount' : 'directories.pendingCount', { count: pendingCount })}</span>}</Space><Space wrap><Tooltip title={t(d.pinned ? 'directories.directoryUnpin' : 'directories.directoryPin')}><Button type="text" className="directory-pin-button" aria-label={t(d.pinned ? 'directories.directoryUnpin' : 'directories.directoryPin')} aria-pressed={d.pinned} icon={d.pinned ? <PushpinFilled /> : <PushpinOutlined />} disabled={blocked} onClick={() => run({ operation: 'update', pinned: !d.pinned })} /></Tooltip><span>{t('directories.autoAdd')}</span><Switch size="small" aria-label={t('directories.autoAdd')} disabled={blocked} checked={d.auto_add} onChange={(auto_add: boolean) => run({ operation: 'update', auto_add })} /></Space></div>
        {(sourceNotices.length > 0 || inaccessibleCount > 0) && <Alert type="warning" showIcon message={<>
          {sourceNotices.map(notice => <div key={notice}>{notice}</div>)}
          {inaccessibleCount > 0 && <div>{t('directories.inaccessibleDocuments', { count: inaccessibleCount })}</div>}
        </>} />}
        <Tabs activeKey={viewMode} onChange={(value: string) => { setViewMode(value); setPage(1); }} items={[{ key: 'groups', label: t('directories.groupedView') }, { key: 'all', label: t('directories.allDocuments') }, ...(detail?.folder ? [{ key: 'source', label: t('directories.sourceTree') }] : [])]} />
        {viewMode === 'source' && directoryId ? <SourceTree directoryId={directoryId} /> : <div className={`directory-workspace ${viewMode === 'all' ? 'directory-workspace-all' : ''}`} >
          {viewMode === 'groups' && <nav className="directory-groups" aria-label={t('directories.group')}>
            <div className="directory-section-heading"><Typography.Text strong>{t('directories.group')}</Typography.Text><Button type="text" size="small" disabled={blocked} onClick={() => edit('group')}>{t('directories.addGroup')}</Button></div>
            {groupOptions.filter(g => g.value !== '' || ungroupedCount > 0).map(g => {
              const count = detail?.group_counts?.[g.value] ?? entries.filter(e => e.group_id === g.value).length;
              return <button key={g.value} className={`directory-group-button ${activeGroup === g.value ? 'is-selected' : ''}`} aria-pressed={activeGroup === g.value} onClick={() => { setSelectedGroup(g.value); setPage(1); }}><FolderOutlined /><span>{g.label}</span><span className="directory-group-count">{count}</span></button>;
            })}
          </nav>}
          <section className="directory-documents">
            <div className="directory-document-toolbar"><Input allowClear prefix={<SearchOutlined />} aria-label={t('directories.search')} placeholder={t('directories.search')} value={query} onChange={(e: ChangeEvent<HTMLInputElement>) => { setQuery(e.target.value); setPage(1); }} /><Select aria-label={t('directories.filterState')} value={stateFilter} onChange={(value: string) => { setStateFilter(value); setPage(1); }} options={['all', 'unconfirmed', 'unread', 'inaccessible'].map(value => ({ value, label: t(`directories.filter_${value}`) }))} /></div>
            <Table tableLayout="fixed" rowKey="id" dataSource={detail?.total !== undefined ? filteredEntries : filteredEntries.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE)} columns={columns} pagination={false} scroll={{ x: 580, y: 400 }} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('directories.noMatchingDocuments')} /> }} />
            <Pagination className="directory-pagination" current={currentPage} onChange={setPage} total={entryTotal} pageSize={PAGE_SIZE} showSizeChanger={false} hideOnSinglePage={false} showTotal={(total: number) => t('directories.documentCount', { count: total })} />
          </section>
        </div>}
        <Collapse className="directory-secondary-panels" items={[
          { key: 'groups', label: t('directories.manageGroups'), children: detail.groups.map(group => <div className="directory-managed-group" key={group.id}><span>{groupName(group.id)}</span><Space><Button size="small" disabled={blocked} onClick={() => {edit('group'); editingGroup.current = group.id; form.setFieldsValue(group);}}>{t('directories.edit')}</Button><Button size="small" disabled={blocked || detail.groups.some(g => g.parent_id === group.id) || entries.some(e => e.group_id === group.id)} onClick={() => run({operation:'delete_group',group_id:group.id})}>{t('directories.removeGroup')}</Button></Space></div>) },
          { key: 'excluded', label: t('directories.excluded'), children: <ExcludedEntries key={d.id} directoryId={d.id} version={d.version} blocked={blocked} restore={id => run({ operation: 'restore', entry_id: id })} /> },
          { key: 'settings', label: t('directories.settings'), children: <><Typography.Paragraph type="secondary">{t('directories.entryPinHelp')}</Typography.Paragraph><Typography.Paragraph type="secondary">{t('directories.retention')}</Typography.Paragraph><Popconfirm title={t('directories.deleteConfirm')} onConfirm={() => act({ operation: 'delete' })}><Button danger disabled={blocked}>{t('directories.delete')}</Button></Popconfirm></> },
        ]} />
      </> : <Empty />}
    </div>
    <Drawer open={!!selected} title={t('directories.details')} width={460} onClose={() => setSelectedEntry(undefined)}>
      {selected && <div className="directory-entry-detail">
        <Typography.Title level={4}>{entryTitle(selected)}</Typography.Title>
        <Space wrap><Tag>{sourceLabel(selected.source)}</Tag><Tag color={selected.confirmed ? 'green' : 'orange'}>{t(detail?.folder ? 'directories.folderMembership' : selected.confirmed ? 'directories.confirmed' : 'directories.unconfirmed')}</Tag></Space>
        <Typography.Paragraph type="secondary">{t(detail?.folder ? 'directories.folderContract' : selected.confirmed ? 'directories.confirmedHelp' : 'directories.confirmationHelp')}</Typography.Paragraph>
        <Typography.Text strong>{t('directories.aiReadState')}</Typography.Text>
        <div><Tag>{readState(selected)}</Tag></div>
        <Typography.Paragraph type="secondary">{t('directories.aiReadStateHelp')}</Typography.Paragraph>
        {selected.source === 'local' && <Button onClick={() => { setSelectedEntry(undefined); setLocalEntry(selected); }}>{t('directories.previewFile')}</Button>}
        {selected.source === 'mail' && <Button onClick={() => { setSelectedEntry(undefined); setMailEntry(selected); }}>{t('directories.viewMail')}</Button>}
        {entryURL(selected) && <Button href={entryURL(selected)} target="_blank" rel="noreferrer">{t('directories.openOriginal')}</Button>}
        <Typography.Text strong>{t('directories.evidence')}</Typography.Text><Typography.Paragraph>{String(selected.evidence?.reason || t('directories.noEvidence'))}</Typography.Paragraph>
        <Typography.Text strong>{t('directories.group')}</Typography.Text><Select aria-label={t('directories.group')} disabled={blocked} value={selected.group_id} options={groupOptions} onChange={(group_id: string) => run({ operation: 'entry', entry_id: selected.id, group_id })} />
        <Tooltip title={t(selected.pinned ? 'directories.entryUnpin' : 'directories.entryPin')}><Button type="text" className="directory-pin-button" aria-label={t(selected.pinned ? 'directories.entryUnpin' : 'directories.entryPin')} aria-pressed={selected.pinned} icon={selected.pinned ? <PushpinFilled /> : <PushpinOutlined />} disabled={blocked} onClick={() => run({ operation: 'entry_pin', entry_id: selected.id, pinned: !selected.pinned })}>{t(selected.pinned ? 'directories.entryUnpin' : 'directories.entryPin')}</Button></Tooltip>
        <Typography.Paragraph type="secondary">{t('directories.entryPinHelp')}</Typography.Paragraph>
        <Space wrap><Tag>{t(`directories.access_${selected.access_state}`, { defaultValue: selected.access_state })}</Tag><Tag>{t(`directories.content_${selected.content_state || 'unknown'}`)}</Tag></Space>
        <Typography.Text strong>{t('directories.location')}</Typography.Text><Typography.Paragraph copyable={{ text: JSON.stringify(selected.locator || { resource_id: selected.resource_id }) }}>{selected.resource_id}</Typography.Paragraph>
        <Typography.Text type="secondary">{selected.namespace}</Typography.Text>
        <Button danger disabled={blocked} onClick={() => { void act({ operation: 'exclude', entry_id: selected.id }).then(() => setSelectedEntry(undefined)).catch(() => {}); }}>{t('directories.exclude')}</Button>
      </div>}
    </Drawer>
    <LocalFileReader directoryId={directoryId} entry={localEntry} onClose={() => setLocalEntry(undefined)} />
    <MailReader directoryId={directoryId} entry={mailEntry} onClose={() => setMailEntry(undefined)} />
    <Modal open={!!editor} title={t(`directories.${editor || 'create'}`)} onCancel={() => setEditor(undefined)} confirmLoading={saving} onOk={() => { void form.validateFields().then(values => act({ operation: editor!, group_id: editor === 'group' ? editingGroup.current : undefined, ...values })).then(() => setEditor(undefined)).catch(() => {}); }}>
      <Form form={form} layout="vertical">{editor !== 'reorganize' && <Form.Item name="name" label={t('directories.name')} rules={[{ required: true, whitespace: true, max: 255 }]}><Input /></Form.Item>}{(editor === 'create' || editor === 'update') && <Form.Item name="intent" label={t('directories.intent')} rules={[{ required: true, whitespace: true, max: 4000 }]}><Input.TextArea /></Form.Item>}{editor === 'reorganize' && <><Alert message={t('directories.newView')} /><Form.Item name="organization" label={t('directories.organization')} rules={[{ required: true, whitespace: true }]}><Input /></Form.Item></>}{editor === 'group' && <Form.Item name="parent_id" label={t('directories.parent')}><Select allowClear options={groupOptions} /></Form.Item>}</Form>
    </Modal>
  </div>;
}
