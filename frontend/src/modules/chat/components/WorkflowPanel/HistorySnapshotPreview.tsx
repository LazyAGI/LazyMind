import { Component, useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { downloadWorkflowFile } from '@/modules/chat/utils/workflowDownload';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl, type MarkdownImageResolver } from '@/modules/knowledge/utils/imageUrl';
import { DEFAULT_SLIDE_CANVAS, measureSlideCanvas } from './ppt/slideCanvas';
import { resolveSlideAssets } from './ppt/slideAssets';
import { extractHtmlFromArtifact, htmlForStaticPreview } from './ppt/exportHtmlToPptx';
import { ensureRasterPng, fingerprintText, loadRasterPng, rasterCacheKey } from './ppt/slideRasterCache';
import { parseSlideSpec, type SlideSpec } from './ppt/slideSchema';
import { SlideCanvas } from './ppt/SlotJsonSlide';
import MarkdownViewer from '../MarkdownViewer';
import { writerMarkdownForEditing } from './writerMarkdownAnchors';
import { WriterIRDocumentEditor } from './WriterIRDocumentEditor';
import { isWriterDocument, type WriterDocument } from './writerIR';

export type HistoryPreviewKind = 'slide' | 'writer';
type Preview = { html: string; thumbnail?: { key: string; source: string } } | { png: string } | { slide: SlideSpec } | { markdown: string } | { document: WriterDocument };
type Job = () => Promise<void>;

// Admit one history request at a time. Rows leaving the viewport can be removed
// before they fetch their source or join the shared export capture queue.
export function createHistoryQueue() {
  const pending: Job[] = [];
  let busy = false;
  async function next() {
    if (busy) return;
    const job = pending.shift();
    if (!job) return;
    busy = true;
    try { await job(); } finally { busy = false; void next(); }
  }
  return (job: Job, selected: boolean) => {
    if (selected) pending.unshift(job);
    else pending.push(job);
    queueMicrotask(next);
    return () => {
      const index = pending.indexOf(job);
      if (index >= 0) pending.splice(index, 1);
    };
  };
}

function payload(snapshot: unknown): unknown {
  if (!snapshot || typeof snapshot !== 'object') return snapshot;
  if ('text' in snapshot) return payload(snapshot.text);
  if ('data' in snapshot) return payload(snapshot.data);
  return snapshot;
}

