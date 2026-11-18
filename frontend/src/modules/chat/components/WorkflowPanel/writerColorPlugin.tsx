import { addComposerChild$, addExportVisitor$, addLexicalNode$, createRootEditorSubscription$, realmPlugin, rootEditor$, useCellValues } from '@mdxeditor/editor';
import { $applyNodeReplacement, $getNodeByKey, $createTextNode, TextNode, HISTORY_PUSH_TAG, type EditorConfig, type LexicalEditor, type SerializedTextNode } from 'lexical';
import { Modal } from 'antd';
import { BgColorsOutlined } from '@ant-design/icons';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { markdownColorMatches, parseMarkdownColor, replaceMarkdownColor } from './writerMarkdownColors';
import './writerColors.scss';

class WriterColorNode extends TextNode {
  static getType() { return 'writer-color'; }
  static clone(node: WriterColorNode) { return new WriterColorNode(node.__text, node.__key); }
  static importJSON(value: SerializedTextNode) { return $applyNodeReplacement(new WriterColorNode()).updateFromJSON(value).setMode('token'); }
  // HTML imports use the standard TextNode converter; the transform adds swatches.
  static importDOM() { return null; }
  decorate(element: HTMLElement) {
    element.classList.add('writer-color');
    element.setAttribute('contenteditable', 'false');
    element.dataset.writerColor = this.getKey();
    element.style.setProperty('--writer-color', this.getTextContent());
  }
  createDOM(config: EditorConfig, editor: LexicalEditor) {
    const element = super.createDOM(config, editor); this.decorate(element); return element;
  }
  updateDOM(previous: this, element: HTMLElement, config: EditorConfig) {
    const replace = super.updateDOM(previous, element, config); this.decorate(element); return replace;
  }
  exportDOM(editor: LexicalEditor) {
    const output = super.exportDOM(editor);
    if (output.element instanceof HTMLElement) {
      for (const element of [output.element, ...output.element.querySelectorAll<HTMLElement>('[data-writer-color]')]) {
        element.classList.remove('writer-color');
        element.removeAttribute('data-writer-color');
        element.removeAttribute('contenteditable');
        element.style.removeProperty('--writer-color');
      }
    }
    return output;
  }
}

function replaceTextNode(source: TextNode, replacement: TextNode) {
  source.replace(replacement.setFormat(source.getFormat()).setStyle(source.getStyle()).setDetail(source.getDetail())
    .setMode(replacement instanceof WriterColorNode ? 'token' : 'normal'));
}

function decorateColor(node: TextNode) {
  if (node.isComposing()) return;
  const text = node.getTextContent();
  if (node instanceof WriterColorNode) {
    if (!parseMarkdownColor(text)) replaceTextNode(node, $createTextNode(text));
    return;
  }
  if (!node.isSimpleText() || node.getParents().some(parent => parent.getType() === 'link')) return;
  const match = markdownColorMatches(text)[0];
  if (!match) return;
  const index = match.index!;
  const parts = node.splitText(index, index + match[0].length);
  replaceTextNode(parts[index === 0 ? 0 : 1], $applyNodeReplacement(new WriterColorNode(match[0])));
}

