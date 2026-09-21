import { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Drawer } from 'antd';
import { useTranslation } from 'react-i18next';
import './ArtifactSourceButton.scss';

/** Inspect the displayed value without unmounting an editor or changing its draft. */
export function ArtifactSourceButton({ value, sourceUrl, fileRecord = false, overlay = false }: {
  value: unknown;
  sourceUrl?: string;
  fileRecord?: boolean;
  overlay?: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [loaded, setLoaded] = useState<{ url: string; text: string }>();
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const returnFocus = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (!open || !sourceUrl) return;
    const controller = new AbortController();
    setLoaded(undefined);
    setFailed(false);
    void fetch(sourceUrl, { signal: controller.signal, credentials: 'same-origin' })
      .then(response => {
        if (!response.ok) throw new Error('source unavailable');
        return response.text();
      })
      .then(text => { if (!controller.signal.aborted) setLoaded({ url: sourceUrl, text }); })
      .catch(() => { if (!controller.signal.aborted) setFailed(true); });
    return () => controller.abort();
  }, [open, sourceUrl, attempt]);
  const source = useMemo(() => open
    ? typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? ''
    : '', [open, value]);

  return <>
    <button type='button'
      className={`artifact-source-button${overlay ? ' artifact-source-button--overlay' : ''}`}
      aria-haspopup='dialog'
      onClick={event => {
        event.stopPropagation();
        const menu = event.currentTarget.closest('details');
        returnFocus.current = menu?.querySelector('summary') ?? event.currentTarget;
        if (menu) menu.open = false;
        setOpen(true);
      }}>
      {t('chat.writerSource.showSource')}
    </button>
    <Drawer open={open} title={t('chat.writerSource.source')} width='min(840px, 100vw)'
      styles={{ body: { display: 'flex', flexDirection: 'column', gap: 12 } }}
      onClose={() => setOpen(false)}
      afterOpenChange={(visible: boolean) => { if (!visible) returnFocus.current?.focus({ preventScroll: true }); }}
      footer={<Button onClick={() => setOpen(false)}>{t('chat.writerSource.backToContent')}</Button>}>
      {fileRecord && <p className='artifact-source-note'>{t('chat.writerSource.fileRecord')}</p>}
      {sourceUrl && loaded?.url !== sourceUrl ? failed
        ? <div role='alert'>{t('chat.slots.contentLoadFailed')} <Button onClick={() => setAttempt(value => value + 1)}>{t('common.retry')}</Button></div>
        : <div role='status'>{t('common.loading')}</div>
        : <textarea className='artifact-source-input' aria-label={t('chat.writerSource.source')}
          readOnly spellCheck={false} value={sourceUrl ? loaded?.text ?? '' : source} />}
    </Drawer>
  </>;
}
