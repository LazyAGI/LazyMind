import { useCallback, useContext, useMemo, useState, type ReactNode } from 'react';
import { Dropdown } from 'antd';
import { CloudUploadOutlined, CopyOutlined, DownOutlined, DownloadOutlined, ExportOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { buildDocumentFooterItems } from './documentFooter';
import { SlotEditingContext, type SlotFooterAction, type SlotEditingContextValue } from './slotEditingContext';

export function DocumentActions({ documentFooter, actionPending = false, runFooterAction }: {
  documentFooter: ReturnType<typeof buildDocumentFooterItems>;
  actionPending?: boolean;
  runFooterAction?: SlotEditingContextValue['runFooterAction'];
}) {
  const { t } = useTranslation();
  if (!documentFooter.actionItems.length && !documentFooter.statusMessages.length) return null;
  return (
    <div className='workflow-panel__footer-document'>
      {documentFooter.statusMessages.length > 0 || documentFooter.actionItems.some(item => item.kind === 'link') ? (
        <div className='workflow-panel__footer-meta'>
          {documentFooter.actionItems.filter(item => item.kind === 'link').map(item => item.kind === 'link' && (
            <a key={item.key} className='workflow-panel__footer-link' href={item.href} target='_blank' rel='noopener noreferrer'>
              {item.label}<ExportOutlined aria-hidden />
            </a>
          ))}
          {documentFooter.statusMessages.map((message) => (
            <span
              key={message.key}
              className={
                message.tone === 'error'
                  ? 'workflow-panel__footer-action-status workflow-panel__footer-action-status--error'
                  : message.tone === 'success'
                    ? 'workflow-panel__footer-action-status workflow-panel__footer-action-status--success'
                    : 'workflow-panel__footer-action-status'
              }
              role={message.tone === 'error' ? 'alert' : 'status'}
            >
              {message.text}
            </span>
          ))}
        </div>
      ) : null}
      {documentFooter.actionItems.length > 0 ? (
        <div className='workflow-panel__footer-actions'>
          {documentFooter.actionItems.map((item) => {
            if (item.kind === 'link') return null;

            const { action } = item;
            return (
              <div key={item.key} className={action.menu ? 'workflow-panel__split-action' : undefined}>
                <button
                  key={item.key}
                  type='button'
                  className={`workflow-panel__action-btn workflow-panel__action-btn--${action.tone ?? 'secondary'}`}
                  disabled={actionPending || action.disabled}
                  aria-disabled={actionPending || action.disabled}
                  onClick={() => {
                    if (action.flushBeforeAction && runFooterAction) {
                      void runFooterAction(action.onClick, action.flushKey);
                      return;
                    }
                    action.onClick();
                  }}
                >
                  {action.icon === 'write-back' ? <CloudUploadOutlined aria-hidden /> : null}
                  {action.icon === 'download' ? <DownloadOutlined aria-hidden /> : null}
                  {action.icon === 'copy' ? <CopyOutlined aria-hidden /> : null}
                  {action.label}
                </button>
                {action.menu && (
                  <Dropdown
                    menu={{
                      className: action.selectedMenuKey ? 'workflow-panel__format-menu' : undefined,
                      selectable: Boolean(action.selectedMenuKey),
                      selectedKeys: action.selectedMenuKey ? [action.selectedMenuKey] : [],
                      items: action.menu.map((option) => ({
                        ...option,
                        onClick: () => { if (action.flushBeforeAction && runFooterAction) void runFooterAction(option.onClick, action.flushKey); else option.onClick(); },
                        icon: action.selectedMenuKey
                          ? <span className='workflow-panel__format-radio' aria-hidden='true' />
                          : option.icon,
                      })),
                    }}
                    trigger={['click']}
                    disabled={actionPending || action.disabled}
                  >
                    <button type='button' className={`workflow-panel__action-btn workflow-panel__action-btn--${action.tone ?? 'secondary'}`}
                      disabled={actionPending || action.disabled} aria-label={action.menuLabel ?? t('chat.writerCopy.chooseFormat')}>
                      <DownOutlined />
                    </button>
                  </Dropdown>
                )}
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

/** Keep secondary document actions beside their content while sharing save/pending state. */
export function DocumentActionScope({ inline, label, children }: {
  inline: boolean;
  label: string;
  children: ReactNode;
}) {
  const parent = useContext(SlotEditingContext);
  const [actions, setActions] = useState<Map<string, SlotFooterAction>>(new Map());
  const registerFooterAction = useCallback((key: string, action: SlotFooterAction | null) => {
    setActions(previous => {
      const next = new Map(previous);
      if (action) next.set(key, action);
      else next.delete(key);
      return next;
    });
    return () => setActions(previous => {
      if (previous.get(key) !== action) return previous;
      const next = new Map(previous);
      next.delete(key);
      return next;
    });
  }, []);
  const context = useMemo(() => inline ? { ...parent, registerFooterAction } : parent,
    [inline, parent, registerFooterAction]);
  return <SlotEditingContext.Provider value={context}>
    {inline ? <div className='workflow-panel__document-item' role='group' aria-label={label}>
      {children}
      <div className='workflow-panel__document-item-actions'>
        <DocumentActions documentFooter={buildDocumentFooterItems(actions)}
          actionPending={parent.actionPending} runFooterAction={parent.runFooterAction} />
      </div>
    </div> : children}
  </SlotEditingContext.Provider>;
}
