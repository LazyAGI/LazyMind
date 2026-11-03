import ZoteroWebDAVAddress from './ZoteroWebDAVAddress';
import PaperAuthors from './PaperAuthors';
import { memo, useCallback, useEffect, useMemo, useRef, useState, type InputHTMLAttributes } from 'react';
import { Alert, Button, Checkbox, Drawer, Input, Modal, Pagination, Radio, Select, Tooltip, Tree } from 'antd';
import { ApiOutlined,  DatabaseOutlined, FilePdfOutlined, FolderOpenOutlined, FolderOutlined, DownOutlined, InfoCircleOutlined, LockOutlined, SearchOutlined, UploadOutlined } from '@ant-design/icons';
import { parseZoteroJSON, previewZotero, readBlobText, zoteroImportError, type ZoteroDatabaseInfo, type ZoteroImportConfig, type ZoteroImportOptions, type ZoteroImportResult, type ZoteroPreview } from './zoteroImport';
import { filterZoteroPreview, importedZoteroKeys, zoteroBrowserIndex, listZoteroPapers, zoteroSelectionWarnings, type ZoteroSelectablePaper } from './zoteroSelection';
import './ZoteroImportModal.scss';

export interface ZoteroImportModalProps {
  open: boolean;
  datasetId: string;
  canWrite: boolean;
  initialFolderId?: string | null;
  persistedPapers?: readonly { zoteroSources?: string[] }[];
  onCancel: () => void;
  onImport: (preview: ZoteroPreview, config: ZoteroImportConfig, options: ZoteroImportOptions) => Promise<ZoteroImportResult>;
}
const initialConfig = (source: ZoteroImportConfig['source'] = 'database'): ZoteroImportConfig => ({ source, library_type: 'users', library_id: source === 'local' || source === 'database' ? '0' : '', ...(source === 'local' ? { local_url: 'http://host.docker.internal:23119/api' } : {}), ...(source === 'remote' || source === 'webdav' ? { api_key: '' } : {}), ...(source === 'webdav' ? { webdav: { url: '', username: '', password: '' } } : {}) });
const directoryInput = { webkitdirectory: '', directory: '' } as InputHTMLAttributes<HTMLInputElement>;
const pageSize = 50;
const noPersistedPapers: readonly { zoteroSources?: string[] }[] = [];

function validateConfig(config: ZoteroImportConfig) {
  if (!/^\d+$/.test(config.library_id) || (!['local', 'database'].includes(config.source) && Number(config.library_id) <= 0)) throw new Error('请填写有效的 Zotero 文献库 ID（数字）');
  if (config.source === 'local') {
    let url: URL;
    try { url = new URL(config.local_url || ''); } catch { throw new Error('请填写本地 Zotero API 地址'); }
    if (!['localhost', '127.0.0.1', 'host.docker.internal'].includes(url.hostname) || url.port !== '23119' || !['/api', '/api/'].includes(url.pathname) || url.protocol !== 'http:' || url.username || url.password || url.search || url.hash) throw new Error('本地地址仅支持 localhost、127.0.0.1 或 host.docker.internal 的 23119/api 地址');
  }
  if (config.source === 'webdav') {
    if (config.library_type === 'groups') throw new Error('Zotero 群组文献库不支持 WebDAV，请使用远端 Zotero');
    if (!config.webdav?.url.trim()) throw new Error('请填写 WebDAV 地址');
    let url: URL;
    try { url = new URL(config.webdav.url); } catch { throw new Error('WebDAV 地址格式无效'); }
    if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error('WebDAV 地址需为 HTTP(S) 地址，请在单独字段填写账号和密码');
  }
}


