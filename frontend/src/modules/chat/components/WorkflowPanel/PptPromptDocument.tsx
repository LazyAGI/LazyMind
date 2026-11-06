import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import './PptPromptDocument.scss';

interface PromptSection {
  title: string;
  body: string;
}

// Split only recognized labels/headings. Keep unrecognized text (including HTML
// examples) literal and in its original order; never rewrite the saved prompt.
export function splitPptPrompt(text: string): PromptSection[] {
  const sections: PromptSection[] = [];
  let current: PromptSection = { title: '', body: '' };
  let fence = '';
  for (const line of text.split('\n')) {
    if (!fence && line.trim() === '<!-- ppt-page-prompt:v1 -->') continue;
    const fenceMatch = line.trim().match(/^(`{3,}|~{3,})/);
    if (fenceMatch) {
      fence = fence ? '' : fenceMatch[1][0];
    }
    const heading = !fence && !fenceMatch && (
      line.match(/^\s*#{1,6}\s+(.+?)\s*$/)
      || line.match(/^\s*\*\*(.{1,48}?)\*\*\s*[：:]?\s*(.*)$/)
      || line.match(/^\s*((?:页面|设计风格|风格|布局|页面布局|主标题|标题|内容模块|设计要求|画布与输出|字体|配色|版式|构图|技术说明|问题|方法|我的实现|结果|左[侧栏]|右[侧栏])[^：:\n]{0,24})[：:]\s*(.*)$/)
    );
    if (heading) {
      if (current.title || current.body.trim()) sections.push(current);
      current = { title: heading[1].replace(/[：:]$/, ''), body: heading[2] ?? '' };
    } else {
      current.body += `${current.body ? '\n' : ''}${line}`;
    }
  }
  if (current.title || current.body.trim()) sections.push(current);
  return sections;
}

export function PptPromptDocument({ text, onEdit, maxHeight }: {
  text: string;
  onEdit?: () => void;
  maxHeight?: number;
}) {
  const { t } = useTranslation();
  const sections = useMemo(() => {
    const parsed = splitPptPrompt(text);
    const isLayout = (section: PromptSection) => /^(布局|页面布局|版式|构图)(?:$|[\s（(：:])/.test(section.title);
    if (text.trimStart().startsWith('<!-- ppt-page-prompt:v1 -->')) return parsed;
    return [...parsed.filter(isLayout), ...parsed.filter(section => !isLayout(section))];
  }, [text]);
  return <div className='ppt-prompt-document'>
    <div className='ppt-prompt-document__toolbar'>
      <strong>{t('chat.slots.pptDesignDocument')}</strong>
      <div className='ppt-prompt-document__actions'>
        {onEdit && <button type='button' onClick={onEdit}>{t('chat.slots.editText')}</button>}
      </div>
    </div>
    <div className='ppt-prompt-document__body' style={maxHeight ? { maxHeight, overflowY: 'auto' } : undefined}>
      {sections.map((section, index) => (
        section.title ? <details className='ppt-prompt-document__section' key={index} open>
          <summary>{section.title}</summary>
          {section.body && <div className='ppt-prompt-document__text'>{section.body}</div>}
        </details> : <div className='ppt-prompt-document__text ppt-prompt-document__intro' key={index}>{section.body}</div>
      ))}
    </div>
  </div>;
}
