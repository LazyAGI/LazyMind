import { Component, useEffect, useRef, useState, type ReactNode } from 'react';
import { Button, Modal, Spin, message } from 'antd';
import { CloseOutlined, DownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import FileViewer from '@/modules/knowledge/components/FileViewer';
import HtmlBlock from '../MarkdownViewer/HtmlBlock';
import MarkdownViewer from '../MarkdownViewer';
import { ExecutionResults, SourceImportProvider } from '../SourceImports';
import { ReferenceSourcesButton } from '../SourceImports/ReferenceSources';
import { downloadStream } from '../../utils/download';
import { previewFilename, previewKind, readPreviewFile, urlPreviewTarget, type ArtifactPreviewTarget } from './target';
import './index.scss';

class PreviewBoundary extends Component<{ children: ReactNode; fallback: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() { return this.state.failed ? this.props.fallback : this.props.children; }
}

export function PreviewContent({ target, close, onVersions }: { target: ArtifactPreviewTarget; close: () => void; onVersions?: () => void }) {
  const { t } = useTranslation();
  const [attempt, setAttempt] = useState(0);
  const [data, setData] = useState<ArrayBuffer>();
  const [error, setError] = useState('');
  const [downloading, setDownloading] = useState(false);
  const downloadController = useRef<AbortController>();
  const kind = previewKind(target);
  useEffect(() => {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => { setError('failed'); controller.abort(); }, 60_000);
    setData(undefined); setError('');
    if (kind !== 'unsupported') {
      void readPreviewFile(target, controller.signal).then(bytes => {
        if (!controller.signal.aborted) setData(bytes);
      }).catch(failure => {
        if (controller.signal.aborted) return;
        setError(failure instanceof Error && failure.message === 'unavailable' ? 'unavailable' : 'failed');
      }).finally(() => window.clearTimeout(timeout));
    } else {
      window.clearTimeout(timeout);
    }
    return () => { window.clearTimeout(timeout); controller.abort(); };
  }, [target, attempt, kind]);
  useEffect(() => () => downloadController.current?.abort(), []);
  const retry = () => setAttempt(value => value + 1);
  const download = async () => {
    if (downloading) return;
    const controller = new AbortController(); downloadController.current = controller;
    const timeout = window.setTimeout(() => {
      if (!controller.signal.aborted) {
        message.error(t('chat.preview.downloadFailed'));
        setDownloading(false);
        controller.abort();
      }
    }, 60_000);
    setDownloading(true);
    try {
      const downloadTarget = target.downloadUrl && target.downloadUrl !== target.url
        ? { ...target, ...urlPreviewTarget(target.downloadUrl, target.filename, target.conversationId), content: undefined }
        : target;
      const bytes = await readPreviewFile(downloadTarget, controller.signal);
      if (!controller.signal.aborted) downloadStream(new Blob([bytes], { type: target.mimeType }), target.filename);
    } catch {
      if (!controller.signal.aborted) message.error(t('chat.preview.downloadFailed'));
    } finally { window.clearTimeout(timeout); if (!controller.signal.aborted) setDownloading(false); }
  };
  const failure = <div className="artifact-preview__state" role="alert">
    <p>{t(`chat.preview.${error || 'failed'}`)}</p>
    <Button icon={<ReloadOutlined />} onClick={retry} aria-label={t('chat.preview.retry')}>{t('chat.preview.retry')}</Button>
  </div>;
  const sourceContext = target.sourceContext;
  const renderMarkdown = sourceContext ? (markdown: string) => <MarkdownViewer sources={sourceContext.sources}>{markdown}</MarkdownViewer> : undefined;
  let content: ReactNode;
  if (kind === 'unsupported') content = <div className="artifact-preview__state" role="status"><p>{t('chat.preview.unsupported')}</p><span>{target.filename}</span></div>;
  else if (error) content = failure;
  else if (data === undefined) content = <div className="artifact-preview__state" role="status"><Spin /><p>{t('common.loading')}</p></div>;
  else if (kind === 'html') content = <HtmlBlock code={new TextDecoder().decode(data)} allowDownload={target.canDownload !== false} />;
  else if (kind === 'json' || kind === 'text') {
    let text = new TextDecoder().decode(data);
    if (kind === 'json') { try { text = JSON.stringify(JSON.parse(text), null, 2); } catch { /* Keep malformed JSON readable. */ } }
    content = <pre className="artifact-preview__text">{text}</pre>;
  } else content = <FileViewer data={data} fileName={previewFilename(target)} renderMarkdown={renderMarkdown} onPreviewError={() => setError('failed')} />;
  if (sourceContext) content = <SourceImportProvider context={sourceContext.context} sources={sourceContext.sources}>
    {content}
    <div className="chat-assistant-msg-tool-actions"><ReferenceSourcesButton sources={sourceContext.sources} /><ExecutionResults operations={sourceContext.operations} /></div>
  </SourceImportProvider>;
  return <section className="artifact-preview" aria-label={t('chat.previewNamedFile', { name: target.filename })}>
    <header className="artifact-preview__header">
      <strong title={target.filename}>{target.filename}</strong>
      {onVersions && <Button size="small" onClick={() => { close(); onVersions(); }}>{t('chat.preview.versions')}</Button>}
      <Button type="text" icon={<DownloadOutlined />} loading={downloading} disabled={error === 'unavailable' || target.canDownload === false} onClick={() => void download()} aria-label={t('chat.preview.download', { name: target.filename })} />
      <Button type="text" icon={<CloseOutlined />} onClick={close} aria-label={t('chat.closePreview')} />
    </header>
    <div className="artifact-preview__body">
      <PreviewBoundary key={attempt} fallback={failure}>{content}</PreviewBoundary>
    </div>
  </section>;
}

export default function ArtifactPreview({ target, onClose, modal = false, onVersions }: {
  target: ArtifactPreviewTarget; onClose: () => void; modal?: boolean; onVersions?: () => void;
}) {
  const { t } = useTranslation();
  const [narrow, setNarrow] = useState(() => window.matchMedia('(max-width: 900px)').matches);
  useEffect(() => {
    const media = window.matchMedia('(max-width: 900px)');
    const update = () => setNarrow(media.matches);
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);
  useEffect(() => {
    if (modal || narrow) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || document.querySelector('.ant-modal-wrap:not([style*="display: none"]), .ant-dropdown:not(.ant-dropdown-hidden), .ant-select-dropdown:not(.ant-select-dropdown-hidden)')) return;
      event.preventDefault(); onClose();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [modal, narrow, onClose]);
  const content = <PreviewContent key={target.key} target={target} close={onClose} onVersions={onVersions} />;
  return modal || narrow ? <Modal open onCancel={onClose} footer={null} closable={false} width={narrow ? '100vw' : 'min(1000px, 90vw)'}
    className={`artifact-preview-modal${narrow ? ' artifact-preview-modal--narrow' : ''}`} title={null} aria-label={t('chat.previewNamedFile', { name: target.filename })}>
    {content}
  </Modal> : <aside className="artifact-preview-pane">{content}</aside>;
}
