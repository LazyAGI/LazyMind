import { downloadStream } from '@/modules/chat/utils/download';
import type { ConversationArtifact } from '@/modules/chat/store/taskCenter';
import {
  basenameFromPath,
  resolveCoreAssetUrl,
} from '@/modules/knowledge/utils/imageUrl';

const encoder = new TextEncoder();

export interface ArtifactFile {
  id: string;
  filename: string;
  sourceType: string;
  origin: ArtifactFileOrigin;
  revision: number;
  size?: number;
  url?: string;
  artifact: ConversationArtifact;
}

export type ArtifactFileOrigin = 'upload' | 'published' | 'draft';

export function formatFileSize(bytes?: number): string {
  if (bytes == null || bytes <= 0) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function extractTextContent(a: ConversationArtifact): string {
  const v = a.value;
  if (!v) return '';
  if (a.content_type === 'json') {
    try {
      return JSON.stringify(v.data ?? v, null, 2);
    } catch {
      return String(v.data ?? v ?? '');
    }
  }
  return v.text ?? '';
}

export function normalizeArtifactSourceType(sourceType?: string, producerType?: string): string {
  if (sourceType === 'user_upload' || sourceType === 'workflow' || sourceType === 'subagent' || sourceType === 'main_chat') {
    return sourceType;
  }
  return producerType === 'main_agent' ? 'main_chat' : 'subagent';
}

export function artifactFileOrigin(
  sourceType: string,
  publicationStatus?: string,
): ArtifactFileOrigin {
  const status = (publicationStatus || '').toLowerCase();
  if (status === 'draft') return 'draft';
  if (status === 'published') return 'published';
  if (status === 'input' || sourceType === 'user_upload') return 'upload';
  return 'published';
}

export function artifactSourceKey(sourceType: string): string {
  if (sourceType === 'user_upload') return 'chat.artifactSourceUserUpload';
  if (sourceType === 'workflow') return 'chat.artifactSourceWorkflow';
  if (sourceType === 'subagent') return 'chat.artifactSourceSubagent';
  return 'chat.artifactSourceChat';
}

export function toArtifactFiles(artifacts: ConversationArtifact[]): ArtifactFile[] {
  return artifacts.flatMap<ArtifactFile>((artifact): ArtifactFile[] => {
    const common = {
      id: artifact.artifact_id,
      sourceType: normalizeArtifactSourceType(artifact.source_type, artifact.producer_type),
      revision: artifact.revision || artifact.seq || 1,
      artifact,
    };
    const sourceType = common.sourceType;
    const origin = artifactFileOrigin(sourceType, artifact.publication_status);
    if (origin !== 'upload' && origin !== 'published') {
      return [];
    }
    if (artifact.content_type === 'file') {
      const url = resolveCoreAssetUrl(artifact.value?.url || '');
      return url
        ? [{
            ...common,
            origin,
            filename:
              artifact.filename || artifact.value?.filename || artifact.slot || 'file',
            size: artifact.value?.size,
            url,
          }]
        : [];
    }
    if (artifact.content_type === 'image') {
      const source = artifact.value?.url || artifact.value?.path || '';
      const url = resolveCoreAssetUrl(source);
      return url
        ? [{
            ...common,
            origin,
            filename: basenameFromPath(source || artifact.slot),
            url,
          }]
        : [];
    }
    if (artifact.content_type === 'file_list') {
      if (typeof artifact.value?.url === 'string' && artifact.value.url) {
        const url = resolveCoreAssetUrl(artifact.value.url);
        return url
          ? [{
                ...common,
              origin,
              filename:
                artifact.value.filename || artifact.filename || `${artifact.slot || 'files'}.zip`,
              url,
            }]
          : [];
      }
      const paths: string[] = Array.isArray(artifact.value?.paths)
        ? artifact.value.paths.filter(
            (path: unknown): path is string => typeof path === 'string',
          )
        : [];
      return paths.flatMap((path, pathIndex) => {
        const url = resolveCoreAssetUrl(path);
        return url
          ? [{
              ...common,
              origin,
              id: `${artifact.artifact_id}:${pathIndex}`,
              filename: basenameFromPath(path),
              url,
            }]
          : [];
      });
    }
    if (artifact.content_type === 'text' || artifact.content_type === 'json') {
      const filename = artifact.filename || (
        artifact.slot?.includes('.')
          ? artifact.slot
          : `${artifact.slot || 'artifact'}.txt`
      );
      return [{
        ...common,
        origin,
        filename,
        size: new Blob([extractTextContent(artifact)]).size,
      }];
    }
    return [];
  });
}

export async function fetchArtifactBytes(file: ArtifactFile): Promise<Uint8Array | null> {
  if (file.url) {
    try {
      const resp = await fetch(file.url);
      if (!resp.ok) return null;
      return new Uint8Array(await resp.arrayBuffer());
    } catch {
      return null;
    }
  }
  return encoder.encode(extractTextContent(file.artifact));
}

export async function downloadArtifactToDisk(file: ArtifactFile): Promise<boolean> {
  const data = await fetchArtifactBytes(file);
  if (!data) return false;
  downloadStream(new Blob([data], { type: 'application/octet-stream' }), file.filename);
  return true;
}
