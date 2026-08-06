import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { SlotRevision } from '@/modules/chat/store/pluginPanel';
import { usePluginStore } from '@/modules/chat/store/pluginPanel';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';
import { localizeErrorCode } from '@/components/request';
import MarkdownViewer from '@/modules/chat/components/MarkdownViewer';
import {
  WriterArtifactContent,
  WRITER_ARTIFACT_SLOT_IDS,
  unwrapArtifactPayload,
} from './writerArtifactViews';
import { WriterIRControl, type WriterIRSaveMode, type WriterIRSaveResult } from './WriterIRControl';
import { isWriterDocument, type WriterDocument } from './writerIR';
import { SlotEditingContext } from './slotEditingContext';
import { useArtifactFileUrl } from './slotResources';
import { SlotVersionPopover } from './SlotVersionPopover';
import {
  getInlineStructuredArtifactPayload,
  replaceStructuredArtifactPayload,
  syncWriterDocumentSlot,
} from './slotArtifactPayload';
import {
  ensureJsonFilename,
  hasProviderTarget,
  isSpaFallbackHtml,
  writerDocumentToMarkdown,
  writerMarkdownFilename,
} from './slotUtils';
import { SlotDownloadContext, tr } from './slotShared';

interface SlotJsonFileProps {
  slot: SlotRevision;
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  onRefresh?: () => void;
  readOnly?: boolean;
}

