import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Image, Modal, message } from 'antd';
import {
  DownloadOutlined,
  FileTextOutlined,
  LeftOutlined,
  RightOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';

import { useTaskCenterStore, type ConversationArtifact } from '@/modules/chat/store/taskCenter';
import {
  artifactFileKey,
  artifactSourceKey,
  downloadArtifactToDisk,
  extractTextContent,
  formatFileSize,
  toArtifactFiles,
  type ArtifactFile,
} from '@/modules/chat/components/ArtifactCollectorCard/artifactFiles';
import {
  ArtifactV2Api,
  type ArtifactRevisionItem,
} from '@/modules/chat/utils/request';
import { downloadStream } from '@/modules/chat/utils/download';
import './index.scss';

const EMPTY_ARTIFACTS: ConversationArtifact[] = [];

interface Props {
  sessionId: string;
  turnHistoryId?: string;
  onClose?: () => void;
  showHeader?: boolean;
  onPreviewLayoutChange?: (layout: PreviewLayout) => void;
}

type PreviewLayout = 'down' | 'right';

const TEXT_FILE_PATTERN =
  /\.(md|markdown|txt|json|csv|ya?ml|xml|html?|css|jsx?|tsx?|py|go|java|sql|sh|log)$/i;
const MAX_PREVIEW_CHARACTERS = 200_000;

function isTextRevision(contentType?: string, filename?: string): boolean {
  const type = (contentType || '').toLowerCase();
  return type.includes('text') || type.includes('json') || type.includes('markdown')
    || TEXT_FILE_PATTERN.test(filename || '');
}

function canPreviewText(file: ArtifactFile): boolean {
  return isTextRevision(file.artifact.content_type, file.filename);
}

function fileMeta(file: ArtifactFile, t: (key: string) => string): string {
  const origin = file.origin === 'upload'
    ? t('chat.artifactPanelUploaded')
    : `${t(artifactSourceKey(file.sourceType))} · ${t('chat.artifactPanelGenerated')}`;
  return file.size != null && file.size > 0
    ? `${origin} · ${formatFileSize(file.size)}`
    : origin;
}

export default function ArtifactPanel({
  sessionId,
  onClose,
  showHeader = true,
  onPreviewLayoutChange,
}: Props) {
  const { t } = useTranslation();
  const artifacts = useTaskCenterStore(
    (state) => state.artifactsByConversation[sessionId] ?? EMPTY_ARTIFACTS,
  );
  const loadConversationArtifacts = useTaskCenterStore(
    (state) => state.loadConversationArtifacts,
  );
  const files = useMemo(() => toArtifactFiles(artifacts), [artifacts]);
  const uploads = useMemo(
    () => files.filter((file) => file.origin === 'upload'),
    [files],
  );
  const published = useMemo(
    () => files.filter((file) => file.origin === 'published'),
    [files],
  );
  const [selectedId, setSelectedId] = useState<string>();
  const [view, setView] = useState<'detail' | 'versions'>('detail');
  const [previewLayout, setPreviewLayout] = useState<PreviewLayout>('down');

  const changePreviewLayout = useCallback((layout: PreviewLayout) => {
    setPreviewLayout(layout);
    onPreviewLayoutChange?.(layout);
  }, [onPreviewLayoutChange]);

  useEffect(() => {
    void loadConversationArtifacts(sessionId);
  }, [loadConversationArtifacts, sessionId]);

  useEffect(() => {
    if (selectedId && !files.some((file) => artifactFileKey(file) === selectedId)) {
      setSelectedId(undefined);
      setView('detail');
      changePreviewLayout('down');
    }
  }, [changePreviewLayout, files, selectedId]);

  const selected = files.find((file) => artifactFileKey(file) === selectedId);

  const downloadFile = useCallback(async (file: ArtifactFile) => {
    const ok = await downloadArtifactToDisk(file);
    if (!ok) {
      message.error(t('chat.artifactCollectorDownloadFailed', { filename: file.filename }));
    }
  }, [t]);

  return (
    <div className="artifact-panel">
      {showHeader && (
        <div className="artifact-panel__header">
          <span className="artifact-panel__title">
            {t('chat.artifactPanelTitle')}
            <span className="artifact-panel__count">{files.length}</span>
          </span>
          {onClose && (
            <button
              type="button"
              className="artifact-panel__close"
              onClick={onClose}
              aria-label={t('common.close')}
            >
              <RightOutlined />
            </button>
          )}
        </div>
      )}

      {selected ? (
        view === 'versions' ? (
          <ArtifactVersions
            file={selected}
            sessionId={sessionId}
            onBack={() => setView('detail')}
            onRestored={() => {
              void loadConversationArtifacts(sessionId);
              setView('detail');
            }}
          />
        ) : (
          <ArtifactDetail
            file={selected}
            previewLayout={previewLayout}
            onPreviewLayoutChange={changePreviewLayout}
            onBack={() => {
              changePreviewLayout('down');
              setSelectedId(undefined);
            }}
            onDownload={() => void downloadFile(selected)}
            onOpenVersions={() => setView('versions')}
          />
        )
      ) : files.length === 0 ? (
        <div className="artifact-panel__empty">{t('chat.artifactPanelEmpty')}</div>
      ) : (
        <div className="artifact-panel__content">
          <p className="artifact-panel__intro">{t('chat.artifactPanelVisibleHint')}</p>
          <ArtifactGroup
            title={t('chat.artifactPanelUploads')}
            files={uploads}
            onSelect={(id) => {
              changePreviewLayout('down');
              setView('detail');
              setSelectedId(id);
            }}
          />
          <ArtifactGroup
            title={t('chat.artifactPanelPublished')}
            files={published}
            onSelect={(id) => {
              changePreviewLayout('down');
              setView('detail');
              setSelectedId(id);
            }}
          />
        </div>
      )}
    </div>
  );
}

function ArtifactGroup({
  title,
  files,
  onSelect,
}: {
  title: string;
  files: ArtifactFile[];
  onSelect: (id: string) => void;
}) {
  const { t } = useTranslation();
  if (files.length === 0) return null;
  return (
    <section className="artifact-panel__group" aria-label={title}>
      <h3 className="artifact-panel__group-title">{title}</h3>
      <div className="artifact-panel__list" role="list">
        {files.map((file) => {
          const key = artifactFileKey(file);
          return (
            <button
              type="button"
              role="listitem"
              key={key}
              className="artifact-panel__item"
              aria-label={`${file.filename} ${fileMeta(file, t)}`}
              onClick={() => onSelect(key)}
            >
              <span className="artifact-panel__item-icon" aria-hidden>
                <FileTextOutlined />
              </span>
              <span className="artifact-panel__item-copy">
                <span className="artifact-panel__item-name">{file.filename}</span>
                <span className="artifact-panel__item-meta">{fileMeta(file, t)}</span>
              </span>
              <RightOutlined className="artifact-panel__item-chevron" aria-hidden />
            </button>
          );
        })}
      </div>
    </section>
  );
}

function ArtifactDetail({
  file,
  onBack,
  onDownload,
  onOpenVersions,
  previewLayout,
  onPreviewLayoutChange,
}: {
  file: ArtifactFile;
  onBack: () => void;
  onDownload: () => void;
  onOpenVersions: () => void;
  previewLayout: PreviewLayout;
  onPreviewLayoutChange: (layout: PreviewLayout) => void;
}) {
  const { t } = useTranslation();
  const revision = file.artifact.revision || file.revision || 1;
  const count = file.artifact.revision_count;
  const showVersions =
    file.origin === 'published' &&
    (file.sourceType === 'main_chat' || file.sourceType === 'subagent') &&
    (count ?? 0) > 0;
  const showPreviewLayout = canPreviewText(file);
  return (
    <div
      data-testid="artifact-detail"
      className={`artifact-panel__detail${previewLayout === 'right' ? ' artifact-panel__detail--preview-right' : ''}`}
    >
      <div className="artifact-panel__detail-topline">
        <button type="button" className="artifact-panel__back" onClick={onBack}>
          <LeftOutlined aria-hidden />
          {t('chat.artifactPanelBack')}
        </button>
        {showPreviewLayout && (
          <div
            className="artifact-panel__preview-layout"
            role="group"
            aria-label={t('chat.artifactPanelPreviewLayout')}
          >
            <button
              type="button"
              className={`artifact-panel__preview-layout-button${previewLayout === 'down' ? ' artifact-panel__preview-layout-button--active' : ''}`}
              aria-label={t('chat.artifactPanelPreviewDown')}
              title={t('chat.artifactPanelPreviewDown')}
              onClick={() => onPreviewLayoutChange('down')}
            >
              <span aria-hidden>↓</span>
            </button>
            <button
              type="button"
              className={`artifact-panel__preview-layout-button${previewLayout === 'right' ? ' artifact-panel__preview-layout-button--active' : ''}`}
              aria-label={t('chat.artifactPanelPreviewRight')}
              title={t('chat.artifactPanelPreviewRight')}
              onClick={() => onPreviewLayoutChange('right')}
            >
              <RightOutlined aria-hidden />
            </button>
          </div>
        )}
      </div>
      <div className="artifact-panel__detail-body">
        <div className="artifact-panel__detail-heading">
          <div className="artifact-panel__detail-name">{file.filename}</div>
          <div className="artifact-panel__detail-meta">{fileMeta(file, t)}</div>
          {showVersions && (
            <div className="artifact-panel__detail-meta">
              {t('chat.artifactPanelCurrentRevision', { revision })}
            </div>
          )}
        </div>
        <div className="artifact-panel__preview">
          <ArtifactPreview file={file} />
        </div>
        <div className="artifact-panel__actions">
          <Button type="primary" icon={<DownloadOutlined />} onClick={onDownload}>
            {t('chat.artifactCollectorDownload')}
          </Button>
          {showVersions && (
            <Button onClick={onOpenVersions}>
              {t('chat.artifactPanelVersionHistory', { count: count || 1 })}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

function ArtifactVersions({
  file,
  sessionId,
  onBack,
  onRestored,
}: {
  file: ArtifactFile;
  sessionId: string;
  onBack: () => void;
  onRestored: () => void;
}) {
  const { t } = useTranslation();
  const [revisions, setRevisions] = useState<ArtifactRevisionItem[]>([]);
  const [diff, setDiff] = useState<{ from: string; to: string } | null>(null);
  const artifactId = file.artifact.v2_artifact_id || file.artifact.artifact_id;
  const canManageVersions = file.sourceType === 'main_chat';

  useEffect(() => {
    let cancelled = false;
    void ArtifactV2Api().listRevisions(artifactId).then((res) => {
      if (cancelled) return;
      const rows = (res?.data?.data?.revisions ?? res?.data?.revisions ?? []) as ArtifactRevisionItem[];
      setRevisions(Array.isArray(rows) ? rows : []);
    }).catch(() => {
      if (!cancelled) setRevisions([]);
    });
    return () => {
      cancelled = true;
    };
  }, [artifactId, sessionId]);

  const restore = (revision: ArtifactRevisionItem) => {
    Modal.confirm({
      title: t('chat.artifactPanelRestoreConfirmTitle'),
      content: t('chat.artifactPanelRestoreConfirm', { revision: revision.revision_no }),
      okText: t('chat.artifactPanelRestore'),
      onOk: async () => {
        try {
          await ArtifactV2Api().moveHead(artifactId, 'published', {
            revision_id: revision.revision_id,
            version: file.artifact.head_version,
          });
          message.success(t('chat.artifactPanelRestoreDone'));
          onRestored();
        } catch {
          message.error(t('chat.artifactPanelRestoreFailed'));
        }
      },
    });
  };

  const downloadRevision = async (revision: ArtifactRevisionItem) => {
    try {
      const res = await ArtifactV2Api().downloadRevisionUrl(revision.revision_id);
      const payload = res?.data?.data || res?.data || {};
      if (payload.url) {
        const resp = await fetch(payload.url);
        if (!resp.ok) throw new Error('download failed');
        downloadStream(new Blob([await resp.arrayBuffer()]), file.filename);
        return;
      }
      if (payload.inline_json) {
        const body = typeof payload.inline_json === 'string'
          ? payload.inline_json
          : JSON.stringify(payload.inline_json, null, 2);
        downloadStream(new Blob([body]), file.filename);
        return;
      }
      throw new Error('empty');
    } catch {
      message.error(t('chat.artifactCollectorDownloadFailed', { filename: file.filename }));
    }
  };

  const published = revisions.find((item) => item.published) || revisions[revisions.length - 1];
  const compare = async (revision: ArtifactRevisionItem) => {
    if (!published || published.revision_id === revision.revision_id) return;
    if (!isTextRevision(revision.content_type, file.filename)) {
      message.info(t('chat.artifactPanelDiffBinary'));
      return;
    }
    try {
      const res = await ArtifactV2Api().diffRevisions(revision.revision_id, published.revision_id);
      const payload = res?.data?.data || res?.data || {};
      if (!payload.comparable) {
        message.info(t('chat.artifactPanelDiffBinary'));
        return;
      }
      setDiff({ from: String(payload.from || ''), to: String(payload.to || '') });
    } catch {
      message.error(t('chat.artifactPanelDiffFailed'));
    }
  };

  return (
    <div className="artifact-panel__detail">
      <button type="button" className="artifact-panel__back" onClick={onBack}>
        <LeftOutlined aria-hidden />
        {t('chat.artifactPanelBackToDetail')}
      </button>
      <div className="artifact-panel__detail-heading">
        <div className="artifact-panel__detail-name">{file.filename}</div>
        <div className="artifact-panel__detail-meta">
          {t('chat.artifactPanelVersionHistory', { count: revisions.length || file.artifact.revision_count || 1 })}
        </div>
      </div>
      <div className="artifact-panel__list" role="list">
        {revisions.map((revision) => (
          <div key={revision.revision_id} className="artifact-panel__revision" role="listitem">
            <div className="artifact-panel__item-copy">
              <span className="artifact-panel__item-name">
                v{revision.revision_no}
                {revision.published ? ` · ${t('chat.artifactPanelPublishedMark')}` : ''}
              </span>
              <span className="artifact-panel__item-meta">
                {[revision.change_summary, revision.producer_type, revision.created_at]
                  .filter(Boolean)
                  .join(' · ')}
              </span>
            </div>
            <div className="artifact-panel__revision-actions">
              <Button size="small" onClick={() => void downloadRevision(revision)}>
                {t('chat.artifactCollectorDownload')}
              </Button>
              {canManageVersions && !revision.published && (
                <Button size="small" onClick={() => restore(revision)}>
                  {t('chat.artifactPanelRestore')}
                </Button>
              )}
              {canManageVersions && !revision.published && (
                <Button size="small" onClick={() => void compare(revision)}>
                  {t('chat.artifactPanelDiff')}
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
      {diff && (
        <div className="artifact-panel__diff">
          <pre className="artifact-panel__pre">{diff.from}</pre>
          <pre className="artifact-panel__pre">{diff.to}</pre>
        </div>
      )}
    </div>
  );
}

function ArtifactPreview({ file }: { file: ArtifactFile }) {
  const { t } = useTranslation();
  const contentType = file.artifact.content_type;
  if (contentType === 'image' && file.url) {
    return <Image src={file.url} alt={file.filename} />;
  }
  if (contentType === 'text' || contentType === 'json') {
    return <TextPreview content={extractTextContent(file.artifact)} />;
  }
  if (canPreviewText(file) && file.url) {
    return <RemoteTextPreview url={file.url} />;
  }
  return <p className="artifact-panel__file-preview">{t('chat.artifactPanelFilePreviewHint')}</p>;
}

function RemoteTextPreview({ url }: { url: string }) {
  const { t } = useTranslation();
  const [content, setContent] = useState<string>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setContent(undefined);
    setFailed(false);
    void fetch(url, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error('preview request failed');
        return response.text();
      })
      .then((text) => setContent(text))
      .catch((error: unknown) => {
        if ((error as { name?: string }).name !== 'AbortError') setFailed(true);
      });
    return () => controller.abort();
  }, [url]);

  if (failed) {
    return <p className="artifact-panel__file-preview">{t('chat.artifactPanelTextPreviewFailed')}</p>;
  }
  if (content == null) {
    return <p className="artifact-panel__file-preview">{t('chat.artifactPanelTextPreviewLoading')}</p>;
  }
  return <TextPreview content={content} />;
}

function TextPreview({ content }: { content: string }) {
  const visibleContent = content.slice(0, MAX_PREVIEW_CHARACTERS);
  const lines = visibleContent.split('\n');
  const truncated = content.length > visibleContent.length;
  return (
    <div className="artifact-panel__text-preview" aria-label="Text preview">
      {lines.map((line, index) => (
        <div className="artifact-panel__text-line" key={`${index}:${line}`}>
          <span className="artifact-panel__line-number" aria-hidden>{index + 1}</span>
          <code className="artifact-panel__line-code">{line || ' '}</code>
        </div>
      ))}
      {truncated && <div className="artifact-panel__text-truncated">Preview truncated after 200 KB.</div>}
    </div>
  );
}
