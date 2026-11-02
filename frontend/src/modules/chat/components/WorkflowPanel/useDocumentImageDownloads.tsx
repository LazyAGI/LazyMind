import { useCallback, useEffect, useRef, useState, type MouseEvent } from 'react';
import { Alert, Button, Modal, Progress, Spin } from 'antd';
import { DownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { DocumentImageExportRequest, DocumentImageExportState } from '@/api/generated/core-client';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import { downloadStream } from '@/modules/chat/utils/download';
import { documentImages, type DocumentImageReference } from './documentImages';
import type { SlotFooterAction } from './slotEditingContext';
import './DocumentImageDownloads.scss';

export interface DocumentImageDownloadSource { artifactId: string; revision: number; draft?: number; document: unknown }
type CapturedSource = { artifactId: string; body: DocumentImageExportRequest };

function GalleryImage({ image, resolve, download, busy }: { image: DocumentImageReference; resolve: (source: string) => Promise<string>; download: () => void; busy: boolean }) {
  const [url, setUrl] = useState('');
  const [failed, setFailed] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const { t } = useTranslation();
  useEffect(() => {
    let current = true; setUrl(''); setFailed(false);
    void resolve(image.preview || image.source).then(value => {
      if (current) { if (/^(https?:|data:image\/|\/)/i.test(value)) setUrl(value); else setFailed(true); }
    }).catch(() => { if (current) setFailed(true); });
    return () => { current = false; };
  }, [image.source, image.preview, resolve]);
  return failed ? <span role='status'>{t('chat.writerImages.imageUnavailable')}</span>
    : url ? <>
      <button className='document-image-downloads__preview' type='button' aria-label={t('chat.writerImages.preview', { name: image.alt })} onClick={() => setExpanded(true)}>
        <img src={url} alt={image.alt} onError={() => setFailed(true)} />
      </button>
      <Modal title={image.alt || t('chat.writerImages.title')} open={expanded} onCancel={() => setExpanded(false)} width={960}
        footer={<Button icon={<DownloadOutlined aria-hidden />} loading={busy} disabled={!image.source} onClick={download}>{t('chat.writerImages.downloadOne')}</Button>}>
        <img className='document-image-downloads__original' src={url} alt={image.alt} />
      </Modal>
    </> : <Spin size='small' />;
}

async function imageErrorCode(error: unknown): Promise<string> {
  let data = (error as { response?: { data?: unknown } })?.response?.data;
  if (data instanceof Blob) {
    try { data = JSON.parse(await data.text()); } catch { return ''; }
  }
  return (data as { data?: { code?: string } } | undefined)?.data?.code ?? '';
}

export function useDocumentImageDownloads({ scopeKey, readSource, resolveImage }: {
  scopeKey: string;
  readSource: () => DocumentImageDownloadSource;
  resolveImage: (source: string) => Promise<string>;
}) {
  const { t } = useTranslation();
  const sourceReader = useRef(readSource); sourceReader.current = readSource;
  const controller = useRef<AbortController>();
  const captured = useRef<CapturedSource>();
  const pending = useRef(false);
  const lastJob = useRef<DocumentImageExportState>();
  const [open, setOpen] = useState(false);
  const [gallery, setGallery] = useState<DocumentImageReference[]>([]);
  const gallerySource = useRef<DocumentImageDownloadSource>();
  const [job, setJob] = useState<DocumentImageExportState>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    controller.current?.abort(); captured.current = undefined; lastJob.current = undefined; pending.current = false;
    setOpen(false); setJob(undefined); setGallery([]); setBusy(false); setError('');
    return () => { controller.current?.abort(); };
  }, [scopeKey]);

  const errorMessage = (code: string) => {
    if (code === 'IMAGE_EXPORT_EMPTY') return t('chat.writerImages.empty');
    if (['REVISION_CONFLICT', 'DRAFT_VERSION_CONFLICT'].includes(code)) return t('chat.writerImages.conflict');
    if (code === 'IMAGE_EXPORT_EXPIRED') return t('chat.writerImages.expired');
    if (code === 'IMAGE_EXPORT_BUSY') return t('chat.writerImages.busy');
    return t('chat.writerImages.failed');
  };
  const fileDownload = async (state: DocumentImageExportState, signal: AbortSignal) => {
    const source = captured.current;
    if (!source) return;
    const fileId = state.single ? state.items?.find(item => item.status === 'ready')?.id : 'bundle';
    if (!fileId) return;
    const response = await WorkflowSessionApi().downloadDocumentImages(source.artifactId, state.id!, fileId, { signal });
    if (!signal.aborted) {
      downloadStream(response.data, state.filename || 'document-images.zip');
      if (state.status === 'ready' && gallery.length === 0) setOpen(false);
    }
  };
  const waitForResult = async (initial: DocumentImageExportState, signal: AbortSignal) => {
    let state = initial;
    const deadline = Date.now() + 3 * 60_000;
    while (!signal.aborted) {
      lastJob.current = state; setJob(state);
      if (state.status === 'ready') { await fileDownload(state, signal); return; }
      if (!['pending', 'running'].includes(state.status ?? '')) { setOpen(true); return; }
      if (Date.now() >= deadline) throw new Error('polling paused');
      await new Promise<void>(resolve => {
        const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve(); };
        const timer = setTimeout(finish, 1000);
        signal.addEventListener('abort', finish, { once: true });
      });
      if (signal.aborted) return;
      const response = await WorkflowSessionApi().readDocumentImageExport(captured.current!.artifactId, state.id!, { signal });
      if (signal.aborted) return;
      state = response.data.data;
    }
  };
  const run = async (work: (signal: AbortSignal) => Promise<void>) => {
    if (pending.current) return;
    pending.current = true; controller.current?.abort();
    const current = new AbortController(); controller.current = current;
    setBusy(true); setError('');
    try { await work(current.signal); }
    catch (failure) {
      const code = await imageErrorCode(failure);
      if (!current.signal.aborted) {
        if (code === 'IMAGE_EXPORT_EXPIRED' && lastJob.current) {
          lastJob.current = { ...lastJob.current, status: 'expired' };
          setJob(lastJob.current);
        }
        setError(errorMessage(code)); setOpen(true);
      }
    }
    finally { if (controller.current === current) { pending.current = false; setBusy(false); } }
  };
  const start = async (image?: string, fixed?: DocumentImageDownloadSource) => {
    if (pending.current) return;
    const source = fixed ?? sourceReader.current();
    // Freeze the editor value before asynchronous saves, edits or refreshes.
    captured.current = { artifactId: source.artifactId, body: {
      base_revision: source.revision, base_draft_version: source.draft,
      document: JSON.parse(JSON.stringify(source.document)), image_reference: image,
    } };
    lastJob.current = undefined; setJob(undefined);
    if (!image) { setGallery([]); setOpen(false); }
    return run(async signal => {
      if (image && image.length > 16384) {
        const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(image));
        if (signal.aborted) return;
        captured.current!.body.image_reference = `sha256:${Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')}`;
      }
      const response = await WorkflowSessionApi().createDocumentImageExport(captured.current!.artifactId, captured.current!.body, { signal });
      if (!signal.aborted) await waitForResult(response.data.data, signal);
    });
  };
  const retry = () => void run(async signal => {
    const source = captured.current; if (!source) return;
    const previous = lastJob.current;
    const response = !previous?.id
      ? await WorkflowSessionApi().createDocumentImageExport(source.artifactId, source.body, { signal })
      : ['pending', 'running'].includes(previous.status ?? '')
        ? await WorkflowSessionApi().readDocumentImageExport(source.artifactId, previous.id, { signal })
        : await WorkflowSessionApi().retryDocumentImageExport(source.artifactId, previous.id, { signal });
    if (!signal.aborted) await waitForResult(response.data.data, signal);
  });
  const showGallery = useCallback(() => {
    if (pending.current) { setOpen(true); return; }
    const source = sourceReader.current();
    captured.current = undefined; lastJob.current = undefined;
    setJob(undefined); setError('');
    gallerySource.current = { ...source, document: JSON.parse(JSON.stringify(source.document)) };
    setGallery(documentImages(source.document)); setOpen(true);
  }, []);
  const onImageClick = useCallback((event: MouseEvent<HTMLElement>) => {
    if (!(event.target instanceof HTMLImageElement)) return;
    event.preventDefault(); showGallery();
  }, [showGallery]);
  const menu: NonNullable<SlotFooterAction['menu']> = [
    { key: 'images', label: t('chat.writerImages.downloadAll'), icon: <DownloadOutlined aria-hidden />, disabled: busy, onClick: () => { void start(); } },
  ];
  const failedItems = job?.items?.filter(item => item.status === 'failed') ?? [];
  const terminalFailure = job?.status === 'failed' || job?.status === 'partial' || job?.status === 'expired';
  const dialog = <Modal title={t('chat.writerImages.title')} open={open} onCancel={() => setOpen(false)} footer={null} width={600} destroyOnHidden>
    <div className='document-image-downloads'>
      {busy && <div role='status' aria-live='polite'>
        {t('chat.writerImages.preparing', { current: job?.completed ?? 0, total: job?.total ?? '…' })}
        <Progress percent={job?.total ? Math.round((job.completed ?? 0) / job.total * 100) : 0} size='small' />
      </div>}
      {(error || terminalFailure) && <Alert type='warning' showIcon message={error || (job?.status === 'expired' ? t('chat.writerImages.expired') : t('chat.writerImages.partial', { ready: job?.ready ?? 0, total: job?.total ?? 0 }))}
        description={failedItems.length > 0 && <ul>{failedItems.map(item => <li key={item.id}>{item.filename} — {t(`chat.writerImages.errors.${item.error_code ?? 'IMAGE_UNAVAILABLE'}`, { defaultValue: t('chat.writerImages.imageUnavailable') })}</li>)}</ul>} />}
      {gallery.length > 0 && <div className='document-image-downloads__gallery'>
        {gallery.map((image, index) => <figure key={`${image.source}:${index}`}>
          <div className='document-image-downloads__image'><GalleryImage image={image} resolve={resolveImage} busy={busy} download={() => start(image.source, gallerySource.current)} /></div>
          <figcaption><span>{image.alt || t('chat.writerImages.image', { index: index + 1 })}</span>
            <Button type='link' size='small' icon={<DownloadOutlined aria-hidden />} disabled={busy || !image.source} onClick={() => start(image.source, gallerySource.current)}>{t('chat.writerImages.downloadOne')}</Button>
          </figcaption>
        </figure>)}
      </div>}
      {(error || terminalFailure) && <div className='document-image-downloads__actions'>
        {(error || terminalFailure) && job?.status !== 'expired' && <Button icon={<ReloadOutlined aria-hidden />} disabled={busy} onClick={retry}>{t('chat.writerImages.retry')}</Button>}
        {!!job?.ready && ['ready', 'partial'].includes(job.status ?? '') && <Button icon={<DownloadOutlined aria-hidden />} disabled={busy} onClick={() => void run(signal => fileDownload(job, signal))}>
          {job.status === 'partial' ? t('chat.writerImages.downloadReady', { count: job.ready }) : t('chat.writerImages.downloadAgain')}
        </Button>}
        {(terminalFailure || error) && <Button disabled={busy} onClick={() => start()}>{t('chat.writerImages.downloadCurrent')}</Button>}
      </div>}
    </div>
  </Modal>;
  return { menu, dialog, busy, onImageClick, showGallery, downloadImage: (image: string) => start(image) };
}
