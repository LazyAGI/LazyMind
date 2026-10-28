import {
  addComposerChild$, addExportVisitor$, addLexicalNode$, createRootEditorSubscription$,
  realmPlugin, rootEditor$, useCellValues, ImageNode,
} from '@mdxeditor/editor';
import {
  $applyNodeReplacement, $createParagraphNode, $getRoot, $isElementNode, $isParagraphNode, $getNodeByKey, $getNearestNodeFromDOMNode, $isLineBreakNode, $isTextNode,
  ElementNode, RootNode, setDOMUnmanaged, type LexicalNode, type NodeKey, type SerializedElementNode,
} from 'lexical';
import { createContext, useContext, useEffect, useLayoutEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { createPortal } from 'react-dom';
import { WriterImageGroupControls } from './WriterImageGroupControls';
import { imageGroupKey, imageGroupLayout, normalizeImageSource, setImageGroup, type WriterImageGroup, type WriterImageRef } from './writerImageGroups';
import './writerImageGroups.scss';

interface ImageGroupSettings {
  groups: WriterImageGroup[];
  onChange: (groups: WriterImageGroup[]) => void;
}

export const WriterImageGroupContext = createContext<ImageGroupSettings | null>(null);

class WriterImageGroupNode extends ElementNode {
  static getType() { return 'writer-image-group'; }
  static clone(node: WriterImageGroupNode) { return new WriterImageGroupNode(node.__key); }
  constructor(key?: NodeKey) { super(key); }
  static importJSON(value: SerializedElementNode) { return $applyNodeReplacement(new WriterImageGroupNode()).updateFromJSON(value); }
  createDOM() {
    const element = document.createElement('section'); element.className = 'writer-image-group';
    const host = document.createElement('div'); host.className = 'writer-image-group-host'; host.contentEditable = 'false';
    setDOMUnmanaged(host);
    const children = document.createElement('div'); children.className = 'writer-image-group-images';
    element.append(host, children); return element;
  }
  getDOMSlot(element: HTMLElement) { return super.getDOMSlot(element).withElement(element.querySelector<HTMLElement>('.writer-image-group-images')!); }
  updateDOM() { return false; }
  canBeEmpty() { return false; }
}

function imageParagraph(node: LexicalNode) {
  return $isParagraphNode(node) && node.getChildren().some(child => child instanceof ImageNode)
    && node.getChildren().every(child => (child instanceof ImageNode && Boolean(child.getSrc())) || $isLineBreakNode(child)
      || ($isTextNode(child) && !child.getTextContent().trim()));
}

function imageCount(node: LexicalNode): number {
  if (node instanceof ImageNode) return 1;
  return $isElementNode(node) ? node.getChildren().reduce((count, child) => count + imageCount(child), 0) : 0;
}

function normalizeImageParagraphs(node: WriterImageGroupNode) {
  for (const child of node.getChildren()) {
    if (!$isParagraphNode(child)) throw new Error('Invalid image group container.');
    if (child.getChildrenSize() <= 1) continue;
    for (const image of child.getChildren().filter(item => item instanceof ImageNode)) {
      const paragraph = $createParagraphNode().setFormat(child.getFormatType());
      child.insertBefore(paragraph); paragraph.append(image);
    }
    child.remove();
  }
}

function groupState() {
  const counts = new Map<string, number>();
  const result: Array<{ key: string; images: WriterImageRef[] }> = [];
  const visit = (node: LexicalNode): WriterImageRef[] => {
    if (node instanceof ImageNode) {
      const source = normalizeImageSource(node.getSrc()), occurrence = counts.get(source) ?? 0;
      counts.set(source, occurrence + 1); return [{ source, occurrence }];
    }
    const images = $isElementNode(node) ? node.getChildren().flatMap(visit) : [];
    if (node instanceof WriterImageGroupNode) result.push({ key: node.getKey(), images });
    return images;
  };
  visit($getRoot()); return result;
}

function GroupControls() {
  const settings = useContext(WriterImageGroupContext);
  if (!settings) throw new Error('Image group settings are required.');
  const { groups: configuredGroups, onChange } = settings;
  const [editor] = useCellValues(rootEditor$);
  const { t } = useTranslation();
  const [groups, setGroups] = useState<ReturnType<typeof groupState>>([]);
  const [editable, setEditable] = useState(false);
  // Publish layout metadata in the same commit as the portal controls.
  useLayoutEffect(() => {
    for (const group of groups) {
      const element = editor?.getElementByKey(group.key);
      element?.setAttribute('data-layout', imageGroupLayout(configuredGroups, group.images) ?? 'vertical');
      element?.setAttribute('data-image-group-key', imageGroupKey(group.images));
      element?.setAttribute('data-image-count', String(group.images.length));
      element?.setAttribute('data-image-group-hint', t('chat.writerImageGroups.hint', { count: group.images.length }));
    }
  }, [editor, groups, configuredGroups, t]);
  useEffect(() => {
    if (!editor) return;
    const refresh = () => setGroups(editor.getEditorState().read(groupState));
    refresh(); setEditable(editor.isEditable());
    const unsubscribe = editor.registerUpdateListener(refresh);
    const unsubscribeEditable = editor.registerEditableListener(setEditable);
    let draggedImage: HTMLImageElement | null = null;
    const startDrag = (event: MouseEvent) => {
      if (event.button !== 0 || !editor.isEditable() || !(event.target instanceof Element)) return;
      draggedImage = event.target.closest<HTMLImageElement>('.writer-image-group-images img');
      if (draggedImage) event.preventDefault();
    };
    const finishDrag = (event: MouseEvent) => {
      const source = draggedImage; draggedImage = null;
      const target = event.target instanceof Element ? event.target.closest<HTMLImageElement>('.writer-image-group-images img') : null;
      if (!editor.isEditable() || !source || !target || source === target
        || source.closest('.writer-image-group') !== target.closest('.writer-image-group')) return;
      const rect = target.getBoundingClientRect();
      const before = target.closest('.writer-image-group')?.getAttribute('data-layout') === 'sliding'
        ? event.clientX < rect.left + rect.width / 2 : event.clientY < rect.top + rect.height / 2;
      editor.update(() => {
        const image = $getNearestNodeFromDOMNode(source), destination = $getNearestNodeFromDOMNode(target);
        if (!(image instanceof ImageNode) || !(destination instanceof ImageNode)) throw new Error('The image group has changed.');
        const group = image.getParentOrThrow().getParentOrThrow();
        if (!(group instanceof WriterImageGroupNode)) throw new Error('The image group has changed.');
        normalizeImageParagraphs(group);
        const paragraph = image.getParentOrThrow(), targetParagraph = destination.getParentOrThrow();
        if (before) targetParagraph.insertBefore(paragraph); else targetParagraph.insertAfter(paragraph);
      });
    };
    const unsubscribeRoot = editor.registerRootListener((root, previous) => {
      previous?.removeEventListener('mousedown', startDrag);
      root?.addEventListener('mousedown', startDrag);
    });
    window.addEventListener('mouseup', finishDrag);
    editor.update(() => $getRoot().markDirty());
    return () => { unsubscribe(); unsubscribeEditable(); unsubscribeRoot(); window.removeEventListener('mouseup', finishDrag); };
  }, [editor]);
  if (!editor) return null;
  return <>{groups.map(group => {
    const host = editor.getElementByKey(group.key)?.querySelector<HTMLElement>('.writer-image-group-host');
    return host && createPortal(<WriterImageGroupControls layout={imageGroupLayout(configuredGroups, group.images)} readOnly={!editable}
      onLayout={layout => editor.update(() => {
        const node = $getNodeByKey(group.key);
        if (!(node instanceof WriterImageGroupNode)) throw new Error('The image group has changed.');
        if (layout === 'sliding') normalizeImageParagraphs(node);
        onChange(setImageGroup(configuredGroups, group.images, layout));
        $getRoot().markDirty();
      })} />, host, group.key);
  })}</>;
}

export const writerImageGroupPlugin = realmPlugin({
  init(realm) {
    realm.pub(addLexicalNode$, WriterImageGroupNode);
    realm.pub(addComposerChild$, GroupControls);
    realm.pub(addExportVisitor$, {
      testLexicalNode: (node): node is WriterImageGroupNode => node instanceof WriterImageGroupNode,
      visitLexicalNode({ lexicalNode, mdastParent, actions }) { actions.visitChildren(lexicalNode, mdastParent); },
    });
    realm.pub(createRootEditorSubscription$, editor => editor.registerNodeTransform(RootNode, root => {
      let run: LexicalNode[] = [];
      const finish = () => {
        const count = run.reduce((total, item) => total + imageCount(item), 0);
        if (count >= 2 && !(run.length === 1 && run[0] instanceof WriterImageGroupNode)) {
          const group = $applyNodeReplacement(new WriterImageGroupNode()); run[0].insertBefore(group);
          for (const item of run) {
            if (item instanceof WriterImageGroupNode) { group.append(...item.getChildren()); item.remove(); }
            else group.append(item);
          }
        }
        run = [];
      };
      for (const child of root.getChildren()) {
        if (child instanceof WriterImageGroupNode && (!child.getChildren().every(imageParagraph)
          || imageCount(child) < 2)) {
          child.getChildren().forEach(item => child.insertBefore(item)); child.remove(); finish();
        } else if (imageParagraph(child) || child instanceof WriterImageGroupNode) run.push(child);
        else finish();
      }
      finish();
    }));
  },
});
