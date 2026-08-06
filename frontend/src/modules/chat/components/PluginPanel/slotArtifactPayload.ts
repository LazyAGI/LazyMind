import type { SlotRevision } from '@/modules/chat/store/pluginPanel';
import { PluginSessionApi } from '@/modules/chat/utils/request';
import { WRITER_ARTIFACT_SLOT_IDS, unwrapArtifactPayload } from './writerArtifactViews';
import type { WriterIRSaveMode, WriterIRSaveResult } from './WriterIRControl';
import {
  isWriterDocument,
  normalizeWriterDocumentForSync,
  type WriterDocument,
} from './writerIR';
import {
  isJsonArtifactFile,
  isMarkdownArtifactFile,
  isOffloadedArtifactReference,
  isWriterIrArtifactFile,
} from './slotUtils';
import { tr } from './slotShared';

export function getInlineStructuredArtifactPayload(slot: SlotRevision): unknown | null {
  const raw = slot.artifact_value;
  if (!raw || typeof raw !== 'object') return null;
  const record = raw as Record<string, unknown>;

  if (isOffloadedArtifactReference(record)) {
    return null;
  }

  if (isWriterDocument(record)) {
    return record;
  }

  if (record.data !== undefined) {
    const payload = unwrapArtifactPayload(raw);
    if (payload !== null && payload !== undefined && typeof payload === 'object') {
      return payload;
    }
    if (typeof payload === 'string') {
      try {
        return JSON.parse(payload);
      } catch {
        return null;
      }
    }
  }

  if (slot.content_type === 'json' && record.text === undefined) {
    return unwrapArtifactPayload(raw);
  }

  return null;
}

export function replaceStructuredArtifactPayload(
  source: unknown,
  document: WriterDocument,
): unknown {
  if (isWriterDocument(source)) return document;
  if (
    source
    && typeof source === 'object'
    && !Array.isArray(source)
    && Object.prototype.hasOwnProperty.call(source, 'data')
  ) {
    return { ...(source as Record<string, unknown>), data: document };
  }
  return document;
}

/** True when the document has a cloud provider target (Feishu uri or document_id). */
export async function syncWriterDocumentSlot(
  sessionId: string,
  slotId: string,
  listIndex: number,
  sourceRevision: string | number | undefined,
  sourceDocument: WriterDocument,
  revisedDocument: WriterDocument,
  mode: WriterIRSaveMode = 'checkpoint',
): Promise<WriterIRSaveResult> {
  if (typeof sourceRevision !== 'number' || sourceRevision <= 0) {
    throw new Error(tr('chat.writerIR.saveFailed'));
  }
  const response = await PluginSessionApi().syncWriterDocument(
    sessionId,
    slotId,
    listIndex,
    {
      base_revision: sourceRevision,
      source_document: normalizeWriterDocumentForSync(sourceDocument),
      revised_document: normalizeWriterDocumentForSync(revisedDocument),
      mode,
    },
    { silentError: true } as never,
  );
  const result = response?.data?.data;
  if (
    response?.data?.code !== 0
    || !result
    || (result.status !== 'synced' && result.status !== 'no_change')
    || typeof result.revision !== 'number'
    || result.revision <= 0
    || result.feishu_synced !== true
    || (result.status === 'synced' && result.artifact_saved !== true)
    || (result.status === 'no_change' && result.artifact_saved !== false)
    || result.patch_result?.success !== true
    || !isWriterDocument(result.document)
  ) {
    throw new Error(tr('chat.writerIR.saveFailed'));
  }
  return {
    document: result.document,
    sourceRevision: result.revision,
  };
}

export function shouldRenderInlineStructuredContent(
  slot: SlotRevision,
  expectedType?: 'image' | 'file' | 'text',
  slotId?: string,
): boolean {
  const payload = getInlineStructuredArtifactPayload(slot);
  if (payload === null) return false;
  if (isWriterDocument(payload)) {
    return expectedType !== 'image';
  }
  if (expectedType !== 'text') return false;
  if (slot.content_type === 'json') return true;
  const resolvedSlotId = slotId ?? slot.slot;
  return WRITER_ARTIFACT_SLOT_IDS.has(resolvedSlotId);
}

export function shouldRenderJsonFileAsContent(
  slot: SlotRevision,
  expectedType?: 'image' | 'file' | 'text',
): boolean {
  const raw = slot.artifact_value;
  if (!raw || typeof raw !== 'object') return false;
  if (isWriterIrArtifactFile(slot)) return true;
  const declaredJson = slot.content_type === 'json' || raw.type === 'json';
  if (expectedType !== 'text' && !declaredJson) return false;
  if (isJsonArtifactFile(slot)) return true;
  const hasPath = Boolean(String(raw.path ?? raw.url ?? '').trim());
  return hasPath && declaredJson;
}

export function shouldRenderMarkdownFileAsContent(
  slot: SlotRevision,
  expectedType?: 'image' | 'file' | 'text',
): boolean {
  return expectedType === 'file' && isMarkdownArtifactFile(slot);
}
