import { useEffect, useState, useRef } from 'react';
import { AppstoreOutlined, ArrowRightOutlined, CheckOutlined, FileTextOutlined, LeftOutlined, RightOutlined } from '@ant-design/icons';
import { Popover, Spin } from 'antd';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { listShowcaseCases, type ShowcaseCase } from '@/modules/showcase/api';
import { matchesShowcaseEntryType, showcasePagePath } from '@/modules/showcase/classification';
import { useFeaturedCapabilityBinding } from '@/modules/showcase/useFeaturedCapabilityBinding';
import { useFeaturedSelectionStore } from '@/modules/chat/store/featuredSelection';
import type { ShowcaseSelection } from './index';
import type { ChatMention } from './MentionEditor';

export default function FeaturedCapabilityMenu({ menuHeight, selectionKey, controls, runInBackground, open, onOpenChange, disabled, onBindingChange, onPromptChange }: {
  menuHeight?: number;
  selectionKey: string;
  controls?: ShowcaseSelection;
  runInBackground: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  disabled: boolean;
  onBindingChange: (mentions: ChatMention[], busy: boolean) => void;
  onPromptChange: (prompt: string) => void;
}) {
  const { t } = useTranslation();
  const selection = useFeaturedSelectionStore(state => state.selections[selectionKey]);
  const setSelection = useFeaturedSelectionStore(state => state.setSelection);
  const [cases, setCases] = useState<ShowcaseCase[]>([]);
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading');
  const [attempt, setAttempt] = useState(0);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [level, setLevel] = useState<'case' | 'task'>('case');
  useEffect(() => { setLevel('case'); }, [selectionKey, runInBackground]);
  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => menuRef.current?.querySelector<HTMLElement>('[role=option]')?.focus());
    return () => cancelAnimationFrame(frame);
  }, [level, open, status]);
  const selected = cases.find(item => item.id === selection?.caseId);
  const binding = useFeaturedCapabilityBinding(controls ? null : selected);

  useEffect(() => {
    if (controls || !open && !selection || cases.length > 0) return;
    const controller = new AbortController();
    setStatus('loading');
    listShowcaseCases({}, { signal: controller.signal }).then(result => {
      if (!controller.signal.aborted) { setCases(result.cases || []); setStatus('ready'); }
    }).catch(() => { if (!controller.signal.aborted) setStatus('error'); });
    return () => controller.abort();
  }, [controls, open, Boolean(selection), attempt]);

  useEffect(() => {
    if (controls || !selection) onBindingChange([], false);
    else {
      const matches = selected?.type === 'workflow'
        ? binding.mentions.some(mention => mention.resource_id === selected.workflow_ref)
        : !selected?.builtin_skill_uid || binding.mentions.some(mention => mention.mention_id.startsWith(`featured:${selected.builtin_skill_uid}:`));
      const busy = Boolean(selection) && (status !== 'ready' || !selected || !matches || binding.status === 'preparing' || binding.status === 'failed');
      onBindingChange(matches ? binding.mentions : [], busy);
    }
  }, [controls, binding.mentions, binding.status, onBindingChange, selection, selected, status]);

  useEffect(() => {
    if (controls) setSelection(selectionKey, controls.skill.value ? { caseId: controls.skill.value, taskId: controls.task?.value } : undefined);
  }, [controls?.skill.value, controls?.task?.value, selectionKey, setSelection]);

  const options = controls?.skill.options ?? cases.filter(item => item.featured && matchesShowcaseEntryType(item.type, runInBackground ? 'work' : 'chat')).sort((a, b) => a.featured_order - b.featured_order).slice(0, 6).map(item => ({ value: item.id, label: item.title, description: item.description, pageAction: item.page_action }));
  const taskOptions: ShowcaseSelection['skill']['options'] = controls?.task?.options ?? [{ value: '', label: t('showcase.fullCapability') }, ...(selected?.tasks || []).map(task => ({ value: task.id, label: task.title, description: task.description }))];
  const title = controls?.skill.selectedLabel ?? selected?.title;
  const selectedTaskId = controls?.task?.value ?? (selected?.tasks.some(task => task.id === selection?.taskId) ? selection?.taskId : '');

  const chooseTask = (taskId: string) => {
    if (!selection) return;
    setSelection(selectionKey, { ...selection, taskId });
    if (controls?.task) controls.task.onChange(taskId);
    else if (selected) onPromptChange(selected.tasks.find(task => task.id === taskId)?.prompt || (selected.tasks.length === 1 ? selected.tasks[0].prompt : t('showcase.fullCapabilityPrompt', { title: selected.title, capabilities: selected.tasks.map(task => task.title).join(t('showcase.capabilitySeparator')) })));
    onOpenChange(false);
    triggerRef.current?.focus();
  };

  return <Popover styles={{ body: { height: menuHeight } }} arrow={false} trigger='click' placement='topLeft' open={open && !disabled} onOpenChange={onOpenChange}
    overlayClassName='chat-composer-menu chat-featured-popover' content={<div ref={menuRef} className='chat-featured-menu'>
      <div className='chat-featured-heading'>
        {level === 'task' ? <button className='chat-featured-back' type='button' onClick={() => setLevel('case')} aria-label={t('chat.featuredBack')}><LeftOutlined aria-hidden /></button> : null}
        <div className='chat-featured-heading-copy'>
          <strong>{level === 'task' ? title : t('chat.featuredCapabilities')}</strong>
          <small>{level === 'task' ? t('showcase.chooseTask') : t('chat.featuredSelectionHint')}</small>
        </div>
      </div>
      {!controls && status === 'loading' ? <div className='chat-featured-state'><Spin /></div> : !controls && status === 'error' ? <div className='chat-featured-state' role='alert'>{t('chat.mentionLoadFailed')}<button className='chat-featured-retry' type='button' onClick={() => setAttempt(n => n + 1)}>{t('common.retry')}</button></div> :
        <div className='chat-featured-options' role='listbox' aria-label={level === 'case' ? t('chat.featuredCapabilities') : t('showcase.chooseTask')}>
          {(level === 'case' ? options : taskOptions).map(option => {
            const pagePath = level === 'case'
              ? showcasePagePath(option.pageAction, runInBackground ? 'work' : 'chat') : undefined;
            const content = <>
              <span className='chat-featured-option-icon' aria-hidden>{level === 'case' ? <AppstoreOutlined /> : <FileTextOutlined />}</span>
              <span className='chat-featured-option-copy'><strong>{option.label}</strong>{option.description ? <small title={option.description}>{option.description}</small> : null}</span>
              <span className='chat-featured-option-trailing' aria-hidden>{level === 'case' ? <RightOutlined /> : selectedTaskId === option.value ? <CheckOutlined /> : null}</span>
            </>;
            return pagePath ? <Link role='option' key={option.value} aria-selected={false} to={pagePath} onClick={() => onOpenChange(false)}>{content}</Link> : (
              <button type='button' role='option' key={option.value} aria-selected={level === 'case' ? selection?.caseId === option.value : selectedTaskId === option.value}
                onClick={() => {
                  if (level === 'task') chooseTask(option.value);
                  else { if (!controls) onBindingChange([], true); setSelection(selectionKey, { caseId: option.value }); controls?.skill.onChange(option.value); setLevel('task'); }
                }}>{content}</button>
            );
          })}
          {level === 'case' && !options.length ? <p>{t('chat.mentionNoResults')}</p> : null}
        </div>}
      {binding.status === 'failed' ? <button className='chat-featured-retry' type='button' onClick={binding.retry}>{t('showcase.retryCapabilityPrepare')}</button> : null}
      {selection || level === 'case' && (!controls || controls.skill.onMore) ? <div className='chat-featured-footer'>
        {level === 'case' && controls?.skill.onMore ? <button className='chat-featured-explore' type='button' onClick={() => { controls.skill.onMore?.(); onOpenChange(false); }}>{controls.skill.moreLabel}<ArrowRightOutlined aria-hidden /></button> : null}
        {level === 'case' && !controls ? <a className='chat-featured-explore' href={`/agent/chat/cases?showcase_entry=${runInBackground ? 'work' : 'chat'}`}>{t('showcase.exploreMoreCapabilities')}<ArrowRightOutlined aria-hidden /></a> : null}
        {selection ? <button className='chat-featured-clear' type='button' onClick={() => { controls?.onClear?.(); setSelection(selectionKey); onBindingChange([], false); setLevel('case'); onOpenChange(false); }}>{t('showcase.clearCase')}</button> : null}
      </div> : null}
    </div>}>
    <button ref={triggerRef} type='button' className='input-bottom-actions-left-item' disabled={disabled} aria-label={t('chat.featuredCapabilities')} aria-expanded={open} title={title}>
      <AppstoreOutlined />{t('chat.featuredCapabilities')}{title ? <span className='chat-featured-current'>{title}</span> : null}
    </button>
  </Popover>;
}