export function SlotJsonFile({
  slot,
  sessionId,
  slotId,
  revisionCount,
  onRefresh,
  readOnly,
}: SlotJsonFileProps) {
  const allowDownload = useContext(SlotDownloadContext);
  const raw = slot.artifact_value;
  const name = String(raw?.filename ?? raw?.name ?? slotId ?? slot.slot);
  const [reloadToken, setReloadToken] = useState(0);
  const { url, resolving, hasSource, sourceKey } = useArtifactFileUrl(
    raw,
    `${slot.revision}:${reloadToken}`,
  );
  const { patchSlotItemValue } = usePluginStore();
  const { setEditing: notifyEditing } = useContext(SlotEditingContext);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [payload, setPayload] = useState<unknown>(null);
  const [sourceJson, setSourceJson] = useState<unknown>(null);
  const [loadedSourceKey, setLoadedSourceKey] = useState('');
  const [loadedRevision, setLoadedRevision] = useState<number>();
  const [localRevisionCount, setLocalRevisionCount] = useState<number | undefined>(revisionCount);
  const [writerEditing, setWriterEditing] = useState(false);
  const hasPayloadRef = useRef(false);
  hasPayloadRef.current = payload !== null;

  const applySavedRevision = useCallback((revision?: number) => {
    if (typeof revision !== 'number' || revision <= 0) return;
    setLoadedRevision((prev) => (prev === undefined || revision > prev ? revision : prev));
    setLocalRevisionCount((prev) => Math.max(prev ?? 0, revisionCount ?? 0, revision));
  }, [revisionCount]);

  useEffect(() => {
    if (typeof slot.revision === 'number' && slot.revision > 0) {
      setLoadedRevision((prev) => (prev === undefined || slot.revision >= prev ? slot.revision : prev));
    }
  }, [slot.revision]);

  useEffect(() => {
    if (typeof revisionCount === 'number' && revisionCount > 0) {
      setLocalRevisionCount((prev) => (prev === undefined || revisionCount >= prev ? revisionCount : prev));
    }
  }, [revisionCount]);

  useEffect(() => {
    if (!hasSource) return;
    setError(null);
  }, [hasSource, sourceKey]);

  useEffect(() => {
    if (!hasSource) {
      setLoading(false);
      setError(localizeErrorCode('2000509'));
      setPayload(null);
      setSourceJson(null);
      setLoadedSourceKey('');
      setLoadedRevision(undefined);
      return;
    }
    if (resolving) return;
    if (!url) {
      setLoading(false);
      setError(localizeErrorCode('2000509'));
      return;
    }

    const controller = new AbortController();
    // Soft refresh: keep the editor mounted when content is already on screen.
    if (!hasPayloadRef.current) setLoading(true);
    setError(null);

    fetch(url, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) {
          throw new Error(localizeErrorCode('2000509'));
        }
        return response.json();
      })
      .then((json) => {
        setSourceJson(json);
        setPayload(unwrapArtifactPayload(json));
        setLoadedSourceKey(sourceKey);
        // Prefer a newer locally-saved revision over a stale session snapshot.
        setLoadedRevision((prev) => (
          typeof prev === 'number' && prev > slot.revision ? prev : slot.revision
        ));
        setLoading(false);
      })
      .catch((fetchError: unknown) => {
        if (fetchError instanceof DOMException && fetchError.name === 'AbortError') return;
        setError(localizeErrorCode('2000509'));
        setLoading(false);
      });

    return () => controller.abort();
  }, [hasSource, reloadToken, resolving, slot.revision, sourceKey, url]);

  const apiListIndex = slot.list_index ?? -1;
  const resolvedSlotId = slotId ?? slot.slot;
  const showArtifactActions = !WRITER_ARTIFACT_SLOT_IDS.has(resolvedSlotId);
  const writerDocument = isWriterDocument(payload) ? payload : null;
  const usesWriterSync = apiListIndex === -1 && hasProviderTarget(writerDocument);
  const canEditWriterIR = Boolean(sessionId && slotId)
    && !readOnly
    && writerDocument?.ui_editable === true
    && (loadedSourceKey === sourceKey || writerEditing);
  const editingKey = `${sessionId}:${slotId}:${apiListIndex}:writer-ir`;
  const displayRevision = loadedRevision ?? slot.revision;
  const displayRevisionCount = localRevisionCount ?? revisionCount;
  const showVersionBadge =
    displayRevisionCount !== undefined && displayRevisionCount > 0 && Boolean(sessionId && slotId);

  const handleSaveWriterDocument = useCallback(async (
    sourceDocument: WriterDocument,
    document: WriterDocument,
    sourceRevision?: string | number,
    mode: WriterIRSaveMode = 'checkpoint',
  ): Promise<WriterIRSaveResult | void> => {
    if (!sessionId || !slotId || readOnly) {
      throw new Error(tr('chat.writerIR.saveFailed'));
    }

    if (usesWriterSync) {
      try {
        const result = await syncWriterDocumentSlot(
          sessionId,
          slot.slot_id,
          apiListIndex,
          sourceRevision,
          sourceDocument,
          document,
          mode,
        );
        const serialized = replaceStructuredArtifactPayload(sourceJson, result.document);
        setSourceJson(serialized);
        // Prefer the client snapshot reference so WriterIRControl can treat the
        // prop update as identity-equal to its in-flight draft and skip a redraw.
        setPayload(document);
        applySavedRevision(
          typeof result.sourceRevision === 'number' ? result.sourceRevision : undefined,
        );
        // Keep the editor mounted: local payload/revision are already authoritative.
        // Session polling will catch up without a hard refresh.
        return {
          ...result,
          document,
        };
      } catch (syncError) {
        onRefresh?.();
        throw syncError;
      }
    }

    const serialized = replaceStructuredArtifactPayload(sourceJson, document);
    const filename = ensureJsonFilename(name);
    const file = new File(
      [JSON.stringify(serialized, null, 2)],
      filename,
      { type: 'application/json' },
    );
    const storedPath = await uploadFileInChunks(file);
    const nextValue: Record<string, unknown> = {
      ...(raw && typeof raw === 'object' ? raw : {}),
      type: 'json',
      path: storedPath,
      filename,
      size: file.size,
    };
    delete nextValue.url;

    const revision = await patchSlotItemValue(
      sessionId, slotId, apiListIndex, nextValue, 'file', mode,
    );
    setSourceJson(serialized);
    setPayload(document);
    applySavedRevision(revision);
    return {
      document,
      sourceRevision: typeof revision === 'number' ? revision : sourceRevision,
    };
  }, [
    apiListIndex,
    applySavedRevision,
    name,
    onRefresh,
    patchSlotItemValue,
    raw,
    readOnly,
    sessionId,
    slot.slot_id,
    slotId,
    sourceJson,
    usesWriterSync,
  ]);

  const handleWriterEditingChange = useCallback((editing: boolean) => {
    setWriterEditing(editing);
    notifyEditing(editingKey, editing);
  }, [editingKey, notifyEditing]);

  const [writerMarkdownDownload, setWriterMarkdownDownload] = useState<{
    url: string;
    filename: string;
  } | null>(null);

  useEffect(() => {
    if (!writerDocument) {
      setWriterMarkdownDownload(null);
      return;
    }
    const blob = new Blob([writerDocumentToMarkdown(writerDocument)], {
      type: 'text/markdown;charset=utf-8',
    });
    const url = URL.createObjectURL(blob);
    setWriterMarkdownDownload({
      url,
      filename: writerMarkdownFilename(name),
    });
    return () => URL.revokeObjectURL(url);
  }, [name, writerDocument]);

  if (!hasSource) {
    return (
      <div className='plugin-slot plugin-slot--text plugin-slot--pending'>
        <span className='plugin-slot__placeholder'>{tr('chat.slots.pendingGeneration')}</span>
      </div>
    );
  }

  if ((loading || resolving) && payload === null) {
    return (
      <div className='plugin-slot plugin-slot--artifact plugin-slot--pending'>
        <span className='plugin-slot__placeholder'>{tr('common.loading')}</span>
      </div>
    );
  }

  if (payload === null) {
    return (
      <div className='plugin-slot plugin-slot--artifact plugin-slot--error'>
        <span className='plugin-slot__placeholder'>{error ?? tr('chat.slots.contentLoadFailed')}</span>
        <button
          className='plugin-slot__file-action-btn'
          type='button'
          onClick={() => setReloadToken((value) => value + 1)}
        >
          {tr('common.retry')}
        </button>
      </div>
    );
  }

  return (
    <div className='plugin-slot plugin-slot--artifact'>
      <div className='plugin-slot__artifact-body'>
        {loadedSourceKey !== sourceKey && !error && payload === null && (
          <div className='writer-ir__notice writer-ir__notice--warning' role='status'>
            {tr('chat.writerIR.refreshing')}
          </div>
        )}
        {error && loadedSourceKey !== sourceKey && (
          <div className='writer-ir__notice writer-ir__notice--error' role='alert'>
            <span>{tr('chat.writerIR.refreshFailed')}</span>
            <button
              className='plugin-slot__file-action-btn'
              type='button'
              onClick={() => setReloadToken((value) => value + 1)}
            >
              {tr('common.retry')}
            </button>
          </div>
        )}
        {writerDocument ? (
          <WriterIRControl
            document={writerDocument}
            sourceRevision={displayRevision}
            readOnly={!canEditWriterIR}
            editingKey={editingKey}
            onSave={canEditWriterIR ? handleSaveWriterDocument : undefined}
            onEditingChange={handleWriterEditingChange}
          />
        ) : (
          <WriterArtifactContent slotId={resolvedSlotId} data={payload} hideDownload={!allowDownload} />
        )}
      </div>
      <div className='plugin-slot__artifact-footer'>
        <div className='plugin-slot__artifact-footer-left'>
          {showVersionBadge && !writerEditing && (
            <SlotVersionPopover
              sessionId={sessionId!}
              slotId={slotId!}
              listIndex={apiListIndex}
              revisionCount={displayRevisionCount!}
              currentRevision={displayRevision}
              currentValue={slot.artifact_value}
              currentChangeSource={slot.change_source}
              contentType='json'
              onRollbackDone={onRefresh}
            />
          )}
        </div>
        <div className='plugin-slot__artifact-actions' hidden={!showArtifactActions}>
          {allowDownload && writerMarkdownDownload ? (
            <a
              className='plugin-slot__file-action-btn'
              href={writerMarkdownDownload.url}
              download={writerMarkdownDownload.filename}
              onClick={(event) => event.stopPropagation()}
            >
              {tr('chat.slots.download')}
            </a>
          ) : allowDownload && url ? (
            <a
              href={url}
              download={name}
              className='plugin-slot__file-action-btn'
              onClick={(e) => e.stopPropagation()}
            >
              {tr('chat.slots.download')}
            </a>
          ) : null}
        </div>
      </div>
    </div>
  );
}

