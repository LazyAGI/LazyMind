import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { Modal } from 'antd';
import { ExclamationCircleOutlined, ExportOutlined, LockOutlined, ReloadOutlined } from '@ant-design/icons';
import type { DocumentProvider, DocumentPublishRequest, DocumentNumberingResult, DocumentConvertResult } from '@/api/generated/core-client';
import { useWorkflowStore, type SlotRevision } from '@/modules/chat/store/workflowPanel';
import { WorkflowSessionApi, type RewriteSelectionPreview, type WriterNumberingState, type WriterNumberingUpdate } from '@/modules/chat/utils/request';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlFromMap } from '@/modules/knowledge/utils/imageUrl';
import { getCloudDocumentsUrl } from '@/modules/modelProvider/utils/cloudDocumentUrls';
import i18n from '@/i18n';
import { MarkdownArtifactEditor, type MarkdownSaveMode } from './MarkdownArtifactEditor';
import { WriterIRControl, type WriterIRSaveMode } from './WriterIRControl';
import { isWriterDocument, normalizeWriterDocumentForSync, type WriterDocument } from './writerIR';
import { SlotEditingContext, WorkflowPanelTabActiveContext } from './slotEditingContext';
import { ArtifactRewriteDialog, type ArtifactRewriteSelection } from './ArtifactRewriteDialog';
import { useDocumentCopy } from './useDocumentCopy';
import { downloadSource, writerDownloadFilename, writerDownloadCacheKey } from './WriterDownloadFormat';
import { useWriterProviderAvailability } from './useWriterProviderAvailability';
import { documentRewritePreview } from './documentRewritePreview';
import { documentPublicationErrorMessage } from './documentPublicationError';
import { DocumentPublicationRecoveryPanel } from './DocumentPublicationRecoveryPanel';
import { documentPublicationTargetUrl } from './documentPublicationUrl';
import { WriterProviderIcon } from './DocumentProviderChoice';
import type { SlotVersionPopover } from './SlotComponents';

function unwrap(value: unknown): unknown {
  while (value && typeof value === 'object') {
    const object = value as Record<string, unknown>;
    if ('data' in object) value = object.data;
    else if ('text' in object) value = object.text;
    else break;
  }
  return value;
}
function key() { return crypto.randomUUID(); }
function responseCode(error: unknown): string | undefined {
  return (error as { response?: { data?: { data?: { code?: string } } } })?.response?.data?.data?.code;
}
type Baseline = { id: string; revision: number; draft?: number; value: unknown };

async function readDocumentValue(value: unknown, representation: string): Promise<unknown> {
  const content = unwrap(value);
  const carrier = content as { path?: string; url?: string } | null;
  const url = carrier && typeof carrier === 'object' ? carrier.url || carrier.path : undefined;
  if (!url) return content;
  const response = await fetch(resolveCoreAssetUrl(url), { credentials: 'same-origin' });
  if (!response.ok) throw new Error('document read failed');
  const text = await response.text();
  return representation === 'ir' ? unwrap(JSON.parse(text)) : text;
}

