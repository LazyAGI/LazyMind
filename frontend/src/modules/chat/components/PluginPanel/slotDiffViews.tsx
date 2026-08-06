import { useEffect, useMemo, useState } from 'react';
import { buildDiffLinesWithInline } from '@/modules/memory/shared';
import { DiffLineContent } from '@/modules/memory/components/DiffLineContent';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from '@/modules/knowledge/utils/imageUrl';
import { localizeErrorCode } from '@/components/request';
import { unwrapArtifactPayload } from './writerArtifactViews';
import { isWriterDocument } from './writerIR';
import { isSpaFallbackHtml, writerDocumentToMarkdown } from './slotUtils';
import { tr } from './slotShared';

interface TextDiffViewProps {
  currentText: string;
  otherText: string;
  otherLabel: string;
  /** When true, otherText is the newer version (green) and currentText is the older one (red). */
  reversed?: boolean;
}

/** Renders a diff block, reusing the memory module's diff builder and styles. */
export function TextDiffView({ currentText, otherText, otherLabel, reversed }: TextDiffViewProps) {
  const diffLines = useMemo(
    () => reversed
      ? buildDiffLinesWithInline(currentText, otherText)
      : buildDiffLinesWithInline(otherText, currentText),
    [currentText, otherText, reversed],
  );

  return (
    <div className='plugin-slot__version-diff'>
      <div className='plugin-slot__version-diff-header'>
        {reversed ? (
          <>
            <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--remove'>
              {tr('chat.slots.currentVersion')}
            </span>
            <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--add'>
              {otherLabel}
            </span>
          </>
        ) : (
          <>
            <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--remove'>
              {otherLabel}
            </span>
            <span className='plugin-slot__version-diff-label plugin-slot__version-diff-label--add'>
              {tr('chat.slots.currentVersion')}
            </span>
          </>
        )}
      </div>
      <div className='plugin-slot__version-diff-body'>
        {diffLines.map((line, index) => (
          <div
            key={`${index}-${line.type}-${line.text.slice(0, 20)}`}
            className={`memory-diff-line is-${line.type}`}
          >
            <span className='memory-diff-prefix'>
              {line.type === 'add' ? '+' : line.type === 'remove' ? '-' : ' '}
            </span>
            <DiffLineContent line={line} />
          </div>
        ))}
        {diffLines.length === 0 && (
          <div className='plugin-slot__version-diff-empty'>{tr('chat.slots.identicalContent')}</div>
        )}
      </div>
    </div>
  );
}

const snapshotDiffTextCache = new Map<string, Promise<string>>();

function snapshotDiffCacheKey(snapshot: unknown): string {
  if (snapshot == null) return '';
  if (typeof snapshot === 'string') return `s:${snapshot}`;
  if (typeof snapshot !== 'object') return `v:${String(snapshot)}`;
  const record = snapshot as Record<string, unknown>;
  if (record.url || record.path) {
    return `f:${String(record.url ?? '')}\n${String(record.path ?? '')}\n${String(record.size ?? '')}\n${String(record.filename ?? record.name ?? '')}`;
  }
  try {
    return `j:${JSON.stringify(snapshot)}`;
  } catch {
    return `o:${Object.keys(record).join(',')}`;
  }
}

export function formatPayloadForDiff(payload: unknown): string {
  const unwrapped = unwrapArtifactPayload(payload);
  if (isWriterDocument(unwrapped)) {
    return writerDocumentToMarkdown(unwrapped);
  }
  if (isWriterDocument(payload)) {
    return writerDocumentToMarkdown(payload);
  }
  if (typeof unwrapped === 'string') return unwrapped;
  if (unwrapped != null) return JSON.stringify(unwrapped, null, 2);
  if (typeof payload === 'string') return payload;
  return payload == null ? '' : JSON.stringify(payload, null, 2);
}