interface SlotInlineStructuredProps {
  slot: SlotRevision;
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  onRefresh?: () => void;
  readOnly?: boolean;
}

export function SlotInlineStructured({
  slot,
  sessionId,
  slotId,
  revisionCount,
  onRefresh,
  readOnly,
}: SlotInlineStructuredProps) {
  const allowDownload = useContext(SlotDownloadContext);
  const payload = getInlineStructuredArtifactPayload(slot);
  const { patchSlotItemValue } = usePluginStore();
  const { setEditing: notifyEditing } = useContext(SlotEditingContext);
  const [writerEditing, setWriterEditing] = useState(false);
  const [localRevision, setLocalRevision] = useState(slot.revision);
  const [localRevisionCount, setLocalRevisionCount] = useState<number | undefined>(revisionCount);
  const apiListIndex = slot.list_index ?? -1;
  const resolvedSlotId = slotId ?? slot.slot;
  const writerDocument = isWriterDocument(payload) ? payload : null;
  const usesWriterSync = apiListIndex === -1 && hasProviderTarget(writerDocument);
  const canEditWriterIR = Boolean(sessionId && slotId)
    && !readOnly
    && writerDocument?.ui_editable === true;
  const editingKey = `${sessionId}:${slotId}:${apiListIndex}:writer-ir`;
  const displayRevision = localRevision ?? slot.revision;
  const displayRevisionCount = localRevisionCount ?? revisionCount;
  const showVersionBadge =
    displayRevisionCount !== undefined && displayRevisionCount > 0 && Boolean(sessionId && slotId);
  const [writerMarkdownDownload, setWriterMarkdownDownload] = useState<{
    url: string;
    filename: string;
  } | null>(null);

  const applySavedRevision = useCallback((revision?: number) => {
    if (typeof revision !== 'number' || revision <= 0) return;
    setLocalRevision((prev) => (prev === undefined || revision > prev ? revision : prev));
    setLocalRevisionCount((prev) => Math.max(prev ?? 0, revisionCount ?? 0, revision));
  }, [revisionCount]);

  useEffect(() => {
    if (typeof slot.revision === 'number' && slot.revision > 0) {
      setLocalRevision((prev) => (prev === undefined || slot.revision >= prev ? slot.revision : prev));
    }
  }, [slot.revision]);

  useEffect(() => {
    if (typeof revisionCount === 'number' && revisionCount > 0) {
      setLocalRevisionCount((prev) => (prev === undefined || revisionCount >= prev ? revisionCount : prev));
    }
  }, [revisionCount]);

  useEffect(() => {
    if (!writerDocument) {
      setWriterMarkdownDownload(null);
      return;
    }
    const blob = new Blob([writerDocumentToMarkdown(writerDocument)], {
      type: 'text/markdown;charset=utf-8',
    });
    const url = URL.createObjectURL(blob);
    setWriterMarkdownDownload({
      url,
      filename: writerMarkdownFilename(slot.caption || resolvedSlotId),
    });
    return () => URL.revokeObjectURL(url);
  }, [resolvedSlotId, slot.caption, writerDocument]);

  const handleSaveWriterDocument = useCallback(async (
    sourceDocument: WriterDocument,
    document: WriterDocument,
    sourceRevision?: string | number,
    mode: WriterIRSaveMode = 'checkpoint',
  ): Promise<WriterIRSaveResult | void> => {
    if (!sessionId || !slotId || readOnly) {
      throw new Error(tr('chat.writerIR.saveFailed'));
    }
    if (usesWriterSync) {
      try {
        const result = await syncWriterDocumentSlot(
          sessionId,
          slot.slot_id,
          apiListIndex,
          sourceRevision,
          sourceDocument,
          document,
          mode,
        );
        applySavedRevision(
          typeof result.sourceRevision === 'number' ? result.sourceRevision : undefined,
        );
        // Avoid hard session refresh; WriterIRControl already applied the result.
        return result;
      } catch (syncError) {
        onRefresh?.();
        throw syncError;
      }
    }
    const serialized = replaceStructuredArtifactPayload(slot.artifact_value, document);
    const revision = await patchSlotItemValue(
      sessionId, slotId, apiListIndex, serialized, 'json', mode,
    );
    applySavedRevision(revision);
    return {
      document,
      sourceRevision: typeof revision === 'number' ? revision : sourceRevision,
    };
  }, [
    apiListIndex,
    applySavedRevision,
    onRefresh,
    patchSlotItemValue,
    readOnly,
    sessionId,
    slot,
    slotId,
    usesWriterSync,
  ]);

  const handleWriterEditingChange = useCallback((editing: boolean) => {
    setWriterEditing(editing);
    notifyEditing(editingKey, editing);
  }, [editingKey, notifyEditing]);

  if (payload === null) {
    return (
      <div className='plugin-slot plugin-slot--artifact plugin-slot--error'>
        <span className='plugin-slot__placeholder'>{tr('chat.slots.contentLoadFailed')}</span>
      </div>
    );
  }

  return (
    <div className='plugin-slot plugin-slot--artifact'>
      <div className='plugin-slot__artifact-body'>
        {writerDocument ? (
          <WriterIRControl
            document={writerDocument}
            sourceRevision={displayRevision}
            readOnly={!canEditWriterIR}
            editingKey={editingKey}
            onSave={canEditWriterIR ? handleSaveWriterDocument : undefined}
            onEditingChange={handleWriterEditingChange}
          />
        ) : (
          <WriterArtifactContent slotId={resolvedSlotId} data={payload} hideDownload={!allowDownload} />
        )}
      </div>
      <div className='plugin-slot__artifact-footer'>
        <div className='plugin-slot__artifact-footer-left'>
          {showVersionBadge && !writerEditing && (
            <SlotVersionPopover
              sessionId={sessionId!}
              slotId={slotId!}
              listIndex={apiListIndex}
              revisionCount={displayRevisionCount!}
              currentRevision={displayRevision}
              currentValue={slot.artifact_value}
              currentChangeSource={slot.change_source}
              contentType='json'
              onRollbackDone={onRefresh}
            />
          )}
        </div>
        <div className='plugin-slot__artifact-actions'>
          {allowDownload && writerMarkdownDownload && (
            <a
              className='plugin-slot__file-action-btn'
              href={writerMarkdownDownload.url}
              download={writerMarkdownDownload.filename}
              onClick={(event) => event.stopPropagation()}
            >
              {tr('chat.slots.download')}
            </a>
          )}
        </div>
      </div>
    </div>
  );
}

