import { useEffect, useRef, useState, type CSSProperties, type ReactElement } from 'react';
import { Popover, Select, type TooltipRef } from 'antd';
import { useTranslation } from 'react-i18next';
import type { DocumentProviderTemplate } from '@/api/generated/core-client';
import cleanHtml from '@/modules/chat/assets/wechat-templates/clean.html?raw';
import editorialContrastHtml from '@/modules/chat/assets/wechat-templates/editorial_contrast.html?raw';
import warmHumanistHtml from '@/modules/chat/assets/wechat-templates/warm_humanist.html?raw';
import './WeChatTemplateSelect.scss';

const samples = [
  { id: 'clean', accent: '#52766f', paper: '#f1f7f5', html: cleanHtml },
  { id: 'editorial_contrast', accent: '#202b33', paper: '#eef2f3', html: editorialContrastHtml },
  { id: 'warm_humanist', accent: '#b97545', paper: '#fff9ef', html: warmHumanistHtml },
];

function TemplatePreview({ html, title, disabled, children }: {
  html: string; title: string; disabled: boolean; children: ReactElement;
}) {
  const { t } = useTranslation();
  const trigger = useRef<TooltipRef>(null);
  const [open, setOpen] = useState(false);
  const [placement, setPlacement] = useState<'leftTop' | 'top'>('leftTop');
  useEffect(() => { if (disabled) setOpen(false); }, [disabled]);
  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape') close(); };
    document.addEventListener('keydown', onKeyDown);
    window.addEventListener('resize', close);
    return () => { document.removeEventListener('keydown', onKeyDown); window.removeEventListener('resize', close); };
  }, [open]);

  const changeOpen = (next: boolean) => {
    if (disabled) { setOpen(false); return; }
    const rect = trigger.current?.nativeElement?.getBoundingClientRect();
    if (next) setPlacement(rect && (rect.left >= 336 || window.innerWidth - rect.right >= 336) ? 'leftTop' : 'top');
    setOpen(next);
  };
  return <Popover ref={trigger} open={open && !disabled} onOpenChange={changeOpen} trigger={disabled ? [] : ['hover', 'focus']}
    placement={placement} arrow={false} zIndex={1080} mouseEnterDelay={0.15} mouseLeaveDelay={0.12} destroyOnHidden
    autoAdjustOverflow={{ adjustX: 1, adjustY: 1, shiftX: 12, shiftY: 12 }}
    classNames={{ root: 'wechat-template-preview-popover' }}
    content={<section className='wechat-template-preview' aria-label={t('chat.publication.wechatTemplatePreview')}
      onMouseDown={event => { event.preventDefault(); event.stopPropagation(); }}
      onClick={event => event.stopPropagation()}>
      <header><strong>{title}</strong></header>
      <div className='wechat-template-preview-article' role='article' aria-label={title} tabIndex={0}
        dangerouslySetInnerHTML={{ __html: html }} />
    </section>}>
    <span className='wechat-template-preview-trigger'>{children}</span>
  </Popover>;
}

export function WeChatTemplateSelect({ id, templates, value, disabled, onChange }: {
  id: string; templates: DocumentProviderTemplate[]; value: string; disabled: boolean; onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const label = (item: DocumentProviderTemplate) => t(`chat.publication.wechatTemplates.${item.id}`, { defaultValue: item.name });
  return <div className='wechat-template-select'>
    <Select id={id} aria-label={t('chat.publication.wechatTemplate')} value={value} disabled={disabled}
      onChange={onChange} onOpenChange={setExpanded} virtual={false} classNames={{ popup: { root: 'wechat-template-dropdown' } }}
      options={templates.map(item => ({ value: item.id, label: label(item) }))}
      optionRender={option => {
        const sample = samples.find(item => item.id === option.value);
        if (!sample) return option.label;
        const title = String(option.label);
        return <TemplatePreview html={sample.html} title={title} disabled={disabled || !expanded}>
          <span className='wechat-template-option' style={{ '--template-accent': sample.accent, '--template-paper': sample.paper } as CSSProperties}>
            <span className='wechat-template-miniature' aria-hidden='true'><i /><i /><i /><i /></span>
            <span className='wechat-template-option-copy'><strong>{title}</strong><small>{t(`chat.publication.wechatTemplateDescriptions.${sample.id}`)}</small></span>
          </span>
        </TemplatePreview>;
      }} />
  </div>;
}
