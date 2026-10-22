import { useEffect, useMemo, useRef, useState, type InputHTMLAttributes } from 'react';
import { Alert, Button, Checkbox, Input, Modal, Radio, Select, Tooltip } from 'antd';
import { CheckCircleOutlined, CloudOutlined, DesktopOutlined, FolderOpenOutlined, InfoCircleOutlined, LockOutlined, UploadOutlined } from '@ant-design/icons';
import { parseZoteroJSON, previewZotero, readBlobText, summarizeZoteroPreview, zoteroImportError, type ZoteroImportConfig, type ZoteroImportOptions, type ZoteroImportResult, type ZoteroPreview } from './zoteroImport';
import './ZoteroImportModal.scss';

export interface ZoteroImportModalProps {
  open: boolean;
  datasetId: string;
  canWrite: boolean;
  initialFolderId?: string | null;
  onCancel: () => void;
  onImport: (preview: ZoteroPreview, config: ZoteroImportConfig, options: ZoteroImportOptions) => Promise<ZoteroImportResult>;
}
const initialConfig = (): ZoteroImportConfig => ({ source: 'local', library_type: 'users', library_id: '0', local_url: 'http://host.docker.internal:23119/api', api_key: '', webdav: { url: '', username: '', password: '' } });
const directoryInput = { webkitdirectory: '', directory: '' } as InputHTMLAttributes<HTMLInputElement>;

