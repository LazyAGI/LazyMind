import { useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Alert, Button } from 'antd';
import { useTranslation } from 'react-i18next';
import type { RewriteSelectionPreview } from '@/modules/chat/utils/request';
import { ArtifactRewriteInlineDiff, renderInlineDiff } from './ArtifactRewriteDialog';
import { captureRewriteScrollAnchor } from './rewriteScrollAnchor';

/** Review stays in the document without trapping focus or blocking other edits. */
export function ArtifactRewriteBatchPreview({ preview, targets, layer, footerHost, disabled, invalidIndices = [], onApply, onComplete, onCancel }: {
  preview: RewriteSelectionPreview;
  targets?: Array<HTMLElement | null>;
  layer?: HTMLElement | null;
  footerHost?: HTMLElement | null;
  disabled?: boolean;
  invalidIndices?: number[];
  onApply: (indices: number[]) => Promise<unknown>;
  onComplete?: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const [resolved, setResolved] = useState<number[]>([]);
  const running = useRef(false);
  const pending = (preview.results ?? []).map((_, index) => index).filter(index => !resolved.includes(index));
  const valid = pending.filter(index => !invalidIndices.includes(index));
  const title = t('chat.artifactRewrite.batchTitle', { count: pending.length });
  const apply = async (indices: number[]) => {
    if (running.current || disabled || !indices.length || indices.some(index => invalidIndices.includes(index))) return;
    const restoreScroll = captureRewriteScrollAnchor(indices.map(index => targets?.[index]));
    running.current = true; setBusy(true); setFailed(false);
    try {
      await onApply(indices);
      setResolved(current => [...current, ...indices]);
      if (indices.length === pending.length) onComplete?.();
      restoreScroll();
    } catch { setFailed(true); }
    finally { running.current = false; setBusy(false); }
  };
  const reject = (index: number) => {
    if (running.current) return;
    setResolved(current => [...current, index]);
    setFailed(false);
    if (pending.length === 1) onCancel();
  };
  const actions = (index: number) => <div className='artifact-rewrite-inline-diff__actions' role='group'
    aria-label={t('chat.artifactRewrite.batchParagraph', { index: index + 1 })}>
    {invalidIndices.includes(index) && <span role='status'>{t('chat.writerLocal.expired')}</span>}
    <Button size='small' autoInsertSpace={false} disabled={busy} onClick={() => reject(index)}>{t('chat.artifactRewrite.paragraphReject')}</Button>
    <Button size='small' autoInsertSpace={false} type='primary' className='artifact-rewrite-inline-diff__apply' disabled={disabled || busy || invalidIndices.includes(index)} onClick={() => void apply([index])}>{t('chat.artifactRewrite.paragraphApply')}</Button>
  </div>;
  const content = <section className='artifact-rewrite-batch' aria-label={title}>
    <div className='artifact-rewrite-batch__fallback-list'>
    {pending.map(index => {
      const item = preview.results![index];
      const target = targets?.[index];
      return target?.isConnected && layer
        ? <ArtifactRewriteInlineDiff key={item.target.node_id ?? index} target={target} layer={layer}
          sessionId='' slotId='' listIndex={-1} preview={{ ...preview, ...item }} showActions={false}
          reviewActions={actions(index)}
          onApplied={() => {}} onReject={onCancel} />
        : <section key={item.target.node_id ?? index} className='artifact-rewrite-batch__fallback'>
          <h4>{t('chat.artifactRewrite.batchParagraph', { index: index + 1 })}</h4>
          <div>{renderInlineDiff(item.preview.old_text, item.preview.new_text)}</div>
          {actions(index)}
        </section>;
    })}
    </div>
    {failed && <Alert type='error' showIcon message={t('chat.artifactRewrite.batchFailed')} />}
    <div className='artifact-rewrite-batch__toolbar'>
      <span aria-live='polite'>{t('chat.artifactRewrite.batchRemaining', { count: pending.length })}</span>
      <Button disabled={busy} onClick={onCancel}>{t('chat.artifactRewrite.batchReject')}</Button>
      <Button type='primary' disabled={disabled || valid.length === 0} loading={busy} onClick={() => void apply(valid)}>{t(valid.length < pending.length ? 'chat.writerLocal.applyRemaining' : 'chat.artifactRewrite.batchApply', { count: valid.length })}</Button>
    </div>
  </section>;
  return footerHost ? createPortal(content, footerHost) : content;
}
