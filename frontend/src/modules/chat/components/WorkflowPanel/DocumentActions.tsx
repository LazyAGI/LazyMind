import { useContext, useMemo, type ReactNode } from 'react';
import { Dropdown } from 'antd';
import { CloudUploadOutlined, CopyOutlined, DownOutlined, DownloadOutlined, ExportOutlined, FileTextOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { buildDocumentFooterItems } from './documentFooter';
import { SlotEditingContext, type SlotEditingContextValue } from './slotEditingContext';
import { useSlotFooterActions } from './useSlotFooterActions';

export function DocumentActions({ documentFooter, actionPending = false, runFooterAction, compact = false }: {
  documentFooter: ReturnType<typeof buildDocumentFooterItems>;
  actionPending?: boolean;
  runFooterAction?: SlotEditingContextValue['runFooterAction'];
  compact?: boolean;
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
            const resourceAction = compact && (action.icon === 'copy' || action.icon === 'download');
            const label = resourceAction && action.label === t('chat.writerCopy.copyContent')
              ? t('chat.copy') : action.label;
            return (
              <div key={item.key} className={action.menu
                ? `workflow-panel__split-action${resourceAction ? ' workflow-panel__split-action--resource' : ''}` : undefined}>
                <button
                  key={item.key}
                  type='button'
                  className={`workflow-panel__action-btn workflow-panel__action-btn--${action.tone ?? 'secondary'}`}
                  disabled={actionPending || action.disabled}
                  aria-disabled={actionPending || action.disabled}
                  aria-label={action.label}
                  onClick={() => {
                    if (action.flushBeforeAction && runFooterAction) {
                      void runFooterAction(action.onClick, action.flushKey);
                      return;
                    }
                    action.onClick();
                  }}
                >
                  {action.icon === 'write-back' ? <CloudUploadOutlined aria-hidden /> : null}
                  {!resourceAction && action.icon === 'download' ? <DownloadOutlined aria-hidden /> : null}
                  {!resourceAction && action.icon === 'copy' ? <CopyOutlined aria-hidden /> : null}
                  {label}
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

/** Keep each document's actions beside its content while sharing save/pending state. */
export function DocumentActionScope({ inline, label, children, showTitle = true }: {
  inline: boolean;
  label: string;
  children: ReactNode;
  showTitle?: boolean;
}) {
  const parent = useContext(SlotEditingContext);
  const { footerActions, registerFooterAction } = useSlotFooterActions();
  const context = useMemo(() => inline ? { ...parent, registerFooterAction } : parent,
    [inline, parent, registerFooterAction]);
  return <SlotEditingContext.Provider value={context}>
    {inline ? <div className='workflow-panel__document-item' role='group' aria-label={label}>
      <div className='workflow-panel__document-item-header'>
        {showTitle && <div className='workflow-panel__document-item-title' title={label}>
          <FileTextOutlined aria-hidden /><span>{label}</span>
        </div>}
        <div className='workflow-panel__document-item-actions'>
          <DocumentActions compact documentFooter={buildDocumentFooterItems(footerActions)}
            actionPending={parent.actionPending} runFooterAction={parent.runFooterAction} />
        </div>
      </div>
      {children}
    </div> : children}
  </SlotEditingContext.Provider>;
}
