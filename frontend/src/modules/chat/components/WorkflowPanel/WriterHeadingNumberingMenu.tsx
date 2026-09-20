import { Segmented } from 'antd';
import { CheckOutlined } from '@ant-design/icons';
import { useEffect, useId, useLayoutEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import type {
  WriterHeadingNumberingMode,
  WriterNumberingUpdate,
  WriterOrderedHeadingNumberingStyle,
} from '@/modules/chat/utils/request';

interface WriterHeadingNumberingMenuProps {
  x: number;
  y: number;
  targetId: string;
  mode: WriterHeadingNumberingMode;
  orderedStyle: WriterOrderedHeadingNumberingStyle;
  restart: boolean;
  disabled?: boolean;
  onApply: (update: WriterNumberingUpdate) => void;
  onClose: () => void;
}

const ORDERED_STYLE_OPTIONS: Array<{
  value: WriterOrderedHeadingNumberingStyle;
  labelKey: string;
  preview: string;
}> = [
  { value: 'hierarchical', labelKey: 'chat.writerIR.hierarchicalStyle', preview: '1.1' },
  { value: 'chinese', labelKey: 'chat.writerIR.chineseStyle', preview: '（一）' },
  { value: 'parenthesized', labelKey: 'chat.writerIR.parenthesizedStyle', preview: '(a)' },
];

export function WriterHeadingNumberingMenu({
  x,
  y,
  targetId,
  mode,
  orderedStyle,
  restart,
  disabled = false,
  onApply,
  onClose,
}: WriterHeadingNumberingMenuProps) {
  const { t } = useTranslation();
  const rootRef = useRef<HTMLDivElement | null>(null);
  const styleGroupName = useId();

  useLayoutEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const { width, height } = root.getBoundingClientRect();
    root.style.left = `${Math.max(8, Math.min(x + 8, globalThis.innerWidth - width - 8))}px`;
    root.style.top = `${Math.max(8, Math.min(y + 8, globalThis.innerHeight - height - 8))}px`;
  }, [mode, x, y, t]);

  useEffect(() => {
    const close = (event: Event) => {
      if (event instanceof KeyboardEvent && event.key !== 'Escape') return;
      const target = event.target;
      if (
        event.type !== 'keydown'
        && target instanceof Element
        && rootRef.current?.contains(target)
      ) return;
      onClose();
    };
    document.addEventListener('mousedown', close, true);
    document.addEventListener('keydown', close);
    window.addEventListener('resize', close);
    return () => {
      document.removeEventListener('mousedown', close, true);
      document.removeEventListener('keydown', close);
      window.removeEventListener('resize', close);
    };
  }, [onClose]);

  return (
    <div
      ref={rootRef}
      className='writer-numbering-menu'
      role='dialog'
      aria-label={t('chat.writerIR.numberingSettings')}
    >
      <div className='writer-numbering-menu__header'>
        <Segmented
          block
          aria-label={t('chat.writerIR.headingOrder')}
          value={mode}
          disabled={disabled}
          options={[
            { value: 'ordered', label: t('chat.writerIR.orderedHeading') },
            { value: 'unordered', label: t('chat.writerIR.unorderedHeading') },
          ]}
          onChange={(value) => onApply({
            type: 'heading',
            target_id: targetId,
            mode: value as WriterHeadingNumberingMode,
          })}
        />
      </div>
      {mode === 'ordered' && (
        <>
          <fieldset className='writer-numbering-menu__styles' disabled={disabled}>
            <legend className='writer-numbering-menu__label'>{t('chat.writerIR.numberingStyle')}</legend>
            <div className='writer-numbering-menu__style-options'>
              {ORDERED_STYLE_OPTIONS.map((option) => (
                <label className='writer-numbering-menu__style-option' key={option.value}>
                  <input
                    type='radio'
                    name={styleGroupName}
                    value={option.value}
                    checked={orderedStyle === option.value}
                    onChange={() => onApply({
                      type: 'ordered_style',
                      ordered_style: option.value,
                    })}
                  />
                  <span className='writer-numbering-menu__style-row'>
                    <span className='writer-numbering-menu__style-example' aria-hidden>{option.preview}</span>
                    <span>{t(option.labelKey)}</span>
                    <CheckOutlined className='writer-numbering-menu__style-check' aria-hidden />
                  </span>
                </label>
              ))}
            </div>
          </fieldset>
          <div className='writer-numbering-menu__sequence'>
            <Segmented
              block
              aria-label={t('chat.writerIR.numberingContinuation')}
              value={restart ? 'restart' : 'continue'}
              disabled={disabled}
              options={[
                { value: 'continue', label: t('chat.writerIR.continueNumbering') },
                { value: 'restart', label: t('chat.writerIR.restartNumbering') },
              ]}
              onChange={(value) => onApply({
                type: 'heading',
                target_id: targetId,
                restart: value === 'restart',
              })}
            />
          </div>
        </>
      )}
    </div>
  );
}