async function readSource(snapshot: unknown, signal: AbortSignal): Promise<string> {
  const value = payload(snapshot);
  const file = typeof value === 'string' && /^(?:https?:\/\/|\/(?:api\/core\/|static-files\/|var\/))\S+$/.test(value)
    ? { path: value }
    : value as { path?: string; url?: string } | null;
  if (file && typeof file === 'object' && (file.path || file.url)) {
    const url = file.url && !isExpiredSignedUrl(file.url) ? resolveCoreAssetUrl(file.url)
      : await resolveMarkdownImageUrlAsync(file.path || file.url!);
    if (!url) throw new Error('Missing source URL');
    const response = await fetch(url, { signal });
    if (!response.ok) throw new Error('Unavailable snapshot');
    const text = await response.text();
    if (/<div id=["'](?:root|app)["']/.test(text.slice(0, 400)) && text.includes('/@vite/client')) throw new Error('Unavailable snapshot');
    return text;
  }
  if (value == null) throw new Error('Missing snapshot');
  return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
}

async function renderPreview(source: string, kind: HistoryPreviewKind, session: string, detail: boolean): Promise<Preview> {
  const html = extractHtmlFromArtifact(source);
  if (kind === 'slide') {
    if (html) {
      const key = rasterCacheKey(session, fingerprintText(html));
      // Script-driven charts need their previously rendered image. Historical
      // documents must never execute scripts in the application origin.
      if (/data-lazymind-echarts=|data-lazymind-theme=|echarts\.init|id=["']chart_/i.test(html)) {
        const png = await loadRasterPng(key);
        if (!png) throw new Error('No static chart snapshot');
        return { png };
      }
      const thumbnail = detail ? undefined : { key: key + ':history-thumb', source: html };
      if (thumbnail) {
        const png = await loadRasterPng(thumbnail.key, 0.2);
        if (png) return { png };
      }
      const doc = new DOMParser().parseFromString(htmlForStaticPreview(await resolveSlideAssets(html)), 'text/html');
      doc.querySelectorAll('script, meta[http-equiv], base, iframe, object, embed').forEach(el => el.remove());
      doc.body.inert = true;
      return { html: '<!doctype html>' + doc.documentElement.outerHTML, thumbnail };
    }
    const slide = parseSlideSpec(source);
    if (slide) return { slide };
  } else if (source.trim() && !html) {
    if (!/^\s*[{[]/.test(source)) return { markdown: writerMarkdownForEditing(source) };
    let value: unknown;
    try { value = payload(JSON.parse(source)); }
    catch { return { markdown: writerMarkdownForEditing(source) }; } // Markdown may start with a link.
    if (isWriterDocument(value)) return { document: value };
    if (typeof value === 'string') return { markdown: writerMarkdownForEditing(value) };
  }
  throw new Error('Unsupported snapshot');
}

class PreviewBoundary extends Component<{ children: ReactNode; fallback: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() { return this.state.failed ? this.props.fallback : this.props.children; }
}

function HistorySlideFrame({ html, label }: { html: string; label: string }) {
  const [size, setSize] = useState(DEFAULT_SLIDE_CANVAS);
  const [state, setState] = useState<'loading' | 'ready' | 'failed'>('loading');
  useEffect(() => {
    if (state !== 'loading') return;
    const timer = window.setTimeout(() => setState('failed'), 15000);
    return () => window.clearTimeout(timer);
  }, [state]);
  if (state === 'failed') throw new Error('Slide resources unavailable');
  return <svg viewBox={`0 0 ${size.width} ${size.height}`} className='history-preview__slide' role='img' aria-label={label}>
    <foreignObject width={size.width} height={size.height}>
      <iframe title={label} sandbox='allow-same-origin' tabIndex={-1} srcDoc={html}
        width={size.width} height={size.height} style={{ border: 0, pointerEvents: 'none' }}
        onLoad={e => {
          const doc = e.currentTarget.contentDocument;
          if (!doc || doc.URL !== 'about:srcdoc') return;
          setSize(measureSlideCanvas(doc));
          doc.body.style.margin = '0';
          doc.body.style.overflow = 'hidden';
          Promise.all(Array.from(doc.images, image => image.decode())).then(
            () => setState('ready'), () => setState('failed'),
          );
        }} />
    </foreignObject>
  </svg>;
}

function PreviewContent({ preview, detail, label, resolveImageUrl }: { preview: Preview; detail: boolean; label: string; resolveImageUrl?: MarkdownImageResolver }) {
  if ('html' in preview) return <HistorySlideFrame html={preview.html} label={label} />;
  if ('png' in preview) return <img className='history-preview__slide' src={preview.png} alt={label} />;
  if ('slide' in preview) return <svg viewBox='0 0 1600 900' role='img' aria-label={label} className='history-preview__slide'>
    <foreignObject width='1600' height='900'><SlideCanvas spec={preview.slide} /></foreignObject>
  </svg>;
  if (!detail) {
    const lines = 'markdown' in preview ? preview.markdown.split(/\r?\n/).map(s => s.trim()).filter(Boolean)
      : [preview.document.title, ...preview.document.blocks.map(b => b.content).filter(Boolean)];
    return <div className='history-preview__excerpt'><strong>{lines[0]?.replace(/^#+\s*/, '').slice(0, 100)}</strong>
      <span>{lines[1]?.slice(0, 120)}</span></div>;
  }
  if ('markdown' in preview) return <MarkdownViewer resolveImageUrl={resolveImageUrl ?? resolveMarkdownImageUrlAsync}>{preview.markdown}</MarkdownViewer>;
  return <WriterIRDocumentEditor document={preview.document} ariaLabel={label} disabled
    onChange={() => {}} onFocus={() => {}} onBlur={() => {}} />;
}

export function HistorySnapshotPreview({ kind, snapshot, session, label, detail = false, selected = false, schedule, allowDownload, resolveImageUrl }: {
  kind: HistoryPreviewKind; snapshot: unknown; session: string; label: string; detail?: boolean; selected?: boolean;
  schedule: ReturnType<typeof createHistoryQueue>; allowDownload: boolean; resolveImageUrl?: MarkdownImageResolver;
}) {
  const { t } = useTranslation();
  const root = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(detail || typeof IntersectionObserver === 'undefined');
  const [preview, setPreview] = useState<Preview>();
  const [failed, setFailed] = useState(false);
  const [source, setSource] = useState<string>();
  const [showSource, setShowSource] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [downloadFailed, setDownloadFailed] = useState(false);
  useEffect(() => {
    if (detail || !root.current || typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(([entry]) => setVisible(entry.isIntersecting));
    observer.observe(root.current);
    return () => observer.disconnect();
  }, [detail]);
  const active = visible || selected;
  const prioritizeWriter = kind === 'writer' && selected;
  useEffect(() => {
    setSource(undefined); setPreview(undefined); setFailed(false); setShowSource(false); setDownloadFailed(false);
    if (!active) return;
    let cancelQueued: (() => void) | undefined;
    const controller = new AbortController();
    const load = async () => {
      try {
        const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]);
        const text = await readSource(snapshot, signal);
        signal.throwIfAborted();
        if (detail) setSource(text);
        const value = await renderPreview(text, kind, session, detail);
        if (controller.signal.aborted) return;
        setPreview(value);
        if ('html' in value && value.thumbnail) {
          const { key, source } = value.thumbnail;
          // Keep the scaled, read-only slide visible while the small PNG waits
          // behind filmstrip captures. A failed capture can keep this preview.
          cancelQueued = schedule(async () => {
            try {
              const png = await ensureRasterPng(key, source, { pixelRatio: 0.2, waitMs: 0, staticOnly: true });
              if (!controller.signal.aborted) setPreview({ png });
            } catch { /* The read-only preview is already available. */ }
          }, false);
        }
      } catch {
        if (!controller.signal.aborted) setFailed(true);
      }
    };
    if (detail || kind === 'slide') void load();
    else cancelQueued = schedule(load, prioritizeWriter);
    return () => { controller.abort(); cancelQueued?.(); };
  }, [active, prioritizeWriter, detail, snapshot, kind, session, schedule, attempt]);
  const fallback = <div role={detail ? 'status' : undefined}>{t('chat.slots.historyPreviewUnavailable')}
    {detail && source === undefined && (snapshot == null
      ? <p>{t('chat.slots.historyMissingSnapshot')}</p>
      : <button type='button' onClick={() => setAttempt(n => n + 1)}>{t('common.retry')}</button>)}
    {detail && source !== undefined && <div className='history-preview__actions'>
      <button type='button' onClick={() => setShowSource(!showSource)}>{t('chat.slots.historyViewSource')}</button>
      {allowDownload && <button type='button' onClick={async () => {
        const ext = /^\s*[[{]/.test(source) ? 'json' : kind === 'slide' ? 'html' : 'md';
        try { await downloadWorkflowFile({ blob: new Blob([source], { type: 'text/plain;charset=utf-8' }), filename: label + '.' + ext }); }
        catch { setDownloadFailed(true); }
      }}>{t('chat.slots.historyDownloadSource')}</button>}
      {downloadFailed && <span role='alert'>{t('chat.slots.versionActionFailed')}</span>}
    </div>}
    {detail && showSource && <pre>{source}</pre>}
  </div>;
  return <div ref={root} className={`history-preview history-preview--${detail ? 'detail' : 'thumb'} history-preview--${kind}`}>
    {failed ? fallback : preview ? <PreviewBoundary key={'png' in preview ? 'png' : 'content'} fallback={fallback}><PreviewContent preview={preview} detail={detail} label={label} resolveImageUrl={resolveImageUrl} /></PreviewBoundary>
      : <span className='history-preview__placeholder'>{t('common.loading')}</span>}
  </div>;
}