async function resolveSnapshotDiffText(snapshot: unknown): Promise<string> {
  if (snapshot == null) return '';
  if (typeof snapshot === 'string') {
    const trimmed = snapshot.trim();
    if (
      trimmed.startsWith('{')
      || trimmed.startsWith('[')
      || (!trimmed.includes('/static-files/')
        && !trimmed.includes('/api/core/')
        && !trimmed.startsWith('http')
        && !trimmed.startsWith('/var/'))
    ) {
      return snapshot;
    }
    const fetchUrl = trimmed.includes('/static-files/') || trimmed.startsWith('http')
      ? resolveCoreAssetUrl(trimmed)
      : await resolveMarkdownImageUrlAsync(trimmed);
    if (!fetchUrl) return snapshot;
    const response = await fetch(fetchUrl);
    if (!response.ok) throw new Error(localizeErrorCode('2000509'));
    const contentType = response.headers.get('content-type') || '';
    if (contentType.includes('json') || trimmed.toLowerCase().includes('.json')) {
      return formatPayloadForDiff(await response.json());
    }
    const text = await response.text();
    if (isSpaFallbackHtml(text)) throw new Error(localizeErrorCode('2000509'));
    return text;
  }

  if (typeof snapshot !== 'object') return String(snapshot);
  const record = snapshot as Record<string, unknown>;
  if (record.text !== undefined) return String(record.text);
  if (record.data !== undefined) {
    return typeof record.data === 'string'
      ? record.data
      : JSON.stringify(record.data, null, 2);
  }
  if (isWriterDocument(record) || isWriterDocument(unwrapArtifactPayload(record))) {
    return formatPayloadForDiff(record);
  }

  const pathForSign = String(record.url ?? record.path ?? '').trim();
  if (!pathForSign) return JSON.stringify(record, null, 2);

  const apiUrl = record.url ? resolveCoreAssetUrl(String(record.url)) : '';
  const fetchUrl = apiUrl && !isExpiredSignedUrl(apiUrl)
    ? apiUrl
    : await resolveMarkdownImageUrlAsync(pathForSign);
  if (!fetchUrl) throw new Error(localizeErrorCode('2000509'));

  const response = await fetch(fetchUrl);
  if (!response.ok) throw new Error(localizeErrorCode('2000509'));
  const contentType = response.headers.get('content-type') || '';
  const looksJson = contentType.includes('json')
    || pathForSign.toLowerCase().includes('.json')
    || String(record.type ?? '').toLowerCase() === 'json'
    || String(record.filename ?? '').toLowerCase().endsWith('.json');
  if (looksJson) {
    return formatPayloadForDiff(await response.json());
  }
  const text = await response.text();
  if (isSpaFallbackHtml(text)) throw new Error(localizeErrorCode('2000509'));
  return text;
}

export function loadSnapshotDiffText(snapshot: unknown): Promise<string> {
  const key = snapshotDiffCacheKey(snapshot);
  if (!key) return Promise.resolve('');
  const cached = snapshotDiffTextCache.get(key);
  if (cached) return cached;
  const pending = resolveSnapshotDiffText(snapshot).catch((error) => {
    snapshotDiffTextCache.delete(key);
    throw error;
  });
  snapshotDiffTextCache.set(key, pending);
  return pending;
}

function useResolvedSnapshotText(snapshot: unknown) {
  const [text, setText] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    loadSnapshotDiffText(snapshot)
      .then((resolved) => {
        if (!cancelled) {
          setText(resolved);
          setLoading(false);
        }
      })
      .catch((loadError: unknown) => {
        if (!cancelled) {
          setText('');
          setLoading(false);
          setError(
            loadError instanceof Error ? loadError.message : localizeErrorCode('2000509'),
          );
        }
      });
    return () => {
      cancelled = true;
    };
  }, [snapshot]);

  return { text, loading, error };
}

export function SnapshotTextPreview({ snapshot }: { snapshot: unknown }) {
  const { text, loading, error } = useResolvedSnapshotText(snapshot);
  if (loading) {
    return <div className='plugin-slot__version-compare-hint'>{tr('common.loading')}</div>;
  }
  if (error) {
    return <div className='plugin-slot__version-compare-hint'>{error || tr('chat.slots.contentLoadFailed')}</div>;
  }
  return (
    <pre className='plugin-slot__version-current-text'>
      {text || tr('chat.slots.noContent')}
    </pre>
  );
}

interface SnapshotTextDiffViewProps {
  currentSnapshot: unknown;
  otherSnapshot?: unknown;
  otherText?: string;
  otherLabel: string;
  reversed?: boolean;
}

export function SnapshotTextDiffView({
  currentSnapshot,
  otherSnapshot,
  otherText,
  otherLabel,
  reversed,
}: SnapshotTextDiffViewProps) {
  const [currentText, setCurrentText] = useState('');
  const [resolvedOtherText, setResolvedOtherText] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    Promise.all([
      loadSnapshotDiffText(currentSnapshot),
      otherText !== undefined
        ? Promise.resolve(otherText)
        : loadSnapshotDiffText(otherSnapshot),
    ])
      .then(([current, other]) => {
        if (!cancelled) {
          setCurrentText(current);
          setResolvedOtherText(other);
          setLoading(false);
        }
      })
      .catch((loadError: unknown) => {
        if (!cancelled) {
          setCurrentText('');
          setResolvedOtherText('');
          setLoading(false);
          setError(
            loadError instanceof Error ? loadError.message : localizeErrorCode('2000509'),
          );
        }
      });
    return () => {
      cancelled = true;
    };
  }, [currentSnapshot, otherSnapshot, otherText]);

  if (loading) {
    return <div className='plugin-slot__version-compare-hint'>{tr('common.loading')}</div>;
  }
  if (error) {
    return <div className='plugin-slot__version-compare-hint'>{error || tr('chat.slots.contentLoadFailed')}</div>;
  }
  return (
    <TextDiffView
      currentText={currentText}
      otherText={resolvedOtherText}
      otherLabel={otherLabel}
      reversed={reversed}
    />
  );
}