function ColorControls() {
  const [editor] = useCellValues(rootEditor$);
  const { t } = useTranslation();
  const [target, setTarget] = useState<{ key: string; original: string }>();
  const [value, setValue] = useState('');
  const trigger = useRef<HTMLElement>();
  const input = useRef<HTMLInputElement>(null);
  const parsed = parseMarkdownColor(value);
  useEffect(() => {
    if (!editor) return;
    const refresh = () => {
      const editable = editor.isEditable();
      for (const element of editor.getRootElement()?.querySelectorAll<HTMLElement>('[data-writer-color]') ?? []) {
        element.setAttribute('role', editable ? 'button' : 'img');
        element.setAttribute('aria-label', t(editable ? 'chat.writerColors.edit' : 'chat.writerColors.preview', { color: element.textContent }));
        element.setAttribute('title', element.getAttribute('aria-label')!);
        element.tabIndex = editable ? 0 : -1;
      }
      if (!editable) setTarget(undefined);
    };
    const open = (event: Event) => {
      if (!editor.isEditable() || (event instanceof KeyboardEvent && !['Enter', ' '].includes(event.key))) return;
      const element = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-writer-color]') : null;
      if (!element || !editor.getRootElement()?.contains(element)) return;
      // The complete labeled color is the control, including wrapped text.
      // A bounding-box offset cannot reliably identify its inline swatch.
      event.preventDefault(); event.stopPropagation();
      editor.getEditorState().read(() => {
        const node = $getNodeByKey(element.dataset.writerColor!);
        if (!(node instanceof WriterColorNode)) return;
        trigger.current = element;
        setTarget({ key: node.getKey(), original: node.getTextContent() }); setValue(node.getTextContent());
      });
    };
    const blockInlineEdit = (event: Event) => {
      const element = event.target instanceof Element ? event.target.closest('[data-writer-color]') : null;
      if (!element || !editor.getRootElement()?.contains(element)) return;
      if (event instanceof KeyboardEvent && event.key !== 'Backspace' && event.key !== 'Delete'
        && (event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey || event.key === ' ')) return;
      if (event instanceof MouseEvent && event.button !== 0) return;
      // A focused control must not edit the editor's previous text selection.
      // Prevent mouse placement inside the token as well as input/paste/cut.
      event.preventDefault(); event.stopPropagation();
    };
    const blockedEvents = ['mousedown', 'keydown', 'beforeinput', 'paste', 'cut'] as const;
    const unregisterRoot = editor.registerRootListener((root, previous) => {
      previous?.removeEventListener('click', open); previous?.removeEventListener('keydown', open, true);
      for (const type of blockedEvents) previous?.removeEventListener(type, blockInlineEdit, true);
      for (const type of blockedEvents) root?.addEventListener(type, blockInlineEdit, true);
      root?.addEventListener('click', open); root?.addEventListener('keydown', open, true); refresh();
    });
    const unregisterUpdate = editor.registerUpdateListener(refresh);
    const unregisterEditable = editor.registerEditableListener(refresh);
    return () => { unregisterRoot(); unregisterUpdate(); unregisterEditable(); };
  }, [editor, t]);
  const close = () => { setTarget(undefined); trigger.current?.focus({ preventScroll: true }); };
  const apply = () => {
    if (!editor?.isEditable() || !target || !parsed) return;
    editor.update(() => {
      const node = $getNodeByKey(target.key);
      // A remote update or a text edit while the picker was open invalidates
      // this occurrence. Never replace another matching color by value.
      if (node instanceof WriterColorNode && node.getTextContent() === target.original) node.setTextContent(value);
    }, { tag: HISTORY_PUSH_TAG });
    close();
  };
  if (!target) return null;
  return <Modal open centered className='writer-color-dialog' title={t('chat.writerColors.title')} width={400} onCancel={close} onOk={apply}
    styles={{ mask: { backgroundColor: 'rgba(16, 24, 40, 0.28)' } }}
    okText={t('common.confirm')} cancelText={t('common.cancel')} okButtonProps={{ disabled: !parsed, autoInsertSpace: false }}
    cancelButtonProps={{ autoInsertSpace: false }}
    afterOpenChange={(open: boolean) => { if (open) input.current?.focus(); }}>
    <div className='writer-color-picker'>
      <p className='writer-color-picker__description'>{t('chat.writerColors.description')}</p>
      <div className='writer-color-picker__comparison'>
        <div className='writer-color-picker__sample'>
          <span className='writer-color-picker__label'>{t('chat.writerColors.original')}</span>
          <div className='writer-color-picker__swatch' aria-hidden>
            <span className='writer-color-picker__fill' style={{ backgroundColor: target.original }} />
          </div>
        </div>
        <div className='writer-color-picker__sample'>
          <span className='writer-color-picker__label'>{t('chat.writerColors.updated')}</span>
          <label className='writer-color-picker__swatch writer-color-picker__swatch--interactive'>
            <span className='writer-color-picker__fill' aria-hidden style={{ backgroundColor: parsed ? value : undefined }} />
            <span className='writer-color-picker__choose' aria-hidden><BgColorsOutlined />{t('chat.writerColors.choose')}</span>
            <input ref={input} type='color' aria-label={t('chat.writerColors.choose')}
              value={parsed?.hex ?? '#000000'} onChange={event => setValue(replaceMarkdownColor(parsed ? value : target.original, event.target.value))} />
          </label>
        </div>
      </div>
      <label className='writer-color-picker__value'>
        <span className='writer-color-picker__value-label'>
          <span>{t('chat.writerColors.value')}</span>
          {parsed && <span className='writer-color-picker__opacity'>{t('chat.writerColors.opacity', { value: Math.round(parsed.alpha * 1000) / 10 })}</span>}
        </span>
        <input type='text' aria-label={t('chat.writerColors.value')} value={value} spellCheck={false}
          aria-invalid={!parsed} onChange={event => setValue(event.target.value)} />
      </label>
      {!parsed && <span role='alert'>{t('chat.writerColors.invalid')}</span>}
    </div>
  </Modal>;
}

export const writerColorPlugin = realmPlugin({ init(realm) {
  realm.pub(addLexicalNode$, WriterColorNode);
  realm.pub(addComposerChild$, ColorControls);
  realm.pub(createRootEditorSubscription$, editor => {
    const plain = editor.registerNodeTransform(TextNode, decorateColor);
    const colors = editor.registerNodeTransform(WriterColorNode, decorateColor);
    return () => { plain(); colors(); };
  });
  // Splitting an inline code text node into color occurrences must not split
  // its Markdown backticks. Keep the editor's normal text export visitor.
  realm.pub(addExportVisitor$, {
    testLexicalNode: (_node): _node is TextNode => false,
    visitLexicalNode() {},
    shouldJoin: (previous, current) => previous.type === 'inlineCode' && current.type === 'inlineCode',
    join: (previous, current) => ({ ...previous, value: (previous as { value: string }).value + (current as { value: string }).value }),
  });
} });
