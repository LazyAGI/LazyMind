import { AgentAppsAuth } from '@/components/auth';
import { BASE_URL } from '@/components/request';
import { ArtifactV2Api, TaskServiceApi } from '../../utils/request';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync } from '@/modules/knowledge/utils/imageUrl';
import { extractTextContent, toArtifactFiles, type ArtifactFile } from '../ArtifactCollectorCard/artifactFiles';
import type { ConversationArtifact } from '../../store/taskCenter';
import { useWorkflowStore } from '../../store/workflowPanel';
import type { SourceContext } from '../SourceImports/api';
import type { ChatSource } from '../../utils/sourceAdapter';
import type { KnowledgeOperation } from '../../utils/knowledgeOperations';

export type ArtifactSourceContext = { context: SourceContext; sources: ChatSource[]; operations?: KnowledgeOperation[] };

export interface WorkflowPreviewOrigin {
  sessionId: string;
  stepId: string;
}

export interface ArtifactPreviewTarget {
  key: string;
  conversationId?: string;
  filename: string;
  url?: string;
  downloadUrl?: string;
  content?: string;
  mimeType?: string;
  canDownload?: boolean;
  refreshUrl?: () => Promise<string>;
  workflow?: WorkflowPreviewOrigin;
  sourceContext?: ArtifactSourceContext;
}

export function safePreviewUrl(raw: string): string {
  if (!raw || raw.includes('\\')) return '';
  if (/^data:image\/(png|jpe?g|gif|webp|bmp);base64,/i.test(raw)) return raw;
  try {
    const url = new URL(raw, window.location.origin);
    if (!['http:', 'https:', 'blob:'].includes(url.protocol) || url.username || url.password) return '';
    return raw;
  } catch { return ''; }
}

export function filePreviewTarget(file: ArtifactFile): ArtifactPreviewTarget {
  const artifact = file.artifact;
  const revision = artifact.revision_id || file.revision;
  const inline = artifact.content_type === 'text' || artifact.content_type === 'json';
  const session = useWorkflowStore.getState().sessionByConversation[artifact.conversation_id];
  const producer = session?.ordinary_tasks?.find(task => task.task_id === artifact.producer_id
    || task.stage_artifacts.some(item => [artifact.artifact_id, artifact.v2_artifact_id].includes(item.artifact_id)));
  const stepId = producer?.workflow_step_id
    || session?.steps?.find(step => step.task_id === artifact.producer_id)?.step_id
    || session?.slots?.find(slot => [artifact.artifact_id, artifact.v2_artifact_id].filter(Boolean).includes(slot.artifact_id))?.step_id;
  return {
    key: `${artifact.conversation_id}:${file.id}:${revision}`,
    conversationId: artifact.conversation_id,
    workflow: artifact.source_type === 'workflow' || stepId
      ? { sessionId: session?.session_id || '', stepId: stepId || '' } : undefined,
    filename: file.filename,
    url: file.url,
    content: inline ? extractTextContent(artifact) : undefined,
    mimeType: artifact.value?.mime_type || (artifact.content_type === 'json' ? 'application/json' : undefined),
    refreshUrl: file.url ? async () => {
      if (artifact.v2_artifact_id && artifact.revision_id && artifact.content_type !== 'file_list') {
        const response = await ArtifactV2Api().downloadRevisionUrl(artifact.revision_id!);
        const payload = response.data?.data ?? response.data;
        if (!payload?.url) throw new Error('unavailable');
        return resolveCoreAssetUrl(payload.url);
      }
      const response = await TaskServiceApi().listConversationArtifacts(artifact.conversation_id);
      const payload = response.data?.data ?? response.data;
      const records: ConversationArtifact[] = [...(payload.deliveries ?? []), ...(payload.artifacts ?? [])];
      const same = records.find(item => item.artifact_id === artifact.artifact_id &&
        (artifact.revision_id ? item.revision_id === artifact.revision_id : (item.revision || item.seq || 1) === file.revision));
      const refreshed = same && toArtifactFiles([same]).find(item => item.id === file.id);
      if (!refreshed?.url) throw new Error('unavailable');
      return refreshed.url;
    } : undefined,
  };
}

export function urlPreviewTarget(url: string, filename: string, conversationId?: string): ArtifactPreviewTarget {
  return {
    key: `${conversationId || ''}:${url}`, conversationId, filename, url,
    refreshUrl: url.includes('/static-files/') ? () => resolveMarkdownImageUrlAsync(url, { forceRefresh: true }) : undefined,
  };
}

export function previewKind(target: ArtifactPreviewTarget): string {
  const ext = previewFilename(target).split('.').pop()?.toLowerCase() || '';
  if (['html', 'htm'].includes(ext)) return 'html';
  if (ext === 'json' || target.mimeType === 'application/json') return 'json';
  if (['md', 'markdown', 'txt', 'log', 'csv', 'pdf', 'doc', 'docx', 'ppt', 'pptx', 'pptm', 'xls', 'xlsx',
    'jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'tif', 'tiff', 'svg', 'xml',
    'mp3', 'wav', 'm4a', 'aac', 'flac', 'mp4', 'webm', 'ogg', 'ogv', 'mov', 'm4v'].includes(ext)) return 'file';
  return target.content !== undefined ? 'text' : 'unsupported';
}

export function previewFilename(target: ArtifactPreviewTarget): string {
  const mimeExtensions: Record<string, string> = {
    'application/pdf': 'pdf', 'text/markdown': 'md', 'text/plain': 'txt', 'text/html': 'html',
    'application/json': 'json', 'image/png': 'png', 'image/jpeg': 'jpg', 'image/webp': 'webp',
    'audio/mpeg': 'mp3', 'video/mp4': 'mp4',
    'application/vnd.openxmlformats-officedocument.wordprocessingml.document': 'docx',
    'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': 'xlsx',
    'application/vnd.openxmlformats-officedocument.presentationml.presentation': 'pptx',
  };
  const extension = mimeExtensions[(target.mimeType || '').split(';')[0]];
  return extension && !/\.[a-z0-9]+$/i.test(target.filename) ? `${target.filename}.${extension}` : target.filename;
}

export async function readPreviewFile(target: ArtifactPreviewTarget, signal: AbortSignal): Promise<ArrayBuffer> {
  if (target.content !== undefined) return new TextEncoder().encode(target.content).buffer as ArrayBuffer;
  const fetchFile = async (raw: string) => {
    const url = safePreviewUrl(resolveCoreAssetUrl(raw));
    if (!url) throw new Error('unavailable');
    const parsed = new URL(url, window.location.origin);
    const core = new URL(BASE_URL || window.location.origin, window.location.origin);
    const authenticated = [window.location.origin, core.origin].includes(parsed.origin) && /^\/(api\/|static-files\/)/.test(parsed.pathname);
    return fetch(url, {
      signal, credentials: authenticated ? 'same-origin' : 'omit',
      headers: authenticated ? AgentAppsAuth.getAuthHeaders() : undefined,
      redirect: authenticated ? 'error' : 'follow',
    });
  };
  let response = await fetchFile(target.url || '');
  if ([401, 403].includes(response.status) && target.refreshUrl) {
    const refreshed = await target.refreshUrl();
    signal.throwIfAborted();
    response = await fetchFile(refreshed);
  }
  if (!response.ok) throw new Error([401, 403, 404, 410].includes(response.status) ? 'unavailable' : 'load');
  return response.arrayBuffer();
}
