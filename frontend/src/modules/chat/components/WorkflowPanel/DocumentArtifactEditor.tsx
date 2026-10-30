import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { Modal } from 'antd';
import { ExclamationCircleOutlined, ExportOutlined, ReloadOutlined } from '@ant-design/icons';
import type { DocumentProvider, DocumentNumberingResult, DocumentConvertResult } from '@/api/generated/core-client';
import { useWorkflowStore, type SlotRevision } from '@/modules/chat/store/workflowPanel';
import { WorkflowSessionApi, type RewriteSelectionPreview, type WriterNumberingState, type WriterNumberingUpdate } from '@/modules/chat/utils/request';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlFromMap } from '@/modules/knowledge/utils/imageUrl';
import { CLOUD_DOCUMENTS_OBSIDIAN_SETUP_PATH, getCloudDocumentsUrl } from '@/modules/modelProvider/utils/cloudDocumentUrls';
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
import { DocumentPublicationDialog, type PublicationProviderChoices } from './DocumentPublicationDialog';
import { DocumentPublicationRecoveryPanel } from './DocumentPublicationRecoveryPanel';
import { documentPublicationTargetUrl } from './documentPublicationUrl';
import { WriterProviderIcon } from './DocumentProviderChoice';
import { pendingImageGroups, keepVerticalImageGroups, imageGroupKey } from './writerImageGroups';
import type { SlotVersionPopover } from './SlotComponents';
import { documentImages } from './documentImages';
import { useDocumentImageDownloads } from './useDocumentImageDownloads';

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
function rewriteConflict(code: 'DRAFT_VERSION_CONFLICT' | 'SELECTION_STALE'): Error & { code: string } {
  return Object.assign(new Error(code), { code });
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
  const [hasLocalChanges, setHasLocalChanges] = useState(false);
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
  const [hasImages, setHasImages] = useState(false);
  useEffect(() => { setHasImages(documentImages(value).length > 0); }, [value]);
  const [providers, setProviders] = useState<DocumentProvider[]>([]);
  const [publishDialog, setPublishDialog] = useState<{ provider?: string }>();
  const [publicationChoices, setPublicationChoices] = useState<PublicationProviderChoices>({});
  const [error, setError] = useState('');
  const [errorAction, setErrorAction] = useState<'providers' | 'download' | 'history' | null>(null);
  const retryHistory = useRef<() => void>();
  const historyLoadPending = useRef(false);
  const [publicationBlocked,setPublicationBlocked]=useState(true);
  const publicationAvailable=useCallback((allowed:boolean)=>setPublicationBlocked(!allowed),[]);
  const publicationResolved=useCallback(()=>{setError('');onRefresh?.();},[onRefresh]);
  const draftContent = useRef(initial.value);
  const numberingEnabled = descriptor.capabilities.includes('numbering');
  const [numberingView, setNumberingView] = useState<{ editingKey: string; source: unknown; id: string; revision: number; draft?: number; result: DocumentNumberingResult }>();
  // Keep the displayed labels until their replacement arrives after an autosave.
  const displayedNumbering = numberingEnabled && numberingView?.editingKey === editingKey
    && numberingView.result.representation === representation
    ? numberingView : undefined;
  const currentNumbering = displayedNumbering && displayedNumbering.source === value && displayedNumbering.id === latest.current.id
    && displayedNumbering.revision === latest.current.revision && displayedNumbering.draft === latest.current.draft
    ? displayedNumbering.result : undefined;
  const numbering = displayedNumbering?.result.numbering as WriterNumberingState | undefined;
  const [selection, setSelection] = useState<ArtifactRewriteSelection | null>(null);
  const [preview, setPreview] = useState<{ id: string; selection: ArtifactRewriteSelection; value: RewriteSelectionPreview } | null>(null);
  const [downloading, setDownloading] = useState(false);
  const downloadPending = useRef(false);
  const retryDownload = useRef<() => void>();
  const [publicationStatus, setPublicationStatus] = useState<{ signature: string; text: string }>();
  const slotSignature = JSON.stringify([slot.artifact_id, slot.revision, slot.draft_version]);
  const [currentSignature, setCurrentSignature] = useState(slotSignature);
  const [publicationUrl, setPublicationUrl] = useState(() => documentPublicationTargetUrl({ uri: slot.write_back_url, doc_id: slot.provider_document_id }, slot.provider));
  const publicationSucceeded = useCallback((url: string | undefined, provider?: string) => {
    setPublicationUrl(url);
    // A recovered publication must not mark newer local content as synced.
    if (dirty.current || slot.write_back_state === 'synced_dirty'
      || slotSignature !== JSON.stringify([latest.current.id, latest.current.revision, latest.current.draft])) return;
    setPublicationStatus({ signature: slotSignature, text: provider === 'wechat' && !url
      ? String(i18n.t('chat.writerIR.wechatDraftLinkUnavailable')) : String(i18n.t('chat.writerIR.writeBackSuccess')) });
  }, [slotSignature, slot.write_back_state]);
  const [providerRefresh, setProviderRefresh] = useState(0);
  const availability = useWriterProviderAvailability(providers.map(provider => provider.id));
  const previewSource = useRef<Baseline>();

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
  const imageDownloads = useDocumentImageDownloads({ scopeKey: editingKey, resolveImage: resolveImageUrl,
    readSource: () => ({ artifactId: latest.current.id, revision: latest.current.revision, draft: latest.current.draft,
      document: getSnapshot?.(editingKey) ?? draftContent.current }),
  });
  const imageDownloadsRef = useRef(imageDownloads); imageDownloadsRef.current = imageDownloads;

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
      if (!dirty.current) { latest.current = next; draftContent.current = content; setCurrentSignature(signature); }
      // A publication refresh must not replace the editor holding newer local edits.
      if (dirty.current && retainedPublicationSource.current !== undefined
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
    setCurrentSignature(JSON.stringify([id, revision, draft]));
    if (!newerEdits) { dirty.current = false; draftContent.current = content; }
    setHasLocalChanges(dirty.current);
    retainedPublicationSource.current = newerEdits && typeof submitted !== typeof content ? submitted : undefined;
    setValue(retainedPublicationSource.current ?? content); setVersion(revision);
  }, []);
  const edit = useCallback((content: unknown) => {
    draftContent.current = content;
    setHasImages(documentImages(content).length > 0);
    dirty.current = JSON.stringify(content) !== JSON.stringify(retainedPublicationSource.current ?? external.current.value);
    setHasLocalChanges(dirty.current);
    if (!dirty.current) {
      latest.current = external.current;
      setCurrentSignature(JSON.stringify([external.current.id, external.current.revision, external.current.draft]));
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
    if (!writable) return false;
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
    onRefresh?.();
    return latest.current;
  }, [accept, onRefresh]);

  useEffect(() => {
    if (!loaded || !numberingEnabled) return;
    let canceled = false; const current = latest.current;
    void WorkflowSessionApi().previewDocumentAction(current.id, { action: 'numbering', base_revision: current.revision,
      base_draft_version: current.draft, input: {} }).then((response) => {
        if (!canceled && latest.current.id === current.id && latest.current.revision === current.revision
          && latest.current.draft === current.draft) {
          setNumberingView({ editingKey, source: value, id: current.id, revision: current.revision, draft: current.draft,
            result: response.data.data as DocumentNumberingResult });
        }
      }).catch(() => {});
    return () => { canceled = true; };
  }, [loaded, value, currentSignature, editingKey, numberingEnabled]);
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
    const convertible = descriptor.capabilities.includes('convert_document');
    if (!active || !loaded || (!convertible && !hasImages)) return;
    return registerFooterAction(`${editingKey}:download`, {
      label: String(i18n.t(downloading || imageDownloads.busy ? 'chat.writerLocal.preparing' : 'chat.slots.download')), icon: 'download', order: 25,
      menuLabel: String(i18n.t('chat.writerLocal.chooseDownload')),
      disabled: downloading || imageDownloads.busy, onClick: () => convertible ? void downloadRef.current('markdown') : imageDownloadsRef.current.showGallery(),
      menu: [...(convertible ? (['markdown', 'text', 'latex', 'lmd'] as const).map(format => ({ key: format,
        label: String(i18n.t(format === 'lmd' ? 'chat.writerLocal.structure' : `chat.writerCopy.${format}`)), onClick: () => void downloadRef.current(format) })) : []),
        ...(hasImages ? imageDownloadsRef.current.menu : [])],
    });
  }, [active, loaded, descriptor.capabilities, registerFooterAction, editingKey, downloading, hasImages, imageDownloads.busy]);
  const applyPreview = async () => {
    if (!preview?.value.commit?.token) return undefined;
    const baseline=previewSource.current;
    if (dirty.current) throw rewriteConflict('SELECTION_STALE');
    if (!baseline || external.current.id!==baseline.id || external.current.revision!==baseline.revision || external.current.draft!==baseline.draft) throw rewriteConflict('DRAFT_VERSION_CONFLICT');
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

  const imageGroupReminders = useRef(new Set<string>());
  const providerLabel = (provider: string) => i18n.exists(`chat.writerIR.providers.${provider}`) ? String(i18n.t(`chat.writerIR.providers.${provider}`)) : provider;
  const chooseProvider = (provider?: string) => {
    if (publicationBlocked || !loaded || !writable) return;
    if (provider && !publicationChoices[provider]?.selectable) return;
    setError(''); setErrorAction(null);
    setPublishDialog({ provider });
  };
  const chooseRef = useRef(chooseProvider); chooseRef.current = chooseProvider;
  useEffect(() => {
    if (!active || !writable || !descriptor.capabilities.includes('publish_document')) return;
    const authorizationStatus = (provider: string) => publicationChoices[provider]?.reason ?? String(i18n.t('chat.writerLocal.checking'));
    // Saving edits advances the local identity, not the published cloud version.
    const confirmedStatus = publicationStatus
      ? publicationStatus.signature === currentSignature ? publicationStatus.text : String(i18n.t('chat.writerLocal.publishedWithEdits'))
      : '';
    // Slot metadata may still describe the version before the latest local save.
    const synced = slot.write_back_state === 'synced_clean' && slotSignature === currentSignature;
    const successStatus = publicationBlocked ? '' : hasLocalChanges
      ? (confirmedStatus === String(i18n.t('chat.writerLocal.publishedWithEdits')) ? confirmedStatus : '')
      : confirmedStatus || (synced ? String(i18n.t('chat.writerIR.writeBackSuccess')) : '');
    return registerFooterAction(`${editingKey}:publish`, {
      label: String(i18n.t('chat.writerLocal.publishTo')),
      icon: 'write-back', tone: 'primary', order: 30,
      menuLabel: String(i18n.t('chat.writerLocal.chooseProvider')),
      disabled: publicationBlocked || !loaded || providers.length === 0, flushBeforeAction: false, flushKey: editingKey,
      onClick: () => chooseRef.current(),
      menu: providers.length > 1 ? providers.map(provider => {
        const label = [providerLabel(provider.id), authorizationStatus(provider.id)].filter(Boolean).join(' · ');
        const state = availability.states[provider.id];
        const isObsidian = provider.id === 'obsidian';
        const settingsAction = isObsidian ? String(i18n.t('modelProvider.cloudDocuments.obsidianSetupGuideShort')) : state === 'authorize' ? String(i18n.t('chat.writerLocal.authorizeAction'))
          : state === 'chat-disabled' ? String(i18n.t('chat.writerLocal.settings')) : '';
        return { key: provider.id,
          disabled: !publicationChoices[provider.id]?.selectable,
          label: settingsAction ? <span className='workflow-panel__provider-menu-label'>
            <span className='workflow-panel__provider-menu-copy'>
              <span>{label}</span>
              {isObsidian && <small title={String(i18n.t('modelProvider.cloudDocuments.obsidianEnvironmentHint'))}>{i18n.t('modelProvider.cloudDocuments.obsidianEnvironmentSummary')}</small>}
            </span>
            <a className='workflow-panel__provider-settings-link'
              href={isObsidian ? CLOUD_DOCUMENTS_OBSIDIAN_SETUP_PATH : getCloudDocumentsUrl(provider.id === 'feishu' || provider.id === 'googledrive' || provider.id === 'wechat' ? provider.id : undefined)}
              target='_blank' rel='noopener noreferrer'
              aria-disabled={false}
              aria-label={isObsidian ? String(i18n.t('modelProvider.cloudDocuments.obsidianSetupGuideAction')) : String(i18n.t('chat.writerLocal.providerSettingsLabel', { provider: providerLabel(provider.id), action: settingsAction }))}
              onClick={event => event.stopPropagation()}
              onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') event.stopPropagation(); }}>
              {settingsAction}<ExportOutlined aria-hidden='true' />
            </a>
          </span> : label,
          icon: <span className='workflow-panel__provider-icon' aria-hidden='true'><WriterProviderIcon provider={provider.id} /></span>,
          onClick: () => chooseRef.current(provider.id),
        };
      }) : undefined,
      statusText: error || successStatus, statusTone: error ? 'error' : 'success',
      statusLink: publicationUrl ? { href: publicationUrl, label: String(i18n.t('chat.writerIR.openCloudDocument')) } : undefined,
    });
  }, [active, writable, descriptor.capabilities, registerFooterAction, editingKey, loaded, providers, publicationBlocked, publicationChoices, error, publicationStatus, publicationUrl, slot.provider, slot.write_back_ready, slot.write_back_state, availability.states, currentSignature, slotSignature, hasLocalChanges]);

  if (!loaded) return <div role='status'>{error || '…'}</div>;
  const versionHistory = VersionHistory && sessionId ? <VersionHistory sessionId={sessionId} slotId={slot.slot_id}
    listIndex={slot.list_index ?? -1} revisionCount={revisionCount ?? version} currentRevision={version}
    currentVersionNumber={slot.version_number} currentValue={value} currentChangeSource={slot.change_source}
    readCurrentValue={() => getSnapshot?.(editingKey) ?? draftContent.current} contentType='text'
    readOnly={!writable} triggerLabel={String(i18n.t('chat.slots.versionHistory'))} onRollback={rollback} /> : undefined;
  return <SlotEditingContext.Provider value={editorContext}><div className='workflow-slot workflow-slot--artifact'>
    <div className={`workflow-slot__artifact-body${representation === 'markdown' ? ' workflow-slot__artifact-body--markdown' : ''}`} onClick={imageDownloads.onImageClick}>
    {representation === 'markdown' && typeof value === 'string'
      ? <MarkdownArtifactEditor renderContext={descriptor.render_context} markdown={value} sourceRevision={version} editingKey={editingKey} readOnly={!writable} allowMultipleParagraphs
        numberingDocument={typeof currentNumbering?.document === 'string' ? currentNumbering.document : undefined}
        resolveImageUrl={resolveImageUrl} onDownloadImage={imageDownloads.downloadImage} onContentChange={edit} numbering={numbering} toolbarActions={versionHistory}
        onRewriteSelection={canRewrite ? (picked) => { if (picked.supported) setSelection({ type: 'markdown', selected_text: picked.text, selectedText: picked.text, paragraph: picked.paragraph, paragraphs: picked.paragraphSelections?.map(item => item.paragraph), startOffset: picked.startOffset, sourceRange: picked.sourceRange, sourceRanges: picked.sourceRanges, anchor: picked.anchor }); } : undefined}
        rewriteDialogOpen={selection !== null}
        rewritePreview={preview?.selection.paragraph ? { paragraph: preview.selection.paragraph, paragraphs: preview.selection.paragraphs, sourceMarkdown: String(previewSource.current?.value ?? value), startOffset: preview.selection.startOffset,
          sessionId, slotId: slot.slot_id, listIndex: slot.list_index ?? -1, preview: preview.value, applyPreview } : null}
        onRewritePreviewApplied={() => setPreview(null)} onRewritePreviewRejected={() => setPreview(null)}
        onSave={async (text, revision, mode, numbering) => { const saved = await save(text, revision, mode, numbering); return { markdown: String(saved.value), revision: saved.revision }; }} />
      : isWriterDocument(value) ? <WriterIRControl document={value as WriterDocument} sourceRevision={version} editingKey={editingKey} readOnly={!writable} allowMultipleParagraphs
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
    {imageDownloads.dialog}
    {error && <div className='workflow-document-notice' role='alert'>
      <span className='workflow-document-notice__icon' aria-hidden='true'><ExclamationCircleOutlined /></span>
      <span className='workflow-document-notice__message'>{error}</span>
      {errorAction && <div className='workflow-document-notice__actions'>
        <button className='workflow-document-notice__action' type='button' onClick={() => { if (errorAction === 'download') retryDownload.current?.(); else if (errorAction === 'history') retryHistory.current?.(); else { setError(''); setProviderRefresh(value => value + 1); void availability.refresh(); } }}>
          <ReloadOutlined aria-hidden='true' />{String(i18n.t('common.retry'))}
        </button>
      </div>}
    </div>}
    {descriptor.capabilities.includes('publish_document') && <DocumentPublicationDialog key={`${sessionId}:${slot.slot_id}:${slot.list_index ?? -1}`}
      artifactId={latest.current.id} sourceSignature={currentSignature} providers={providers} availability={availability} open={!!publishDialog} initialProvider={publishDialog?.provider} dirty={hasLocalChanges} readOnly={!writable}
      onProviderChoicesChange={setPublicationChoices}
      onClose={() => setPublishDialog(undefined)} beforePublish={async selectedProviders => {
        if (flushEditor.current && !await flushEditor.current()) throw new Error(String(i18n.t('chat.writerIR.saveFailed')));
        if (dirty.current) throw new Error(String(i18n.t('chat.writerIR.saveFailed')));
        const content = draftContent.current;
        const pending = selectedProviders.includes('wechat') && (typeof content === 'string' || isWriterDocument(content))
          ? pendingImageGroups(content).filter(group => !imageGroupReminders.current.has(imageGroupKey(group.images))) : [];
        if (pending.length) {
          const keepVertical = await new Promise<boolean>(resolve => {
            Modal.confirm({ title: i18n.t('chat.writerImageGroups.title'),
              content: i18n.t('chat.writerImageGroups.reminder', { count: pending.length }),
              okText: i18n.t('chat.writerImageGroups.keepVertical'), cancelText: i18n.t('chat.writerImageGroups.returnToDocument'),
              onOk: () => { resolve(true); },
              onCancel: () => {
                setPublishDialog(undefined);
                const group = Array.from(window.document.querySelectorAll<HTMLElement>('.writer-image-group'))
                  .find(element => pending.some(candidate => imageGroupKey(candidate.images) === element.dataset.imageGroupKey));
                group?.scrollIntoView({ block: 'center', behavior: 'smooth' });
                group?.querySelector<HTMLButtonElement>('button')?.focus();
                resolve(false);
              },
            });
          });
          if (!keepVertical) return undefined;
          const current = draftContent.current;
          if (typeof current !== 'string' && !isWriterDocument(current)) throw new Error(String(i18n.t('chat.writerIR.saveFailed')));
          await save(keepVerticalImageGroups(current, pending), latest.current.revision, 'draft');
          pending.forEach(group => imageGroupReminders.current.add(imageGroupKey(group.images)));
        }
        return { ...latest.current };
      }} />}
    {descriptor.capabilities.includes('publish_document') && <DocumentPublicationRecoveryPanel key={latest.current.id} artifactId={latest.current.id} slotId={slot.slot_id} itemIndex={slot.list_index ?? -1}
      refreshKey={0} publishing={false} readOnly={readOnly} canApplyLocal={()=>!dirty.current} onAvailability={publicationAvailable} onResolved={publicationResolved} onPublished={writable ? publicationSucceeded : undefined} onTarget={writable ? setPublicationUrl : undefined} />}
    <ArtifactRewriteDialog open={selection !== null} sessionId={sessionId} slotId={slot.slot_id} listIndex={slot.list_index ?? -1}
      baseRevision={latest.current.revision} baseDraftVersion={latest.current.draft} selection={selection}
      onClose={() => setSelection(null)} onApplied={() => { setPreview(null); onRefresh?.(); }}
      requestPreview={async (instruction, picked) => {
        if (dirty.current && flushEditor.current && !await flushEditor.current()) throw rewriteConflict('DRAFT_VERSION_CONFLICT');
        const current = latest.current; previewSource.current = { ...current };
        if (picked.type === 'ppt_html') throw new Error('unsupported document selection');
        const response = await WorkflowSessionApi().previewDocumentAction(current.id, { action: 'rewrite_selection',
          base_revision: current.revision, base_draft_version: current.draft,
          input: picked.type === 'ir' ? { instruction, type: 'ir', selection_ranges: picked.nodeSelections ?? [{ node_id: picked.node_id, ...(picked.selectedText ? { selected_text: picked.selectedText } : {}) }] } : { instruction, type: 'markdown', selection_ranges: picked.sourceRanges ?? [picked.sourceRange ?? { selected_text: picked.selected_text }] } });
        return documentRewritePreview(response.data.data, current.revision, current.draft);
      }} onPreviewReady={(result) => { if (selection && previewSource.current) setPreview({ id: previewSource.current.id, selection, value: result }); }} />

  </div></SlotEditingContext.Provider>;
}