// Stable rows avoid rerendering every checkbox and tooltip on each selection.
const ZoteroPaperRow = memo(function ZoteroPaperRow({ paper, checked, disabled, imported, onToggle }: {
  paper: ZoteroSelectablePaper; checked: boolean; disabled: boolean; imported: boolean; onToggle: (key: string, checked: boolean) => void;
}) {
 return <div role="listitem" className={`zotero-paper-row${checked ? ' is-selected' : ''}`}>
          <Checkbox aria-label={`选择 ${paper.title}`} checked={checked} disabled={disabled || imported} onChange={event => onToggle(paper.key, event.target.checked)} />
          <div className="zotero-paper-details"><label className="zotero-paper-title" onClick={() => { if (!disabled && !imported) onToggle(paper.key, !checked); }}>{paper.title}</label><div className="zotero-paper-meta"><PaperAuthors authors={paper.authors} />{paper.venue && <span className="zotero-paper-venue" title={paper.venue}>{paper.venue}</span>}<span>{paper.year || '年份未记录'}</span></div>{!!paper.collections.length && <div className="zotero-paper-collections"><FolderOpenOutlined aria-hidden />{paper.collections.join('；')}</div>}</div>
          <div className="zotero-paper-materials">{imported && <span className="zotero-imported-badge">已导入</span>}{!!paper.pdfCount && <span><FilePdfOutlined aria-hidden />{paper.pdfCount} PDF</span>}{!!paper.unavailablePdfCount && <Tooltip title="本机无法读取这些 PDF。请修复 Zotero 附件路径后重试，或取消勾选 PDF 附件以导入题录。"><span className="zotero-pdf-unavailable">{paper.unavailablePdfCount} 个 PDF 不可用</span></Tooltip>}{!!(paper.noteCount + paper.annotationCount) && <span>{paper.noteCount + paper.annotationCount} 条笔记与批注</span>}</div>
        </div>;
});

