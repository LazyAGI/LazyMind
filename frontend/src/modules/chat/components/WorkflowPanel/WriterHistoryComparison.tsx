import { useEffect, useRef, useState, type ComponentProps } from 'react';
import { diffArrays } from 'diff';
import { useTranslation } from 'react-i18next';
import { HistorySnapshotPreview } from './HistorySnapshotPreview';

type Side = { label: string; snapshot: unknown };
type Props = Pick<ComponentProps<typeof HistorySnapshotPreview>, 'session' | 'schedule' | 'allowDownload' | 'resolveImageUrl'> & {
  summary: string;
  before: Side;
  after: Side;
};

// Compare visible article blocks. Image signing, editor IDs and preview controls
// can change independently of the content stored in a revision.
function blockKey(element: Element): string {
  const parts: (string | null)[] = [];
  function visit(node: Node) {
    if (node.nodeType === Node.TEXT_NODE) { parts.push('text', node.textContent); return; }
    if (!(node instanceof Element) || node.matches('button, [hidden], [aria-hidden="true"], .ant-image-mask, a:empty')) return;
    const imageSource = node.getAttribute('data-image-source') ?? node.getAttribute('data-writer-image-source');
    if (imageSource !== null) {
      parts.push('image', imageSource, node.getAttribute('alt') ?? node.querySelector('img')?.alt ?? '');
      return;
    }
    const wrapper = node.matches('div, span') && !node.hasAttribute('style');
    if (!wrapper) parts.push(node.tagName, ...['href', 'src', 'alt', 'style', 'start', 'colspan', 'rowspan'].map(name => node.getAttribute(name)));
    node.childNodes.forEach(visit);
    if (!wrapper) parts.push('/');
  }
  visit(element);
  return JSON.stringify(parts);
}

function articleBlocks(side: Element) {
  const article = side.querySelector('.rag-markdown, .writer-ir__document');
  if (!article) return null;
  const blocks = article.matches('.rag-markdown') ? Array.from(article.children)
    : Array.from(article.querySelectorAll('[data-writer-document-title], [data-writer-block-content]:not(figcaption), figure, table'))
      .filter(block => !block.parentElement?.closest('table, figure, [data-writer-block-content]'));
  return blocks.map(element => ({ element, key: blockKey(element) })).filter(block => block.key && !block.element.matches('[hidden], p:empty'));
}

export function WriterHistoryComparison({ summary, ...props }: Props) {
  const [open, setOpen] = useState(false);
  return <details className='workflow-slot__history-comparison' onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>{summary}</summary>
    {open && <ComparedPages {...props} />}
  </details>;
}

function ComparedPages({ before, after, ...previewProps }: Omit<Props, 'summary'>) {
  const { t } = useTranslation();
  const root = useRef<HTMLDivElement>(null);
  const [identical, setIdentical] = useState(false);
  useEffect(() => {
    const container = root.current!;
    function compare() {
      const [oldSide, newSide] = Array.from(container.children);
      const oldBlocks = articleBlocks(oldSide);
      const newBlocks = articleBlocks(newSide);
      if (!oldBlocks || !newBlocks) { setIdentical(false); return; }
      const changes = diffArrays(oldBlocks.map(b => b.key), newBlocks.map(b => b.key));
      let oldIndex = 0;
      let newIndex = 0;
      for (const change of changes) {
        for (let i = 0; i < change.count; i++) {
          if (!change.added) oldBlocks[oldIndex++].element.toggleAttribute('data-history-removed', !!change.removed);
          if (!change.removed) newBlocks[newIndex++].element.toggleAttribute('data-history-added', !!change.added);
        }
      }
      setIdentical(changes.every(change => !change.added && !change.removed));
    }
    compare();
    const observer = new MutationObserver(compare);
    observer.observe(container, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['src', 'alt', 'href'] });
    return () => observer.disconnect();
  }, [before.snapshot, after.snapshot]);
  return <div className='writer-history-diff'>
    {identical && <span role='status'>{t('chat.slots.identicalContent')}</span>}
    <div className='writer-history-diff__pages' ref={root}>
      {[before, after].map((side, i) => <section key={i} aria-label={side.label}>
        <header>{t(i === 0 ? 'chat.slots.beforeChange' : 'chat.slots.afterChange')} · {side.label}</header>
        <HistorySnapshotPreview {...previewProps} kind='writer' detail snapshot={side.snapshot} label={side.label} />
      </section>)}
    </div>
  </div>;
}