export function DocumentArtifactEditor({ slot: incomingSlot, sessionId, readOnly, onRefresh, revisionCount, VersionHistory }: {
  slot: SlotRevision; sessionId: string; readOnly?: boolean; onRefresh?: () => void;
  revisionCount?: number; VersionHistory?: typeof SlotVersionPopover;
}) {
  const parentSignature = JSON.stringify([incomingSlot.artifact_id, incomingSlot.revision, incomingSlot.draft_version]);
  const parentSignatureRef = useRef(parentSignature);
  parentSignatureRef.current = parentSignature;
  const [restoredSelection, setRestoredSelection] = useState<{ superseded: string[]; slot: SlotRevision }>();
  const rollbackPrevious = useRef<string[]>([]);
  const slot = restoredSelection?.superseded.includes(parentSignature) ? restoredSelection.slot : incomingSlot;
  useEffect(() => {
    const selected = restoredSelection?.slot;
    if (selected && parentSignature === JSON.stringify([selected.artifact_id, selected.revision, selected.draft_version])) {
      setRestoredSelection(undefined);
    }
  }, [parentSignature, restoredSelection]);
  const [restoring, setRestoring] = useState(false);
  const descriptor = slot.document!;
  const writable = !readOnly && !restoring && descriptor.editable && descriptor.capabilities.includes('save');
  const active = useContext(WorkflowPanelTabActiveContext);
  const editingContext = useContext(SlotEditingContext);
  const { registerFooterAction, getSnapshot } = editingContext;
  const editingKey = `document:${sessionId}:${slot.slot_id}:${slot.list_index ?? -1}`;
  const flushEditor = useRef<() => Promise<boolean>>();
  const editorContext = useMemo(() => ({ ...editingContext,
    registerFlush: (key: string, flush: () => Promise<boolean>) => {
      if (key === editingKey) flushEditor.current = flush;
      const unregister = editingContext.registerFlush(key, flush);
      return () => {
        if (flushEditor.current === flush) flushEditor.current = undefined;
        unregister();
      };
    },
  }), [editingContext, editingKey]);
  const initial: Baseline = { id: slot.artifact_id!, revision: slot.revision, draft: slot.draft_version, value: unwrap(slot.artifact_value) };
  const latest = useRef(initial);
  const external = useRef(initial);
  const dirty = useRef(false);
  const seen = useRef('');
  const retainedPublicationSource = useRef<unknown>();
  const incomingValue = useRef(initial.value);
  incomingValue.current = initial.value;
  const carrier = initial.value as { path?: string; url?: string } | null;
  const sourceUrl = carrier && typeof carrier === 'object' ? carrier.url || carrier.path : undefined;
  const [value, setValue] = useState<unknown>(initial.value);
  const representation = typeof value === 'string' ? 'markdown' : isWriterDocument(value) ? 'ir' : descriptor.representation;
  const [version, setVersion] = useState(initial.revision);
  const [loaded, setLoaded] = useState(false);
  const [providers, setProviders] = useState<DocumentProvider[]>([]);
  const [publishingProvider, setPublishingProvider] = useState<string | null>(null);
  const busy = publishingProvider !== null;
  const [error, setError] = useState('');
  const [errorAction, setErrorAction] = useState<'providers' | 'download' | 'history' | null>(null);
  const retryHistory = useRef<() => void>();
  const historyLoadPending = useRef(false);
  const [publicationBlocked,setPublicationBlocked]=useState(true);
  const [publicationRefresh,setPublicationRefresh]=useState(0);
  const publicationAvailable=useCallback((allowed:boolean)=>setPublicationBlocked(!allowed),[]);
  const publicationResolved=useCallback(()=>{attempt.current=undefined;setError('');onRefresh?.();},[onRefresh]);
  const draftContent = useRef(initial.value);
  const [numberingView, setNumberingView] = useState<{ source: unknown; id: string; revision: number; draft?: number; result: DocumentNumberingResult }>();
  const currentNumbering = numberingView && numberingView.source === value && numberingView.id === latest.current.id
    && numberingView.revision === latest.current.revision && numberingView.draft === latest.current.draft
    ? numberingView.result : undefined;
  const numbering = currentNumbering?.numbering as WriterNumberingState | undefined;
  const [selection, setSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [preview, setPreview] = useState<{ id: string; selection: ArtifactRewriteSelection; value: RewriteSelectionPreview } | null>(null);
  const [downloading, setDownloading] = useState(false);
  const downloadPending = useRef(false);
  const retryDownload = useRef<() => void>();
  const publishPending = useRef(false);
  const [publicationStatus, setPublicationStatus] = useState('');
  const [publicationUrl, setPublicationUrl] = useState(() => documentPublicationTargetUrl({ uri: slot.write_back_url, doc_id: slot.provider_document_id }, slot.provider));
  const publicationSucceeded = useCallback((url: string | undefined, provider?: string) => {
    setPublicationUrl(url);
    setPublicationStatus(current => provider === 'wechat' && !url
      ? String(i18n.t('chat.writerIR.wechatDraftLinkUnavailable'))
      : current === String(i18n.t('chat.writerLocal.publishedWithEdits')) ? current : String(i18n.t('chat.writerIR.writeBackSuccess')));
  }, []);
  const [authorizationNeeded, setAuthorizationNeeded] = useState('');
  const [providerRefresh, setProviderRefresh] = useState(0);
  const availability = useWriterProviderAvailability(providers.map(provider => provider.id));
  const previewSource = useRef<Baseline>();
  const attempt = useRef<{ fingerprint: string; body: DocumentPublishRequest; id: string; uncertain: boolean }>();

  const writerSlot = (['source_document', 'outline_document', 'flat_draft_document', 'draft_document'] as const).find(id => id === slot.slot_id);
  const mediaRevision = useWorkflowStore(state => {
    const session = Object.values(state.sessionByConversation).find(item => item?.session_id === sessionId);
    if (session?.workflow_id !== 'writer-workflow') return undefined;
    return JSON.stringify((session.slots ?? [])
      .filter(item => item.selected && ['media_assets', 'resolved_media_assets', 'flat_resolved_media_assets', 'target_document'].includes(item.slot_id))
      .map(item => [item.slot_id, item.artifact_id, item.revision, item.draft_version]));
  });
  const mediaKey = JSON.stringify([sessionId, slot.artifact_id, slot.revision, slot.draft_version, writerSlot, mediaRevision]);
  const [media, setMedia] = useState<{ key: string; urls?: Record<string, string> }>();
  const mediaUrls = media?.key === mediaKey || retainedPublicationSource.current !== undefined ? media?.urls : undefined;
  const latestMediaUrls = useRef(mediaUrls);
  latestMediaUrls.current = mediaUrls;
  const resolveImageUrl = useCallback(async (url: string) => {
    const resolved = await resolveMarkdownImageUrlFromMap(url, mediaUrls);
    // MDXEditor does not cancel older preview lookups when its handler changes.
    return latestMediaUrls.current === mediaUrls ? resolved : resolveMarkdownImageUrlFromMap(url, latestMediaUrls.current);
  }, [mediaUrls]);

  useEffect(() => {
    if (descriptor.representation !== 'markdown' || !writerSlot || mediaRevision === undefined || (slot.list_index ?? -1) >= 0) return;
    const controller = new AbortController();
    // Resolve preview resources only; the editable source keeps its original image references.
    void WorkflowSessionApi().renderWriterDocument(sessionId, writerSlot, {
      signal: controller.signal, silentError: true,
    } as never).then(response => {
      if (!controller.signal.aborted) setMedia({ key: mediaKey, urls: response.data.code === 0 ? response.data.data.media_urls : undefined });
    }).catch(() => {
      if (!controller.signal.aborted) setMedia({ key: mediaKey });
    });
    return () => controller.abort();
  }, [descriptor.representation, writerSlot, mediaRevision, slot.list_index, sessionId, mediaKey]);

  useEffect(() => { setPublicationUrl(documentPublicationTargetUrl({ uri: slot.write_back_url, doc_id: slot.provider_document_id }, slot.provider)); }, [slot.write_back_url, slot.provider, slot.provider_document_id]);

  useEffect(() => {
    const signature = JSON.stringify([slot.artifact_id, slot.revision, slot.draft_version]);
    if (seen.current === signature) return;
    let canceled = false;
    async function load() {
      let content = incomingValue.current;
      if (sourceUrl) {
        content = await readDocumentValue(content, descriptor.representation);
      }
      if (canceled) return;
      seen.current = signature;
      const next = { id: slot.artifact_id!, revision: slot.revision, draft: slot.draft_version, value: content };
      // A delayed parent refresh must not undo a save or publication we accepted.
      if (next.revision < latest.current.revision || (next.revision === latest.current.revision && (next.draft ?? 0) < (latest.current.draft ?? 0))) return;
      external.current = next;
      if (!dirty.current) { latest.current = next; draftContent.current = content; }
      // A publication refresh must not replace the editor holding newer local edits.
      if (dirty.current && (retainedPublicationSource.current !== undefined || publishPending.current)
        && typeof content !== typeof draftContent.current) return;
      setValue(content); setVersion(next.revision); setLoaded(true);
    }
    void load().catch(() => { if (!canceled) setError(String(i18n.t('chat.writerIR.saveFailed'))); });
    return () => { canceled = true; };
  }, [slot.artifact_id, slot.revision, slot.draft_version, sourceUrl, descriptor.representation]);

  useEffect(() => {
    if (!descriptor.capabilities.includes('publish_document') || !writable) return;
    let canceled = false;
    void WorkflowSessionApi().listDocumentProviders().then((response) => {
      if (!canceled) setProviders(response.data.data.providers);
    }).catch(() => { if (!canceled) { setError(documentPublicationErrorMessage('DOCUMENT_PROVIDERS_UNAVAILABLE')); setErrorAction('providers'); } });
    return () => { canceled = true; };
  }, [descriptor.capabilities, writable, providerRefresh]);

  const accept = useCallback((id: string, revision: number, draft: number | undefined, content: unknown, submitted: unknown = draftContent.current) => {
    const newerEdits = JSON.stringify(draftContent.current) !== JSON.stringify(submitted);
    const next = { id, revision, draft, value: content };
    latest.current = next; external.current = next;
    if (!newerEdits) { dirty.current = false; draftContent.current = content; }
    retainedPublicationSource.current = newerEdits && typeof submitted !== typeof content ? submitted : undefined;
    setValue(retainedPublicationSource.current ?? content); setVersion(revision);
  }, []);
  const edit = useCallback((content: unknown) => {
    draftContent.current = content;
    dirty.current = JSON.stringify(content) !== JSON.stringify(retainedPublicationSource.current ?? external.current.value);
    if (!dirty.current) {
      latest.current = external.current;
      if (retainedPublicationSource.current !== undefined) {
        retainedPublicationSource.current = undefined;
        draftContent.current = external.current.value;
        setValue(external.current.value);
      }
    }
  }, []);
  const loadRestoredVersion = async (revision: number) => {
    if (historyLoadPending.current) return;
    historyLoadPending.current = true;
    try {
      const response = await WorkflowSessionApi().getSlots(sessionId);
      const selected = (response.data.data.slots as SlotRevision[]).find(item => item.selected
        && item.slot_id === slot.slot_id && (item.list_index ?? -1) === (slot.list_index ?? -1)
        && item.revision === revision);
      if (!selected?.artifact_id || !selected.document) throw new Error('selected document unavailable');
      const content = await readDocumentValue(selected.artifact_value, selected.document.representation);
      // Replace the save baseline as well as the visible text, before editing resumes.
      dirty.current = false;
      draftContent.current = content;
      retainedPublicationSource.current = undefined;
      accept(selected.artifact_id, selected.revision, selected.draft_version, content);
      seen.current = JSON.stringify([selected.artifact_id, selected.revision, selected.draft_version]);
      setRestoredSelection({ superseded: [...rollbackPrevious.current, parentSignatureRef.current], slot: selected });
      setPreview(null); setSelection(null); setError(''); setErrorAction(null);
      setRestoring(false);
      onRefresh?.();
    } catch (error) {
      setError(String(i18n.t('chat.slots.contentLoadFailed')));
      setErrorAction('history');
      retryHistory.current = () => { void loadRestoredVersion(revision).catch(() => {}); };
      throw error;
    } finally {
      historyLoadPending.current = false;
    }
  };
  const rollback = async (revision: number) => {
    if (!writable || busy) return false;
    if (flushEditor.current && !await flushEditor.current()) throw new Error('draft save failed');
    rollbackPrevious.current = [parentSignatureRef.current,
      JSON.stringify([latest.current.id, latest.current.revision, latest.current.draft])];
    setRestoring(true);
    try {
      await useWorkflowStore.getState().rollbackSlotItem(sessionId, slot.slot_id, slot.list_index ?? -1, revision);
    } catch (error) {
      setRestoring(false);
      throw error;
    }
    await loadRestoredVersion(revision);
    return true;
  };
  const save = useCallback(async (content: unknown, base: number, mode: MarkdownSaveMode | WriterIRSaveMode = 'checkpoint', numbering?: WriterNumberingUpdate) => {
    const current = latest.current;
    const ir = isWriterDocument(content);
    const artifact = ir
      ? { schema: 'application/vnd.lazymind.writer+json', data: normalizeWriterDocumentForSync(content) }
      : { text: content, ...(isWriterDocument(current.value) ? { schema: 'text/markdown' } : {}) };
    const response = await WorkflowSessionApi().saveDocumentArtifact(current.id, {
      base_revision: base, base_draft_version: current.draft, mode,
      numbering_update: numbering, content_type: ir ? 'json' : 'text/markdown', value: artifact, command_id: key(),
    }, { silentError: true } as never);
    const result = response.data.result;
    if (!response.data.ok || !result?.artifact_id || typeof result.revision !== 'number' || typeof result.draft_version !== 'number') throw new Error('invalid save response');
    accept(result.artifact_id, result.revision, result.draft_version, unwrap(result.value ?? artifact), content);
    attempt.current = undefined;
    onRefresh?.();
    return latest.current;
  }, [accept, onRefresh]);

  useEffect(() => {
    if (!loaded || !descriptor.capabilities.includes('numbering')) return;
    let canceled = false; const current = latest.current;
    void WorkflowSessionApi().previewDocumentAction(current.id, { action: 'numbering', base_revision: current.revision,
      base_draft_version: current.draft, input: {} }).then((response) => {
        if (!canceled && latest.current.id === current.id && latest.current.revision === current.revision
          && latest.current.draft === current.draft) {
          setNumberingView({ source: value, id: current.id, revision: current.revision, draft: current.draft,
            result: response.data.data as DocumentNumberingResult });
        }
      }).catch(() => {});
    return () => { canceled = true; };
  }, [loaded, version, value, slot.artifact_id, slot.draft_version, descriptor.capabilities]);
  useDocumentCopy({ enabled: loaded, editingKey, sessionId, slotId: slot.slot_id, listIndex: slot.list_index ?? -1,
    revision: latest.current.revision, draftVersion: latest.current.draft,
    document: value as string | WriterDocument, pendingReview: Boolean(preview) });
  const beginDownload = async (format: 'markdown' | 'latex' | 'text' | 'lmd', fixed?: { snapshot: unknown; current: Baseline }) => {
    if (downloadPending.current) return;
    downloadPending.current = true; setDownloading(true); setError(''); setErrorAction(null);
    const snapshot = fixed?.snapshot ?? getSnapshot?.(editingKey) ?? draftContent.current;
    const current = fixed?.current ?? { ...latest.current };
    retryDownload.current = () => void beginDownload(format, { snapshot, current });
    try {
      const title = isWriterDocument(snapshot) ? snapshot.title : '';
      if (format === 'lmd') {
        await downloadSource(isWriterDocument(snapshot)
          ? { filename: writerDownloadFilename(title, 'lmd'), content: JSON.stringify(snapshot, null, 2) }
          : { filename: writerDownloadFilename(title, 'lmd'), conversionSource: String(snapshot), conversionSourceFormat: 'markdown', cacheKey: writerDownloadCacheKey('document-lmd', String(snapshot)) }, 'lmd');
      } else {
        const response = await WorkflowSessionApi().previewDocumentAction(current.id, { action: 'convert_document',
          base_revision: current.revision, base_draft_version: current.draft,
          input: { output_format: format, document: snapshot as string | Record<string, unknown> } });
        const content = (response.data.data as DocumentConvertResult).content;
        if (typeof content !== 'string') throw new Error('invalid conversion response');
        await downloadSource({ filename: writerDownloadFilename(title, format === 'markdown' ? 'md' : format === 'latex' ? 'tex' : 'txt'), content }, format);
      }
    } catch { setError(String(i18n.t('chat.writer.downloadFormatFailed'))); setErrorAction('download'); }
    finally { downloadPending.current = false; setDownloading(false); }
  };
  const downloadRef = useRef(beginDownload); downloadRef.current = beginDownload;
  useEffect(() => {
    if (!active || !loaded || !descriptor.capabilities.includes('convert_document')) return;
    return registerFooterAction(`${editingKey}:download`, {
      label: String(i18n.t(downloading ? 'chat.writerLocal.preparing' : 'chat.slots.download')), icon: 'download', order: 25,
      menuLabel: String(i18n.t('chat.writerLocal.chooseDownload')),
      disabled: downloading, onClick: () => void downloadRef.current('markdown'),
      menu: (['markdown', 'text', 'latex', 'lmd'] as const).map(format => ({ key: format,
        label: String(i18n.t(format === 'lmd' ? 'chat.writerLocal.structure' : `chat.writerCopy.${format}`)), onClick: () => void downloadRef.current(format) })),
    });
  }, [active, loaded, descriptor.capabilities, registerFooterAction, editingKey, downloading]);
  const applyPreview = async () => {
    if (!preview?.value.commit?.token) return undefined;
    const baseline=previewSource.current;
    if (dirty.current || !baseline || external.current.id!==baseline.id || external.current.revision!==baseline.revision || external.current.draft!==baseline.draft) throw new Error('rewrite baseline changed');
    const response = await WorkflowSessionApi().executeDocumentAction(preview.id, { action: 'rewrite_selection',
      base_revision: preview.value.base_revision, base_draft_version: preview.value.base_draft_version,
      input: { commit_token: preview.value.commit.token } });
    const result = response.data.data;
    const fetched = await WorkflowSessionApi().getDocumentArtifact(result.artifact_id);
    const artifact = fetched.data.result;
    accept(artifact.artifact_id, artifact.revision, artifact.draft_version, unwrap(artifact.value));
    setPreview(null); onRefresh?.(); return artifact.revision;
  };
  const canRewrite = writable && descriptor.capabilities.includes('rewrite_selection') && !selection && !preview;

  const publish = useCallback(async (provider: string) => {
    if (publishPending.current || busy || publicationBlocked) return;
    const current = latest.current;
    const fingerprint = JSON.stringify([current.id, current.revision, current.draft, provider]);
    if (attempt.current?.uncertain && attempt.current.fingerprint !== fingerprint) {
      setError(documentPublicationErrorMessage('PUBLICATION_OUTCOME_UNKNOWN', provider)); return;
    }
    if (!attempt.current || attempt.current.fingerprint !== fingerprint) {
      attempt.current = { fingerprint, id: current.id, uncertain: false, body: { action: 'publish_document',
        base_revision: current.revision, base_draft_version: current.draft,
        input: { provider, mode: 'replace', idempotency_key: key() } } };
    }
    const request = attempt.current;
    publishPending.current = true; setPublishingProvider(provider); setError(''); setErrorAction(null); setPublicationStatus('');
    try {
      const response = await WorkflowSessionApi().publishDocument(request.id, request.body, { silentError: true } as never);
      const result = response.data.data;
      if (!result.provider_synced || !result.artifact_saved || !result.artifact_id) throw new Error('publication did not complete');
      accept(result.artifact_id, result.revision, result.draft_version, result.document, current.value);
      setPublicationUrl(documentPublicationTargetUrl(result.target_document, provider));
      setPublicationStatus(String(i18n.t(dirty.current ? 'chat.writerLocal.publishedWithEdits' : 'chat.writerIR.writeBackSuccess')));
      attempt.current = undefined; onRefresh?.();
    } catch (failure) {
      const code = responseCode(failure);
      const beforeWrite = ['DOCUMENT_ACTION_INVALID', 'REVISION_CONFLICT', 'DRAFT_VERSION_CONFLICT', 'ARTIFACT_IN_USE',
        'DOCUMENT_ACTION_UNSUPPORTED', 'DOCUMENT_PROVIDERS_UNAVAILABLE', 'PROVIDER_CREDENTIALS_UNAVAILABLE', 'DOCUMENT_CONVERSION_FAILED', 'PUBLICATION_RECOVERY_CLOSED'];
      if (code && beforeWrite.includes(code)) attempt.current = undefined;
      else request.uncertain = true;
      setError(documentPublicationErrorMessage(code, provider));
      if (code === 'PROVIDER_CREDENTIALS_UNAVAILABLE' && provider === 'feishu') {
        const states = await availability.refresh();
        if (states?.feishu === 'chat-disabled') {
          setAuthorizationNeeded(provider);
          setError(String(i18n.t('chat.writerLocal.feishuChatDisabled')));
          setErrorAction('providers');
        }
      }
    } finally { publishPending.current = false; setPublishingProvider(null);setPublicationRefresh(value=>value+1); }
  }, [accept, busy, publicationBlocked, onRefresh, availability.refresh]);
  const publishRef = useRef(publish); publishRef.current = publish;

  const providerLabel = (provider: string) => i18n.exists(`chat.writerIR.providers.${provider}`) ? String(i18n.t(`chat.writerIR.providers.${provider}`)) : provider;
  const chooseProvider = (provider: string) => {
    const state = availability.states[provider];
    if (state !== 'ready') {
      setAuthorizationNeeded(provider);
      setError(state === 'chat-disabled' ? String(i18n.t('chat.writerLocal.feishuChatDisabled')) : state === 'authorize' ? String(i18n.t('chat.writerLocal.authorization', { provider: providerLabel(provider) })) : String(i18n.t(state === 'checking' ? 'chat.writerLocal.checking' : 'chat.writerLocal.platformFailed')));
      setErrorAction('providers');
      return;
    }
    setAuthorizationNeeded(''); setError(''); setErrorAction(null);
    try { localStorage.setItem('writer-publish-provider', provider); } catch { /* Storage preferences are optional. */ }
    if (slot.provider === provider && (slot.provider_document_id || slot.write_back_ready)) {
      const safeTarget = documentPublicationTargetUrl({ uri: slot.write_back_url, doc_id: slot.provider_document_id }, slot.provider);
      Modal.confirm({ title: i18n.t('chat.writerLocal.update', { provider: providerLabel(provider) }),
        content: <div>{i18n.t('chat.writerLocal.updateConfirm')}{safeTarget && <p><a href={safeTarget} target='_blank' rel='noreferrer'>{safeTarget}</a></p>}</div>,
        onOk: () => publishRef.current(provider),
      });
    } else { void publishRef.current(provider); }
  };
  const chooseRef = useRef(chooseProvider); chooseRef.current = chooseProvider;
  useEffect(() => {
    if (authorizationNeeded && availability.states[authorizationNeeded] === 'ready') {
      setAuthorizationNeeded(''); setError(''); setErrorAction(null);
    }
  }, [authorizationNeeded, availability.states]);
  useEffect(() => {
    if (!active || !writable || !descriptor.capabilities.includes('publish_document')) return;
    let remembered: string | null = null;
    try { remembered = localStorage.getItem('writer-publish-provider'); } catch { /* Storage preferences are optional. */ }
    const preferred = providers.find(provider => provider.id === slot.provider)?.id ?? providers.find(provider => provider.id === remembered)?.id ?? providers[0]?.id;
    const authorizationStatus = (provider: string) => availability.states[provider] === 'ready' ? '' : String(i18n.t(availability.states[provider] === 'chat-disabled' ? 'chat.writerLocal.feishuChatDisabledShort' : availability.states[provider] === 'authorize' ? 'chat.writerLocal.authorizeShort' : availability.states[provider] === 'failed' ? 'chat.writerLocal.platformFailed' : 'chat.writerLocal.checking'));
    return registerFooterAction(`${editingKey}:publish`, {
      label: publishingProvider !== null ? String(i18n.t('chat.writerLocal.publishing', { provider: providerLabel(publishingProvider) }))
        : providers.length === 1 ? String(i18n.t(slot.provider === preferred && slot.write_back_ready ? 'chat.writerLocal.update' : 'chat.writerLocal.create', { provider: providerLabel(preferred) })) : String(i18n.t('chat.writerLocal.publishTo')),
      icon: 'write-back', tone: 'primary', order: 30,
      menuLabel: String(i18n.t('chat.writerLocal.chooseProvider')),
      disabled: busy || publicationBlocked || !loaded || providers.length === 0, flushBeforeAction: true, flushKey: editingKey,
      onClick: () => { if (preferred) chooseRef.current(preferred); },
      menu: providers.length > 1 ? providers.map(provider => ({ key: provider.id,
        label: [providerLabel(provider.id), authorizationStatus(provider.id)].filter(Boolean).join(' · '),
        icon: <span className='workflow-panel__provider-icon' aria-hidden='true'><WriterProviderIcon provider={provider.id} /></span>,
        onClick: () => chooseRef.current(provider.id) })) : undefined,
      statusText: error && authorizationNeeded ? undefined : error || publicationStatus || (providers.length === 1 && preferred ? authorizationStatus(preferred) : undefined), statusTone: error ? 'error' : 'success',
      statusLink: publicationUrl ? { href: publicationUrl, label: String(i18n.t('chat.writerIR.openCloudDocument')) } : undefined,
    });
  }, [active, writable, descriptor.capabilities, registerFooterAction, editingKey, loaded, providers, busy, publishingProvider, publicationBlocked, error, authorizationNeeded, publicationStatus, publicationUrl, slot.provider, slot.write_back_ready, availability.states]);

  if (!loaded) return <div role='status'>{error || '…'}</div>;
  const versionHistory = VersionHistory && sessionId ? <VersionHistory sessionId={sessionId} slotId={slot.slot_id}
    listIndex={slot.list_index ?? -1} revisionCount={revisionCount ?? version} currentRevision={version}
    currentVersionNumber={slot.version_number} currentValue={value} currentChangeSource={slot.change_source}
    readCurrentValue={() => getSnapshot?.(editingKey) ?? draftContent.current} contentType='text'
    readOnly={!writable || busy} triggerLabel={String(i18n.t('chat.slots.versionHistory'))} onRollback={rollback} /> : undefined;
  return <SlotEditingContext.Provider value={editorContext}><div className='workflow-slot workflow-slot--artifact'>
    <div className={`workflow-slot__artifact-body${representation === 'markdown' ? ' workflow-slot__artifact-body--markdown' : ''}`}>
    {representation === 'markdown' && typeof value === 'string'
      ? <MarkdownArtifactEditor renderContext={descriptor.render_context} markdown={value} sourceRevision={version} editingKey={editingKey} readOnly={!writable} savePaused={busy} allowMultipleParagraphs
        numberingDocument={typeof currentNumbering?.document === 'string' ? currentNumbering.document : undefined}
        resolveImageUrl={resolveImageUrl} onContentChange={edit} numbering={numbering} toolbarActions={versionHistory}
        onRewriteSelection={canRewrite ? (picked) => { if (picked.supported) setSelection({ type: 'markdown', selected_text: picked.text, selectedText: picked.text, paragraph: picked.paragraph, paragraphs: picked.paragraphSelections?.map(item => item.paragraph), startOffset: picked.startOffset, sourceRange: picked.sourceRange, sourceRanges: picked.sourceRanges, anchor: picked.anchor }); } : undefined}
        rewriteDialogOpen={selection !== null}
        rewritePreview={preview?.selection.paragraph ? { paragraph: preview.selection.paragraph, paragraphs: preview.selection.paragraphs, sourceMarkdown: String(previewSource.current?.value ?? value), startOffset: preview.selection.startOffset,
          sessionId, slotId: slot.slot_id, listIndex: slot.list_index ?? -1, preview: preview.value, applyPreview } : null}
        onRewritePreviewApplied={() => setPreview(null)} onRewritePreviewRejected={() => setPreview(null)}
        onSave={async (text, revision, mode, numbering) => { const saved = await save(text, revision, mode, numbering); return { markdown: String(saved.value), revision: saved.revision }; }} />
      : isWriterDocument(value) ? <WriterIRControl document={value as WriterDocument} sourceRevision={version} editingKey={editingKey} readOnly={!writable} savePaused={busy} allowMultipleParagraphs
        onDocumentChange={edit} numbering={numbering} toolbarActions={versionHistory}
        onRewriteSelection={canRewrite ? (picked) => setSelection({ type: 'ir', node_id: picked.nodeId, nodeSelections: picked.nodeSelections, selectedText: picked.selectedText, anchor: picked.anchor }) : undefined}
        rewriteDialogOpen={selection !== null}
        rewritePreview={preview?.selection.type === 'ir' ? { nodeId: preview.selection.node_id, sourceDocument: previewSource.current?.value as WriterDocument, sessionId,
          slotId: slot.slot_id, listIndex: slot.list_index ?? -1, preview: preview.value, applyPreview } : null}
        onRewritePreviewApplied={() => setPreview(null)} onRewritePreviewRejected={() => setPreview(null)} onSave={async (_source, revised, revision, mode, numbering) => {
          const saved = await save(revised, Number(revision ?? latest.current.revision), mode, numbering);
          return { document: saved.value as WriterDocument, sourceRevision: saved.revision, draftVersion: saved.draft };
        }} /> : <div role='alert'>{String(i18n.t('chat.writerIR.saveFailed'))}</div>}
    </div>
    {error && <div className={`workflow-document-notice${authorizationNeeded ? ' workflow-document-notice--authorization' : ''}`} role='alert'>
      <span className='workflow-document-notice__icon' aria-hidden='true'>{authorizationNeeded ? <LockOutlined /> : <ExclamationCircleOutlined />}</span>
      <span className='workflow-document-notice__message'>{error}</span>
      {(authorizationNeeded || errorAction) && <div className='workflow-document-notice__actions'>
        {authorizationNeeded && <a className='workflow-document-notice__action workflow-document-notice__action--settings' href={getCloudDocumentsUrl(authorizationNeeded === 'feishu' ? 'feishu' : undefined)} target='_blank' rel='noreferrer'>
          {String(i18n.t(authorizationNeeded === 'feishu' ? 'chat.writerLocal.feishuSettings' : 'chat.writerLocal.settings'))}<ExportOutlined aria-hidden='true' />
        </a>}
        {errorAction && <button className='workflow-document-notice__action' type='button' onClick={() => { if (errorAction === 'download') retryDownload.current?.(); else if (errorAction === 'history') retryHistory.current?.(); else { setError(''); setProviderRefresh(value => value + 1); void availability.refresh(); } }}>
          <ReloadOutlined aria-hidden='true' />{String(i18n.t('common.retry'))}
        </button>}
      </div>}
    </div>}
    {descriptor.capabilities.includes('publish_document') && <DocumentPublicationRecoveryPanel key={latest.current.id} artifactId={latest.current.id} slotId={slot.slot_id} itemIndex={slot.list_index ?? -1}
      refreshKey={publicationRefresh} publishing={busy} readOnly={readOnly} canApplyLocal={()=>!dirty.current} onAvailability={publicationAvailable} onResolved={publicationResolved} onPublished={writable ? publicationSucceeded : undefined} onTarget={writable ? setPublicationUrl : undefined} />}
    <ArtifactRewriteDialog open={selection !== null} sessionId={sessionId} slotId={slot.slot_id} listIndex={slot.list_index ?? -1}
      baseRevision={latest.current.revision} baseDraftVersion={latest.current.draft} selection={selection}
      onClose={() => setSelection(null)} onApplied={() => { setPreview(null); onRefresh?.(); }}
      requestPreview={async (instruction, picked) => {
        const current = latest.current; previewSource.current = { ...current };
        if (picked.type === 'ppt_html') throw new Error('unsupported document selection');
        const response = await WorkflowSessionApi().previewDocumentAction(current.id, { action: 'rewrite_selection',
          base_revision: current.revision, base_draft_version: current.draft,
          input: picked.type === 'ir' ? { instruction, type: 'ir', selection_ranges: picked.nodeSelections ?? [{ node_id: picked.node_id, ...(picked.selectedText ? { selected_text: picked.selectedText } : {}) }] } : { instruction, type: 'markdown', selection_ranges: picked.sourceRanges ?? [picked.sourceRange ?? { selected_text: picked.selected_text }] } });
        return documentRewritePreview(response.data.data, current.revision, current.draft);
      }} onPreviewReady={(result) => { if (selection && previewSource.current) setPreview({ id: previewSource.current.id, selection, value: result }); }} />

  </div></SlotEditingContext.Provider>;
}