export default function ZoteroImportModal({ open, datasetId, canWrite, initialFolderId, persistedPapers = noPersistedPapers, onCancel, onImport }: ZoteroImportModalProps) {
  const [config, setConfig] = useState<ZoteroImportConfig>(() => initialConfig());
  const [localMode, setLocalMode] = useState<'connect' | 'file'>('connect');
  const [localFiles, setLocalFiles] = useState<File[]>([]);
  const [jsonFile, setJSONFile] = useState<File | null>(null);
  const [includeAttachments, setIncludeAttachments] = useState(true);
  const [preview, setPreview] = useState<ZoteroPreview | null>(null);
  const [databaseInfo, setDatabaseInfo] = useState<ZoteroDatabaseInfo | null>(null);
  const [papers, setPapers] = useState<ZoteroSelectablePaper[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState('');
  const [sourceFolder, setSourceFolder] = useState<string | null>(null);
  const [completeImported, setCompleteImported] = useState(false);
  const retryKeys = useRef<Set<string>>(new Set());
  const [page, setPage] = useState(1);
  const [showSettings, setShowSettings] = useState(false);
  const [showGuide, setShowGuide] = useState(false);
  const [busy, setBusy] = useState<'preview' | 'import' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [partial, setPartial] = useState(false);
  const [importProgress, setImportProgress] = useState('');
  const request = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const jsonPicker = useRef<HTMLInputElement>(null);
  const folderPicker = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => { if (listRef.current) listRef.current.scrollTop = 0; }, [page, query, sourceFolder]);
  const disabled = !!busy || !canWrite;
  const isDatabase = config.source === 'database';
  const isFile = config.source === 'local' && localMode === 'file';
  const browserIndex = useMemo(() => preview ? zoteroBrowserIndex(preview) : null, [preview]);
  const imported = useMemo(() => preview ? importedZoteroKeys(preview.source_id, papers, persistedPapers) : new Set<string>(), [preview, papers, persistedPapers]);
  const effectiveSelected = useMemo(() => new Set([...selected, ...imported]), [selected, imported]);
  const newSelectedCount = [...selected].filter(key => !imported.has(key)).length;
  const visiblePapers = useMemo(() => {
    const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
    return papers.filter(paper => (!sourceFolder || browserIndex?.memberships.get(paper.key)?.has(sourceFolder)) && terms.every(term => paper.searchText.includes(term)));
  }, [papers, query, sourceFolder, browserIndex]);
  const selectSourceFolder = (key: string | null) => { setSourceFolder(key); setPage(1); };
  const selectedCounts = useMemo(() => papers.reduce((counts, paper) => selected.has(paper.key) ? { pdfs: counts.pdfs + paper.pdfCount, unavailable: counts.unavailable + paper.unavailablePdfCount, notes: counts.notes + paper.noteCount + paper.annotationCount } : counts, { pdfs: 0, unavailable: 0, notes: 0 }), [papers, selected]);

  const resetPreview = () => {
    setPreview(null); setPapers([]); setSelected(new Set()); setQuery(''); setPage(1); setError(null); setPartial(false); setShowSettings(false); setSourceFolder(null); setCompleteImported(false); retryKeys.current = new Set();
  };
  useEffect(() => {
    generation.current++; request.current?.abort();
    setShowGuide(false); setConfig(initialConfig()); setLocalMode('connect'); setLocalFiles([]); setJSONFile(null); setDatabaseInfo(null);
    setIncludeAttachments(true); resetPreview(); setBusy(null);
    return () => { generation.current++; request.current?.abort(); };
  }, [open, datasetId]);

  const changeConfig = (patch: Partial<ZoteroImportConfig>) => {
    if (disabled) return;
    setConfig(value => ({ ...value, ...patch })); resetPreview();
  };
  const changeSource = (source: ZoteroImportConfig['source']) => {
    if (disabled) return;
    setConfig(initialConfig(source)); resetPreview(); setDatabaseInfo(null); setJSONFile(null); setLocalFiles([]); setLocalMode('connect');
  };
  const cancel = () => {
    if (busy === 'import') return;
    generation.current++; request.current?.abort(); request.current = null;
    setBusy(null); resetPreview(); setConfig(initialConfig()); setDatabaseInfo(null); setJSONFile(null); setLocalFiles([]);
    onCancel();
  };
  const load = async () => {
    if (disabled) return;
    const scope = generation.current;
    setBusy('preview'); resetPreview();
    request.current?.abort(); request.current = new AbortController();
    try {
      let next: ZoteroPreview;
      if (isFile) {
        if (!jsonFile) throw new Error('请先选择 Zotero JSON 文件');
        if (jsonFile.size > 20 * 1024 * 1024) throw new Error('Zotero JSON 文件超过 20 MiB 上限');
        next = parseZoteroJSON(await readBlobText(jsonFile));
      } else { validateConfig(config); next = await previewZotero(datasetId, config, request.current.signal); }
      const nextPapers = listZoteroPapers(next);
      if (scope === generation.current) {
        setPreview({ ...next, warnings: zoteroSelectionWarnings(next) }); setPapers(nextPapers);
        if (next.database && isDatabase) {
          setDatabaseInfo(next.database);
          setConfig(value => ({ ...value, library_type: next.database!.library_type, library_id: next.database!.library_id }));
        }
      }
    } catch (cause) { if (scope === generation.current) setError(zoteroImportError(cause, config)); }
    finally { if (scope === generation.current) setBusy(null); }
  };
  const commit = async () => {
    if (disabled || !preview) return;
    const commitKeys = partial ? retryKeys.current : new Set([...effectiveSelected].filter(key => !imported.has(key) || completeImported));
    if (!commitKeys.size) return;
    retryKeys.current = new Set(commitKeys);
    const scope = generation.current;
    setBusy('import'); setImportProgress(`正在导入 ${commitKeys.size} 篇`); setError(null);
    try {
      const chosenPreview = filterZoteroPreview(preview, commitKeys);
      const result = await onImport(chosenPreview, config, { includeAttachments, localFiles, targetFolderId: initialFolderId, onProgress: stage => { if (scope === generation.current) setImportProgress(stage); } });
      if (scope !== generation.current) return;
      if (!result || !Number.isSafeInteger(result.imported) || !Number.isSafeInteger(result.attachments) || result.imported < 0 || result.attachments < 0) throw new Error('导入结果无效，请重新加载论文库后检查');
      if (result.failures?.length) {
        setPartial(true);
        setError(`文献已保存，但有附件未完成。重试会继续导入未完成的附件。\n${result.failures.join('\n')}`);
      } else onCancel();
    } catch (cause) {
      if (scope === generation.current) { setPartial(true); setError(zoteroImportError(cause, config)); }
    } finally { if (scope === generation.current) setBusy(null); }
  };
  const togglePaper = useCallback((key: string, checked: boolean) => {
    setSelected(current => { const next = new Set(current); if (checked) next.add(key); else next.delete(key); return next; });
  }, []);

  const footer = <div className="zotero-import-footer"><span className="zotero-private-hint"><LockOutlined />{isDatabase ? '确认选择后才会写入论文库' : '密钥和密码仅用于本次导入'}</span><Button disabled={busy === 'import'} onClick={cancel}>取消</Button>
      {preview ? <Button type="primary" disabled={disabled || (!newSelectedCount && !(completeImported && imported.size) && !partial)} loading={busy === 'import'} onClick={commit}>{busy === 'import' ? importProgress : partial ? '重试未完成的导入' : newSelectedCount ? `确认导入 ${newSelectedCount} 篇` : completeImported && imported.size ? '补全已导入附件' : '确认导入 0 篇'}</Button> : <Button type="primary" disabled={disabled || (isFile && !jsonFile)} loading={busy === 'preview'} onClick={load}>{isDatabase ? '读取本地数据库' : isFile ? '读取并预览' : '连接并预览'}</Button>}
    </div>;

  return <Modal open={open} title={null} aria-labelledby="zotero-import-title" className={`zotero-import-modal${preview ? " is-expanded" : ""}`} width={preview ? 1440 : 760} centered destroyOnHidden footer={preview ? null : footer}
    onCancel={cancel} closable={busy !== 'import'} maskClosable={busy !== 'import'} keyboard={busy !== 'import'}>
    <div className="zotero-import-workspace"><aside className="zotero-import-settings">
    <div className="zotero-settings-heading"><h2 id="zotero-import-title" className="zotero-compact-title">从 Zotero 导入</h2><Button type="text" size="small" aria-label="接入指引" icon={<InfoCircleOutlined aria-hidden />} onClick={() => setShowGuide(true)}>接入指引</Button></div>
    <details className="zotero-source-settings" open={!preview}><summary>来源设置 · {isDatabase ? '本地数据库' : 'API'}</summary>
    <div role="radiogroup" aria-label="导入来源"><Radio.Group className="zotero-source-picker" value={isDatabase ? 'database' : 'api'} disabled={disabled} onChange={event => changeSource(event.target.value === 'database' ? 'database' : 'local')}>
      <Radio.Button value="database"><span className="zotero-source-name"><DatabaseOutlined aria-hidden />本地数据库</span><span className="zotero-source-subtitle">直接读取 Zotero 文献与附件</span></Radio.Button>
      <Radio.Button value="api" aria-label="API"><span className="zotero-source-name"><ApiOutlined aria-hidden />API</span><span className="zotero-source-subtitle">通过客户端、远端或 WebDAV 连接</span></Radio.Button>
    </Radio.Group></div>
    {isDatabase ? <div className="zotero-database-info">
      {!preview && <p>自动查找本机的 zotero.sqlite，无需启动 Zotero、开启 API 或填写密钥。</p>}
      {databaseInfo && <>
        <div className="zotero-field"><label htmlFor="zotero-database-library">本地文献库</label><Select id="zotero-database-library" value={`${config.library_type}:${config.library_id}`} disabled={disabled} options={databaseInfo.libraries.map(library => ({ value: `${library.type}:${library.id}`, label: library.name }))} onChange={value => { const [library_type, library_id] = value.split(':'); changeConfig({ library_type: library_type as 'users' | 'groups', library_id }); }} /></div>
        <div className="zotero-database-location"><span>本机 Zotero 数据目录 · 只读访问</span><details><summary>路径详情</summary><code>{databaseInfo.path}</code><p>容器部署时显示挂载路径，直接读取原数据库，不会复制数据库。</p></details></div>
      </>}
    </div> : <>
      <div className="zotero-api-heading"><div role="radiogroup" aria-label="API 连接方式"><Radio.Group value={config.source} size="small" disabled={disabled} onChange={event => changeSource(event.target.value)}>
        <Radio.Button value="local">本地客户端</Radio.Button><Radio.Button value="remote">远端 Zotero</Radio.Button><Radio.Button value="webdav">WebDAV</Radio.Button>
      </Radio.Group></div>{preview && <Button type="link" size="small" disabled={disabled} onClick={() => setShowSettings(!showSettings)}>{showSettings ? '收起设置' : '连接设置'}</Button>}</div>
      {(!preview || showSettings) && <div className="zotero-form">
        {config.source === 'local' && <>
          <div className="zotero-field"><div className="zotero-field-label-row"><label htmlFor="zotero-local-url">本地 API 地址</label><Radio.Group size="small" value={config.local_url?.includes('host.docker.internal') ? 'docker' : 'direct'} disabled={disabled || isFile} onChange={event => changeConfig({ local_url: event.target.value === 'docker' ? 'http://host.docker.internal:23119/api' : 'http://localhost:23119/api' })} aria-label="本地连接方式"><Radio.Button value="docker">Docker 连接</Radio.Button><Radio.Button value="direct">本机直连</Radio.Button></Radio.Group></div><Input id="zotero-local-url" value={config.local_url} disabled={disabled || isFile} onChange={event => changeConfig({ local_url: event.target.value })} /></div>

          <details className="zotero-json-fallback"><summary>已有 Zotero JSON 导出文件</summary><Checkbox checked={isFile} disabled={disabled} onChange={event => { setLocalMode(event.target.checked ? 'file' : 'connect'); resetPreview(); }}>从 JSON 文件读取</Checkbox>{isFile && <div className="zotero-file-picker"><Button icon={<UploadOutlined />} disabled={disabled} onClick={() => jsonPicker.current?.click()}>选择 JSON 文件</Button><span>{jsonFile?.name || '支持 Zotero API JSON、BetterBibTeX JSON'}</span><input ref={jsonPicker} type="file" accept=".json,application/json" hidden aria-label="Zotero JSON 文件" disabled={disabled} onChange={event => { setJSONFile(event.target.files?.[0] || null); resetPreview(); }} /></div>}</details>
        </>}
        {!isFile && <div className="zotero-form-row"><div className="zotero-field"><label htmlFor="zotero-library-type">文献库类型</label><Select id="zotero-library-type" value={config.library_type} disabled={disabled || config.source === 'webdav'} options={[{ value: 'users', label: '个人文献库' }, { value: 'groups', label: '群组文献库' }]} onChange={value => changeConfig({ library_type: value, library_id: config.source === 'local' && value === 'users' ? '0' : '' })} /></div><div className="zotero-field"><label htmlFor="zotero-library-id">{config.library_type === 'groups' ? '群组 ID' : '用户 ID'} <Tooltip title="使用数字 ID，不是 Zotero 用户名。本地个人文献库填 0；远端用户 ID 可在 Zotero API Keys 页面查看。"><InfoCircleOutlined /></Tooltip></label><Input id="zotero-library-id" value={config.library_id} inputMode="numeric" placeholder={config.source === 'local' ? '0' : '例如 1234567'} disabled={disabled} onChange={event => changeConfig({ library_id: event.target.value })} /></div></div>}
        {config.source !== 'local' && <div className="zotero-field"><label htmlFor="zotero-api-key">API Key</label><Input.Password id="zotero-api-key" value={config.api_key} placeholder="公开文献库可留空" autoComplete="off" disabled={disabled} onChange={event => changeConfig({ api_key: event.target.value })} /></div>}
        {config.source === 'webdav' && <>
          <div className="zotero-field"><label htmlFor="zotero-webdav-url">WebDAV 地址</label><ZoteroWebDAVAddress value={config.webdav?.url || ""} disabled={disabled} onChange={url => changeConfig({ webdav: { ...config.webdav!, url } })} /></div>
          <div className="zotero-form-row"><div className="zotero-field"><label htmlFor="zotero-webdav-user">WebDAV 用户名</label><Input id="zotero-webdav-user" value={config.webdav?.username} autoComplete="off" disabled={disabled} onChange={event => changeConfig({ webdav: { ...config.webdav!, username: event.target.value } })} /></div><div className="zotero-field"><label htmlFor="zotero-webdav-password">WebDAV 密码</label><Input.Password id="zotero-webdav-password" value={config.webdav?.password} autoComplete="new-password" disabled={disabled} onChange={event => changeConfig({ webdav: { ...config.webdav!, password: event.target.value } })} /></div></div>
        </>}
      </div>}
    </>}
    </details>
    <div className="zotero-options"><Checkbox checked={includeAttachments} disabled={disabled} onChange={event => { setIncludeAttachments(event.target.checked); if (!event.target.checked) setCompleteImported(false); }}>同时导入 PDF 附件</Checkbox>{preview && <span className="zotero-selection-materials">本次选择包含 {selectedCounts.pdfs} 个 PDF、{selectedCounts.notes} 条笔记与批注{selectedCounts.unavailable > 0 && <span className="zotero-pdf-unavailable">（其中 {selectedCounts.unavailable} 个 PDF 当前不可用）</span>}</span>}
      {config.source === 'local' && includeAttachments && <><div className="zotero-file-picker"><Button size="small" icon={<FolderOpenOutlined />} disabled={disabled} onClick={() => folderPicker.current?.click()}>选择本地附件文件夹</Button><span>{localFiles.length ? `已选择 ${localFiles.length} 个文件` : '选择 Zotero storage 或导出附件目录'}</span></div><input {...directoryInput} ref={folderPicker} type="file" multiple hidden aria-label="本地 Zotero 附件目录" disabled={disabled} onChange={event => { setLocalFiles(Array.from(event.target.files || [])); setError(null); }} /></>}
    </div>
    {!!imported.size && <div className="zotero-mapping-summary"><span>{imported.size} 篇已关联到当前论文库</span><Checkbox checked={completeImported} disabled={disabled || !includeAttachments} onChange={event => setCompleteImported(event.target.checked)}>补全已导入文献的附件</Checkbox></div>}
    {preview && <nav className="zotero-folder-browser" aria-label="Zotero 文件夹"><div className="zotero-folder-caption">文献库</div><Button type="text" className={!sourceFolder ? 'is-active' : ''} onClick={() => selectSourceFolder(null)}>全部文献 <span>{papers.length}</span></Button><Tree aria-label="Zotero 文件夹目录" showIcon blockNode switcherIcon={<DownOutlined />} treeData={browserIndex?.folders} selectedKeys={sourceFolder ? [sourceFolder] : []} onSelect={keys => selectSourceFolder(keys.length ? String(keys[0]) : null)} titleRender={node => <span className="zotero-folder-title"><FolderOutlined /><span>{node.title}</span><small>{node.count}</small></span>} /></nav>}
    {!!preview?.warnings.length && <details className="zotero-warning-details"><summary>导入提示（{preview.warnings.length}）</summary><Alert className="zotero-import-alert" type="warning" showIcon message={preview.warnings.join('\n')} /></details>}
    {error && <Alert className="zotero-import-alert" type="error" showIcon message={error} />}
    {!canWrite && <Alert className="zotero-import-alert" type="warning" showIcon message="需要论文库编辑权限才能导入" />}
    </aside>
    {preview && <section className="zotero-selection" aria-label="选择导入文献">
      <div className="zotero-selection-heading"><strong>{sourceFolder ? "选择文件夹内的文献" : "选择要导入的文献"}</strong><span>{visiblePapers.length} 篇文献</span></div>
      <Input aria-label="搜索文献" className="zotero-search" prefix={<SearchOutlined aria-hidden />} placeholder="搜索标题、作者或年份" allowClear value={query} disabled={disabled} onChange={event => { setQuery(event.target.value); setPage(1); }} />
      <div className="zotero-selection-toolbar"><span role="status">已选择 {effectiveSelected.size} / {papers.length} 篇{imported.size > 0 && <small> · 已导入 {imported.size} · 新选择 {newSelectedCount}</small>}</span><div><Button size="small" type="text" disabled={disabled || !visiblePapers.length} onClick={() => setSelected(current => new Set([...current, ...visiblePapers.map(paper => paper.key)]))}>{query.trim() ? '选择搜索结果' : sourceFolder ? '选择此文件夹' : '全选'}</Button><Button size="small" type="text" disabled={disabled || !selected.size} onClick={() => setSelected(new Set())}>清空选择</Button></div></div>
      <div ref={listRef} className="zotero-paper-list" role="list" aria-label="可导入文献">
        {visiblePapers.slice((page - 1) * pageSize, page * pageSize).map(paper => <ZoteroPaperRow key={paper.key} paper={paper} checked={effectiveSelected.has(paper.key)} imported={imported.has(paper.key)} disabled={disabled} onToggle={togglePaper} />)}
        {!visiblePapers.length && <div className="zotero-list-empty">{papers.length ? '没有匹配的文献，请调整搜索词。' : '此文献库中没有可导入的论文或独立 PDF。'}</div>}
      </div>
      {visiblePapers.length > pageSize && <Pagination className="zotero-list-pagination" size="small" current={page} pageSize={pageSize} total={visiblePapers.length} showSizeChanger={false} disabled={disabled} onChange={setPage} />}
    <div className="zotero-selection-actions">{footer}</div>
    </section>}
    </div>

    <Drawer title="Zotero 接入指引" placement="right" width={420} open={open && showGuide} onClose={() => setShowGuide(false)} zIndex={1100} rootClassName="zotero-connection-guide">
      <section><h3>用户数字 ID 与 API Key</h3><ol>
        <li>登录 <a href="https://www.zotero.org/settings/keys" target="_blank" rel="noreferrer">Zotero API Keys 页面</a>（官网 Settings → Feeds/API → API Keys）。</li>
        <li>找到 <code>Your userID for use in API calls is …</code>，将后面的数字填入「用户 ID」。这不是用户名，也不是邮箱。</li>
        <li>点击 <strong>Create new private key</strong>，为 LazyMind 创建专用密钥，授予所需文献库的读取权限；读取 Zotero 云端附件时还需允许读取文件。复制到「API Key」。公开题录可不填密钥。</li>
      </ol><p>Security 页面中的 <strong>Register a new key</strong> 是登录用的 Passkey，不是 API Key。请使用上面的直达链接。</p><p>群组文献库请填写群组的数字 ID，并为密钥授权相应群组的读取权限。</p></section>
      <section><h3>WebDAV：与 Zotero 保持一致</h3><ol><li>打开 Zotero「设置 → 账户／同步 → 文件同步」，查看 WebDAV 设置。</li><li>分别选择协议、填写地址主体和 WebDAV 用户名／密码。右侧固定的 <code>/zotero/</code> 会自动补上；粘贴完整 URL 也会自动拆分。</li><li>例如 Zotero 中填写 <code>https</code> 和 <code>dav.example.com/dav</code>，实际目录是 <code>https://dav.example.com/dav/zotero/</code>。</li></ol><p>WebDAV 只提供附件，题录和文件夹仍从上面的 Zotero 文献库读取，因此仍需数字用户 ID 和必要的 API Key。WebDAV 账号不一定与 Zotero 账号相同；群组文献库不支持 WebDAV。</p></section>
      <section><h3>本地客户端</h3><ol><li>打开 Zotero「设置 → 高级」，勾选「允许此计算机上的其他应用程序与 Zotero 通讯」。</li><li>保持 Zotero 运行。个人文献库 ID 填 <code>0</code>，读取不需要 API Key。</li><li>LazyMind 容器部署选择「Docker 连接」，直接运行选择「本机直连」。</li></ol><p>如果需要本地 PDF，选择 Zotero 数据目录中的 <code>storage</code> 文件夹。可在「设置 → 高级 → 文件和文件夹」查看数据目录。</p></section>
      <p className="zotero-guide-links"><a href="https://www.zotero.org/support/dev/web_api/v3/basics" target="_blank" rel="noreferrer">官方 API 说明</a> · <a href="https://www.zotero.org/support/sync" target="_blank" rel="noreferrer">官方同步说明</a></p>
    </Drawer>
  </Modal>;
}
