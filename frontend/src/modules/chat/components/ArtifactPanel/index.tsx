import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Image, Modal, message } from 'antd';
import {
  DownloadOutlined,
  FileTextOutlined,
  LeftOutlined,
  RightOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';

import MarkdownViewer from '@/modules/chat/components/MarkdownViewer';
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
}

function isMarkdownFilename(filename: string): boolean {
  return /\.(md|markdown)$/i.test(filename);
}

function isTextRevision(contentType?: string, filename?: string): boolean {
  const type = (contentType || '').toLowerCase();
  return type.includes('text') || type.includes('json') || type.includes('markdown')
    || /\.(md|markdown|txt|json)$/i.test(filename || '');
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

  useEffect(() => {
    void loadConversationArtifacts(sessionId);
  }, [loadConversationArtifacts, sessionId]);

  useEffect(() => {
    if (selectedId && !files.some((file) => artifactFileKey(file) === selectedId)) {
      setSelectedId(undefined);
      setView('detail');
    }
  }, [files, selectedId]);

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
            onBack={() => setSelectedId(undefined)}
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
              setView('detail');
              setSelectedId(id);
            }}
          />
          <ArtifactGroup
            title={t('chat.artifactPanelPublished')}
            files={published}
            onSelect={(id) => {
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
}: {
  file: ArtifactFile;
  onBack: () => void;
  onDownload: () => void;
  onOpenVersions: () => void;
}) {
  const { t } = useTranslation();
  const revision = file.artifact.revision || file.revision || 1;
  const count = file.artifact.revision_count;
  const showVersions = file.origin === 'published' && file.sourceType === 'main_chat' && (count ?? 0) > 0;
  return (
    <div className="artifact-panel__detail">
      <button type="button" className="artifact-panel__back" onClick={onBack}>
        <LeftOutlined aria-hidden />
        {t('chat.artifactPanelBack')}
      </button>
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
              {!revision.published && (
                <Button size="small" onClick={() => restore(revision)}>
                  {t('chat.artifactPanelRestore')}
                </Button>
              )}
              {!revision.published && (
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
    const text = extractTextContent(file.artifact);
    if (contentType === 'text' && isMarkdownFilename(file.filename)) {
      return <MarkdownViewer>{text}</MarkdownViewer>;
    }
    return <pre className="artifact-panel__pre">{text}</pre>;
  }
  return <p className="artifact-panel__file-preview">{t('chat.artifactPanelFilePreviewHint')}</p>;
}
