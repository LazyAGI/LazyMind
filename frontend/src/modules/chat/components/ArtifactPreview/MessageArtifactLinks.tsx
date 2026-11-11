import { lazy, Suspense, useEffect, useMemo } from 'react';
import { Spin } from 'antd';
import { FileTextOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { useTaskCenterStore } from '../../store/taskCenter';
import { useWorkflowStore } from '../../store/workflowPanel';
import { toArtifactFiles } from '../ArtifactCollectorCard/artifactFiles';
import type { ChatExport } from '../newChatContainer/types';
import { exportCitationMarkdown } from '../../utils/chatExportCitations';
import type { ChatSourceCollection } from '../../utils/sourceAdapter';
import { useArtifactPreview } from './context';
import { filePreviewTarget, previewFilename, type ArtifactPreviewTarget } from './target';
import './index.scss';

const InlinePreview = lazy(() => import('./index').then(module => ({ default: module.PreviewContent })));

export default function MessageArtifactLinks({ conversationId, historyId, content, exports = [], sources = [] }: {
  conversationId: string; historyId: string; content: string; exports?: ChatExport[]; sources?: ChatSourceCollection;
}) {
  const { t } = useTranslation();
  const preview = useArtifactPreview();
  const deliveries = useTaskCenterStore(state => state.deliveriesByConversation[conversationId]
    ?? state.artifactsByConversation[conversationId]);
  const session = useWorkflowStore(state => state.sessionByConversation[conversationId]);
  const load = useTaskCenterStore(state => state.loadConversationArtifacts);
  useEffect(() => {
    if (preview && !deliveries && !useTaskCenterStore.getState()._loadingArtifacts[conversationId]) void load(conversationId);
  }, [conversationId, Boolean(preview), deliveries, load]);
  const publishedTargets = useMemo(() => {
    const targets = new Map<string, ArtifactPreviewTarget>();
    if (session?.ordinary_tasks?.some(task => task.trigger_history_id === historyId)) return targets;
    const files = toArtifactFiles((deliveries ?? []).filter(item => item.history_id === historyId))
      .filter(file => file.origin === 'published' && file.sourceType !== 'workflow');
    for (const file of files) {
      const target = filePreviewTarget(file);
      if (!target.workflow) targets.set(file.id, target);
    }
    return targets;
  }, [deliveries, historyId, session]);
  if (!preview || !historyId) return null;
  const isWorkflowTurn = session?.ordinary_tasks?.some(task => task.trigger_history_id === historyId);
  if (isWorkflowTurn) return null;
  // Only explicit publication groups identify alternate formats of one work.
  const visuals: ArtifactPreviewTarget[] = [];
  const groups = new Map<string, number>();
  const priority = (target: ArtifactPreviewTarget) => /\.html?$/i.test(previewFilename(target)) ? 0
    : /\.svg$/i.test(previewFilename(target)) ? 1 : 2;
  for (const target of publishedTargets.values()) {
    if (!/\.(html?|svg|png|jpe?g|gif|webp|bmp)$/i.test(previewFilename(target))) continue;
    const index = target.previewGroup ? groups.get(target.previewGroup) : undefined;
    if (index === undefined) {
      if (target.previewGroup) groups.set(target.previewGroup, visuals.length);
      visuals.push(target);
    } else if (priority(target) < priority(visuals[index])) {
      visuals[index] = target;
    }
  }
  const targets = new Map(publishedTargets);
  // Inline exports are already part of this reply; previewing them must not create a saved artifact.
  let cursor = 0;
  for (const item of exports) {
    if (item.start < cursor || item.end <= item.start || item.end > content.length) continue;
    cursor = item.end;
    if (!targets.has(item.export_id)) targets.set(item.export_id, {
      key: `${conversationId}:${historyId}:${item.export_id}`, conversationId, filename: item.filename,
      content: exportCitationMarkdown(content.slice(item.start, item.end), sources, window.location.origin),
      mimeType: 'text/markdown',
    });
  }
  if (!targets.size) return null;
  return <>
    {visuals.map(target => <div className="message-artifact-preview" key={target.key}>
      <Suspense fallback={<Spin />}><InlinePreview target={target} inline /></Suspense>
    </div>)}
    <ul className="message-artifact-links" aria-label={t('chat.preview.messageArtifacts')}>
    {[...targets].map(([id, target]) => <li key={id}>
      <button type="button" onClick={() => preview.open(target)} aria-label={t('chat.previewNamedFile', { name: target.filename })}>
        <FileTextOutlined aria-hidden="true" />{target.filename}
      </button>
    </li>)}
    </ul>
  </>;
}