interface SlotMarkdownFileProps {
  slot: SlotRevision;
  sessionId?: string;
  slotId?: string;
  revisionCount?: number;
  onRefresh?: () => void;
}

export function SlotMarkdownFile({
  slot,
  sessionId,
  slotId,
  revisionCount,
  onRefresh,
}: SlotMarkdownFileProps) {
  const allowDownload = useContext(SlotDownloadContext);
  const raw = slot.artifact_value;
  const name: string = raw?.filename ?? raw?.name ?? slotId ?? slot.slot;
  const { url, resolving, hasSource } = useArtifactFileUrl(raw);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [content, setContent] = useState('');

  useEffect(() => {
    if (!hasSource) {
      setLoading(false);
      setError(localizeErrorCode('2000509'));
      return;
    }
    if (resolving || !url) {
      return;
    }

    let cancelled = false;
    setLoading(true);
    setError(null);

    fetch(url)
      .then((response) => {
        if (!response.ok) {
          throw new Error(localizeErrorCode('2000509'));
        }
        return response.text();
      })
      .then((text) => {
        if (cancelled) return;
        if (isSpaFallbackHtml(text)) {
          throw new Error('invalid artifact content');
        }
        setContent(text);
        setLoading(false);
      })
      .catch(() => {
        if (!cancelled) {
          setError(localizeErrorCode('2000509'));
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [hasSource, resolving, url]);

  const apiListIndex = slot.list_index ?? -1;
  const showVersionBadge =
    revisionCount !== undefined && revisionCount > 0 && Boolean(sessionId && slotId);
  const resolvedSlotId = slotId ?? slot.slot;
  const showArtifactActions = !WRITER_ARTIFACT_SLOT_IDS.has(resolvedSlotId);

  if (!hasSource) {
    return (
      <div className='plugin-slot plugin-slot--text plugin-slot--pending'>
        <span className='plugin-slot__placeholder'>{tr('chat.slots.pendingGeneration')}</span>
      </div>
    );
  }

  if (loading || resolving) {
    return (
      <div className='plugin-slot plugin-slot--artifact plugin-slot--pending'>
        <span className='plugin-slot__placeholder'>{tr('common.loading')}</span>
      </div>
    );
  }

  if (error || !content.trim()) {
    return (
      <div className='plugin-slot plugin-slot--artifact plugin-slot--error'>
        <span className='plugin-slot__placeholder'>{error ?? tr('chat.slots.contentLoadFailed')}</span>
      </div>
    );
  }

  return (
    <div className='plugin-slot plugin-slot--artifact'>
      <div className='writer-artifact__output-toolbar' hidden={!allowDownload || !showArtifactActions}>
        <button
          type='button'
          className='plugin-slot__file-action-btn writer-artifact__download-btn'
          onClick={() => {
            const blob = new Blob([content], { type: 'text/markdown;charset=utf-8' });
            const objectUrl = URL.createObjectURL(blob);
            const anchor = document.createElement('a');
            anchor.href = objectUrl;
            anchor.download = name.toLowerCase().endsWith('.md') ? name : `${name.replace(/\.[^.]+$/, '') || 'writing_output'}.md`;
            anchor.click();
            URL.revokeObjectURL(objectUrl);
          }}
        >
          {tr('chat.writer.downloadMarkdown')}
        </button>
        {url ? (
          <a
            href={url}
            download={name}
            className='plugin-slot__file-action-btn'
            onClick={(e) => e.stopPropagation()}
          >
            {tr('chat.slots.downloadOriginalFile')}
          </a>
        ) : null}
      </div>
      <div className='plugin-slot__artifact-body'>
        {resolvedSlotId === 'writing_output_md' ? (
          <WriterArtifactContent slotId='writing_output' data={{ content }} hideDownload />
        ) : (
          <div className='writer-artifact__markdown'>
            <MarkdownViewer>{content}</MarkdownViewer>
          </div>
        )}
      </div>
      <div className='plugin-slot__artifact-footer'>
        <div className='plugin-slot__artifact-footer-left'>
          {showVersionBadge && (
            <SlotVersionPopover
              sessionId={sessionId!}
              slotId={slotId!}
              listIndex={apiListIndex}
              revisionCount={revisionCount!}
              currentRevision={slot.revision}
              currentValue={slot.artifact_value}
              currentChangeSource={slot.change_source}
              contentType='file'
              onRollbackDone={onRefresh}
            />
          )}
        </div>
      </div>
    </div>
  );
}

