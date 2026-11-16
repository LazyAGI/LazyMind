import {
  addComposerChild$, addExportVisitor$, addLexicalNode$, createRootEditorSubscription$,
  realmPlugin, rootEditor$, useCellValues, ImageNode,
} from '@mdxeditor/editor';
import {
  $applyNodeReplacement, $copyNode, $createParagraphNode, $getRoot, $getState, $setState, $isElementNode, $isParagraphNode, $getNodeByKey, $getNearestNodeFromDOMNode, $isLineBreakNode, $isTextNode,
  createState, ElementNode, HISTORY_MERGE_TAG, HISTORY_PUSH_TAG, RootNode, setDOMUnmanaged, type LexicalNode, type NodeKey, type SerializedElementNode,
} from 'lexical';
import { createContext, useContext, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { createPortal } from 'react-dom';
import { WriterImageGroupControls } from './WriterImageGroupControls';
import { startWriterImageDrag } from './writerImageDrag';
import { imageGroups, imageGroupKey, imageGroupLayout, normalizeImageSource, setImageGroup, type WriterImageGroup, type WriterImageRef } from './writerImageGroups';
import './writerImageGroups.scss';

const imageGroupsState = createState('writer-image-groups', { parse: imageGroups });

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

function nearestDropElement(elements: HTMLElement[], point: number, horizontal: boolean) {
  let nearest: HTMLElement | null = null, distance = Infinity;
  for (const element of elements) {
    const rect = element.getBoundingClientRect();
    if (!rect.width || !rect.height) continue;
    const start = horizontal ? rect.left : rect.top, end = horizontal ? rect.right : rect.bottom;
    const next = Math.max(start - point, point - end, 0);
    if (next < distance) { nearest = element; distance = next; }
  }
  return nearest;
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
  const { groups: configuredGroups } = settings;
  const settingsRef = useRef(settings);
  settingsRef.current = settings;
  const [editor] = useCellValues(rootEditor$);
  const { t } = useTranslation();
  const [groups, setGroups] = useState<ReturnType<typeof groupState>>([]);
  const [editable, setEditable] = useState(false);
  useLayoutEffect(() => {
    editor?.update(() => {
      if (JSON.stringify($getState($getRoot(), imageGroupsState)) !== JSON.stringify(configuredGroups)) {
        $setState($getRoot(), imageGroupsState, configuredGroups);
      }
    }, { tag: HISTORY_MERGE_TAG });
  }, [editor, configuredGroups]);
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
    const unsubscribe = editor.registerUpdateListener(({ editorState }) => {
      refresh();
      const current = editorState.read(() => $getState($getRoot(), imageGroupsState));
      if (JSON.stringify(current) !== JSON.stringify(settingsRef.current.groups)) settingsRef.current.onChange(current);
    });
    let cancelDrag: (() => void) | undefined;
    const unsubscribeEditable = editor.registerEditableListener(value => { setEditable(value); if (!value) cancelDrag?.(); });
    const startDrag = (event: MouseEvent) => {
      if (event.button !== 0 || !editor.isEditable() || !(event.target instanceof Element)) return;
      const source = event.target.closest<HTMLImageElement>('[data-editor-block-type="image"] img');
      const root = editor.getRootElement();
      if (!source || !root || !editor.read(() => $getNearestNodeFromDOMNode(source) instanceof ImageNode)) return;
      const canJoinGroup = editor.read(() => imageParagraph($getNearestNodeFromDOMNode(source)!.getParentOrThrow()));
      cancelDrag?.();
      cancelDrag = startWriterImageDrag(event, source, root, (x, y) => {
        const hovered = document.elementFromPoint(x, y);
        if (!(hovered instanceof HTMLElement) || !root.contains(hovered) || hovered.closest('.writer-image-group-host')) return null;
        let target: HTMLElement | null = hovered;
        const targetGroup = hovered.closest('.writer-image-group');
        const horizontal = targetGroup?.getAttribute('data-layout') === 'sliding';
        if (targetGroup && canJoinGroup) {
          target = hovered.closest('[data-editor-block-type="image"]')?.querySelector('img') ?? null;
          if (!target) target = nearestDropElement(
            Array.from(targetGroup.querySelectorAll('img')).filter(image => image !== source), horizontal ? x : y, horizontal);
          if (!target || target.closest('.writer-image-group') !== targetGroup) return null;
        } else if (hovered === root) {
          target = nearestDropElement(Array.from(root.children).filter((element): element is HTMLElement =>
            element instanceof HTMLElement && !element.contains(source)), y, false);
        } else {
          while (target && target.parentElement !== root) target = target.parentElement;
        }
        if (!target || target === source || target.contains(source)) return null;
        const rect = target.getBoundingClientRect();
        const before = horizontal ? x < rect.left + rect.width / 2 : y < rect.top + rect.height / 2;
        return { element: target, position: before ? 'before' : 'after' };
      }, target => {
        if (!editor.isEditable()) return;
        editor.update(() => {
          const previousGroups = groupState();
          const image = $getNearestNodeFromDOMNode(source), destination = $getNearestNodeFromDOMNode(target.element);
          if (!(image instanceof ImageNode) || !destination) throw new Error('The image has changed.');
          for (const node of [image, destination]) {
            const group = node instanceof ImageNode ? node.getParent()?.getParent() : null;
            if (group instanceof WriterImageGroupNode) normalizeImageParagraphs(group);
          }
          const parent = image.getParentOrThrow();
          const linked = parent.getType() === 'link';
          const content = linked ? $copyNode(parent).append(image) : image;
          if (linked && parent.isEmpty()) {
            const container = parent.getParent();
            parent.remove();
            if ($isParagraphNode(container) && container.isEmpty()) container.remove();
          }
          const paragraph = imageParagraph(parent) ? parent : $createParagraphNode().append(content);
          const targetNode = destination instanceof ImageNode ? destination.getParentOrThrow() : destination;
          if (target.position === 'before') targetNode.insertBefore(paragraph); else targetNode.insertAfter(paragraph);
          const currentGroups = groupState();
          $setState($getRoot(), imageGroupsState, $getState($getRoot(), imageGroupsState).flatMap(config => {
            const previous = previousGroups.find(group => imageGroupKey(group.images) === imageGroupKey(config.images));
            const current = currentGroups.find(group => group.key === previous?.key);
            return current && current.images.length >= 2 ? [{ ...config, images: current.images }] : [];
          }));
        }, { tag: HISTORY_PUSH_TAG });
      });
    };
    const unsubscribeRoot = editor.registerRootListener((root, previous) => {
      cancelDrag?.();
      previous?.removeEventListener('mousedown', startDrag);
      root?.addEventListener('mousedown', startDrag);
    });
    editor.update(() => $getRoot().markDirty());
    return () => { cancelDrag?.(); unsubscribe(); unsubscribeEditable(); unsubscribeRoot(); };
  }, [editor]);
  if (!editor) return null;
  return <>{groups.map(group => {
    const host = editor.getElementByKey(group.key)?.querySelector<HTMLElement>('.writer-image-group-host');
    return host && createPortal(<WriterImageGroupControls layout={imageGroupLayout(configuredGroups, group.images)} readOnly={!editable}
      onLayout={layout => editor.update(() => {
        const node = $getNodeByKey(group.key);
        if (!(node instanceof WriterImageGroupNode)) throw new Error('The image group has changed.');
        if (layout === 'sliding') normalizeImageParagraphs(node);
        $setState($getRoot(), imageGroupsState, setImageGroup($getState($getRoot(), imageGroupsState), group.images, layout));
      }, { tag: HISTORY_PUSH_TAG })} />, host, group.key);
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