function validateConfig(config: ZoteroImportConfig) {
  if (!/^\d+$/.test(config.library_id) || (config.source !== 'local' && Number(config.library_id) <= 0)) throw new Error('请填写有效的 Zotero 文献库 ID（数字）');
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

export default function ZoteroImportModal({ open, datasetId, canWrite, initialFolderId, onCancel, onImport }: ZoteroImportModalProps) {
  const [config, setConfig] = useState<ZoteroImportConfig>(initialConfig);
  const [localMode, setLocalMode] = useState<'connect' | 'file'>('connect');
  const [localFiles, setLocalFiles] = useState<File[]>([]);
  const [jsonFile, setJSONFile] = useState<File | null>(null);
  const [includeAttachments, setIncludeAttachments] = useState(true);
  const [preview, setPreview] = useState<ZoteroPreview | null>(null);
  const [busy, setBusy] = useState<'preview' | 'import' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [partial, setPartial] = useState(false);
  const request = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const jsonPicker = useRef<HTMLInputElement>(null);
  const folderPicker = useRef<HTMLInputElement>(null);
  const disabled = !!busy || !canWrite;
  const isFile = config.source === 'local' && localMode === 'file';
  const summary = useMemo(() => preview ? summarizeZoteroPreview(preview) : null, [preview]);

  useEffect(() => {
    generation.current++;
    request.current?.abort();
    setConfig(initialConfig()); setLocalMode('connect'); setLocalFiles([]); setJSONFile(null);
    setIncludeAttachments(true); setPreview(null); setBusy(null); setError(null); setPartial(false);
    return () => { generation.current++; request.current?.abort(); };
  }, [open, datasetId]);

  const changeConfig = (patch: Partial<ZoteroImportConfig>) => {
    if (disabled) return;
    setConfig(value => ({ ...value, ...patch })); setPreview(null); setError(null); setPartial(false);
  };
  const changeSource = (source: ZoteroImportConfig['source']) => {
    if (disabled) return;
    setConfig({ ...initialConfig(), source, library_id: source === 'local' ? '0' : '' });
    setPreview(null); setError(null); setPartial(false); setJSONFile(null); setLocalFiles([]); setLocalMode('connect');
  };
  const load = async () => {
    if (disabled) return;
    const scope = generation.current;
    setBusy('preview'); setError(null); setPartial(false); setPreview(null);
    request.current?.abort(); request.current = new AbortController();
    try {
      let next: ZoteroPreview;
      if (isFile) {
        if (!jsonFile) throw new Error('请先选择 Zotero JSON 文件');
        if (jsonFile.size > 20 * 1024 * 1024) throw new Error('Zotero JSON 文件超过 20 MiB 上限');
        next = parseZoteroJSON(await readBlobText(jsonFile));
      } else { validateConfig(config); next = await previewZotero(datasetId, config, request.current.signal); }
      summarizeZoteroPreview(next); // Validate the complete graph before displaying a successful preview.
      if (scope === generation.current) setPreview(next);
    } catch (cause) { if (scope === generation.current) setError(zoteroImportError(cause, config)); }
    finally { if (scope === generation.current) setBusy(null); }
  };
  const commit = async () => {
    if (disabled || !preview) return;
    const scope = generation.current;
    setBusy('import'); setError(null);
    try {
      const result = await onImport(preview, config, { includeAttachments, localFiles, targetFolderId: initialFolderId });
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

  return <Modal open={open} title="从 Zotero 导入" className="zotero-import-modal" width={620} centered destroyOnHidden footer={null}
    onCancel={() => { if (!busy) onCancel(); }} closable={!busy} maskClosable={!busy} keyboard={!busy}>
    <p className="zotero-import-description">连接文献库，保留文件夹层级、标签、笔记与 PDF 附件。</p>
    <Radio.Group className="zotero-source-picker" value={config.source} onChange={event => changeSource(event.target.value)} disabled={disabled} aria-label="Zotero 来源">
      <Radio.Button value="local"><span className="zotero-source-name"><DesktopOutlined />本地 Zotero</span><span className="zotero-source-subtitle">桌面客户端或导出文件</span></Radio.Button>
      <Radio.Button value="remote"><span className="zotero-source-name"><CloudOutlined />远端 Zotero</span><span className="zotero-source-subtitle">个人或群组文献库</span></Radio.Button>
      <Radio.Button value="webdav"><span className="zotero-source-name"><FolderOpenOutlined />WebDAV</span><span className="zotero-source-subtitle">Zotero 题录与网盘附件</span></Radio.Button>
    </Radio.Group>
    <div className="zotero-form">
      {config.source === 'local' && <>
        <Radio.Group value={localMode} size="small" disabled={disabled} onChange={event => { setLocalMode(event.target.value); setPreview(null); setError(null); setPartial(false); }} aria-label="本地导入方式">
          <Radio.Button value="connect">连接客户端</Radio.Button><Radio.Button value="file">选择 JSON 文件</Radio.Button>
        </Radio.Group>
        {isFile ? <div className="zotero-field">
          <label>Zotero JSON 文件</label>
          <div className="zotero-file-picker"><Button icon={<UploadOutlined />} disabled={disabled} onClick={() => jsonPicker.current?.click()}>选择 JSON 文件</Button><span>{jsonFile?.name || '支持 Zotero API JSON、BetterBibTeX JSON'}</span></div>
          <input ref={jsonPicker} type="file" accept=".json,application/json" hidden aria-label="Zotero JSON 文件" disabled={disabled} onChange={event => { setJSONFile(event.target.files?.[0] || null); setPreview(null); setError(null); setPartial(false); }} />
          <p className="zotero-field-hint">可使用 Zotero API 保存的 JSON，或 Better BibTeX 插件导出的 BetterBibTeX JSON。普通 CSL JSON 不包含完整的文件夹与附件关系。</p>
        </div> : <>
          <div className="zotero-field"><div className="zotero-field-label-row"><label htmlFor="zotero-local-url">本地 API 地址</label><Radio.Group size="small" value={config.local_url?.includes('host.docker.internal') ? 'docker' : config.local_url?.includes('localhost') || config.local_url?.includes('127.0.0.1') ? 'direct' : undefined} disabled={disabled} onChange={event => changeConfig({ local_url: event.target.value === 'docker' ? 'http://host.docker.internal:23119/api' : 'http://localhost:23119/api' })} aria-label="本地连接方式"><Radio.Button value="docker">Docker 连接</Radio.Button><Radio.Button value="direct">本机直连</Radio.Button></Radio.Group></div><Input id="zotero-local-url" value={config.local_url} disabled={disabled} onChange={event => changeConfig({ local_url: event.target.value })} /></div>
          <details className="zotero-setup-help">
            <summary>首次连接：设置 Zotero</summary>
            <ol>
              <li>打开桌面版 Zotero 的设置。<span>macOS：顶部菜单「Zotero → 设置」（⌘ ,）；Windows / Linux：「编辑 → 设置」。</span></li>
              <li>选择「高级」，在页面上方勾选<strong>「允许此计算机上的其他应用程序与 Zotero 通讯」</strong>。</li>
              <li>保持 Zotero 运行，再回到此窗口点击「连接并预览」。<span>本地个人文献库的用户 ID 填 0，无需 API Key。</span></li>
            </ol>
            <p>LazyMind 使用容器运行时选择「Docker 连接」，直接运行时选择「本机直连」。远端部署请使用「远端 Zotero」或 JSON 文件。</p>
          </details>
        </>}
      </>}
      {!isFile && <div className="zotero-form-row">
        <div className="zotero-field"><label htmlFor="zotero-library-type">文献库类型</label><Select id="zotero-library-type" value={config.library_type} disabled={disabled || config.source === 'webdav'} options={[{ value: 'users', label: '个人文献库' }, { value: 'groups', label: '群组文献库' }]} onChange={value => changeConfig({ library_type: value, library_id: config.source === 'local' && value === 'users' ? '0' : '' })} /></div>
        <div className="zotero-field"><label htmlFor="zotero-library-id">{config.library_type === 'groups' ? '群组 ID' : '用户 ID'} <Tooltip title="使用数字 ID，不是 Zotero 用户名。本地个人文献库填 0；远端用户 ID 可在 Zotero API Keys 页面查看。"><InfoCircleOutlined /></Tooltip></label><Input id="zotero-library-id" value={config.library_id} inputMode="numeric" placeholder={config.source === 'local' ? '0' : '例如 1234567'} disabled={disabled} onChange={event => changeConfig({ library_id: event.target.value })} /></div>
      </div>}
      {config.source !== 'local' && <div className="zotero-field">
        <label htmlFor="zotero-api-key">API Key</label><Input.Password id="zotero-api-key" value={config.api_key} placeholder="公开文献库可留空" autoComplete="off" disabled={disabled} onChange={event => changeConfig({ api_key: event.target.value })} />
        <p className="zotero-field-hint">在 <a href="https://www.zotero.org/settings/keys" target="_blank" rel="noreferrer">Zotero API Keys</a> 创建具有文献库读取权限的密钥。</p>
      </div>}
      {config.source === 'webdav' && <>
        <p className="zotero-connection-hint">WebDAV 只提供附件，题录和文件夹仍从上面的 Zotero 文献库读取。</p>
        <div className="zotero-field"><label htmlFor="zotero-webdav-url">WebDAV 地址</label><Input id="zotero-webdav-url" value={config.webdav?.url} placeholder="https://dav.example.com/" disabled={disabled} onChange={event => changeConfig({ webdav: { ...config.webdav!, url: event.target.value } })} /><p className="zotero-field-hint">填写 Zotero 同步设置中的地址，支持以 /zotero 结尾的存储目录。</p></div>
        <div className="zotero-form-row"><div className="zotero-field"><label htmlFor="zotero-webdav-user">WebDAV 用户名</label><Input id="zotero-webdav-user" value={config.webdav?.username} autoComplete="off" disabled={disabled} onChange={event => changeConfig({ webdav: { ...config.webdav!, username: event.target.value } })} /></div><div className="zotero-field"><label htmlFor="zotero-webdav-password">WebDAV 密码</label><Input.Password id="zotero-webdav-password" value={config.webdav?.password} autoComplete="new-password" disabled={disabled} onChange={event => changeConfig({ webdav: { ...config.webdav!, password: event.target.value } })} /></div></div>
      </>}
    </div>
    <div className="zotero-options">
      <Checkbox checked={includeAttachments} disabled={disabled} onChange={event => setIncludeAttachments(event.target.checked)}>同时导入 PDF 附件</Checkbox>
      {config.source === 'local' && includeAttachments && <>
        <div className="zotero-file-picker"><Button size="small" icon={<FolderOpenOutlined />} disabled={disabled} onClick={() => folderPicker.current?.click()}>选择本地附件文件夹</Button><span>{localFiles.length ? `已选择 ${localFiles.length} 个文件` : '可选择 Zotero storage 或导出附件目录'}</span></div>
        <input {...directoryInput} ref={folderPicker} type="file" multiple hidden aria-label="本地 Zotero 附件目录" disabled={disabled} onChange={event => { setLocalFiles(Array.from(event.target.files || [])); setError(null); }} />
        <p className="zotero-field-hint">在 Zotero「设置 → 高级 → 文件和文件夹 → 打开数据文件夹」中找到 storage 文件夹，并在此选择。系统按附件标识匹配 PDF；选择 JSON 导入时，也可选择导出文件附带的附件目录。</p>
      </>}
    </div>
    {summary && <div className="zotero-preview" aria-label="导入预览">
      <div className="zotero-preview-heading"><CheckCircleOutlined />已读取文献库，确认后开始导入</div>
      <div className="zotero-preview-counts">{[[summary.papers, '文献'], [summary.folders, '文件夹'], [summary.pdfs, 'PDF 附件'], [summary.notes + summary.annotations, '笔记与批注']].map(([count, label]) => <div className="zotero-preview-count" key={label}><strong>{count}</strong><span>{label}</span></div>)}</div>
      {!!summary.titles.length && <ul className="zotero-preview-titles">{summary.titles.map((title, index) => <li key={index} title={title}>{title}</li>)}</ul>}
    </div>}
    {!!summary?.warnings.length && <Alert className="zotero-import-alert" type="warning" showIcon message={summary.warnings.join('\n')} />}
    {error && <Alert className="zotero-import-alert" type="error" showIcon message={error} />}
    {!canWrite && <Alert className="zotero-import-alert" type="warning" showIcon message="需要论文库编辑权限才能导入" />}
    <div className="zotero-import-footer">
      <span className="zotero-private-hint"><LockOutlined />密钥和密码仅用于本次导入</span>
      <Button disabled={!!busy} onClick={onCancel}>取消</Button>
      {preview ? <Button type="primary" disabled={disabled || (!summary?.papers && !summary?.folders)} loading={busy === 'import'} onClick={commit}>{partial ? '重试未完成的导入' : '确认导入'}</Button>
        : <Button type="primary" disabled={disabled || (isFile && !jsonFile)} loading={busy === 'preview'} onClick={load}>{isFile ? '读取并预览' : '连接并预览'}</Button>}
    </div>
  </Modal>;
}
