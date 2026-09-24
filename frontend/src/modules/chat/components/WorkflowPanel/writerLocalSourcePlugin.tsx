import {
  addExportVisitor$, addImportVisitor$, addLexicalNode$, addMdastExtension$,
  addSyntaxExtension$, addToMarkdownExtension$, realmPlugin, useCodeBlockEditorContext,
  CodeBlockNode, CodeMirrorEditor, type CodeBlockEditorDescriptor, type CodeBlockEditorProps, type MdastImportVisitor,
} from '@mdxeditor/editor';
import { $applyNodeReplacement, $getRoot, $isElementNode, DecoratorNode, UNDO_COMMAND, REDO_COMMAND, type LexicalEditor, type LexicalNode, type NodeKey, type SerializedLexicalNode } from 'lexical';
import { useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import remarkMath from 'remark-math';
import { toMarkdown } from 'mdast-util-to-markdown';
import { WriterLocalSourceEditor } from './WriterLocalSourceEditor';
import { WriterCodeDisplayContext } from './writerLocalDisplayHints';

type SourceKind = 'math' | 'source';
type SerializedSource = SerializedLexicalNode & { source: string; kind: SourceKind; inline: boolean };

function LocalNodeEditor({ node, editor }: { node: WriterSourceNode; editor: LexicalEditor }) {
  const [editable, setEditable] = useState(editor.isEditable());
  useEffect(() => editor.registerEditableListener(setEditable), [editor]);
  return <WriterLocalSourceEditor source={node.__source} kind={node.__kind} inline={node.isInline()} readOnly={!editable}
    onUndoRedo={redo => editor.dispatchCommand(redo ? REDO_COMMAND : UNDO_COMMAND, undefined)}
    onChange={(source) => editor.update(() => node.setSource(source))} />;
}

export class WriterSourceNode extends DecoratorNode<ReactNode> {
  __source: string;
  __kind: SourceKind;
  __inline: boolean;
  constructor(source = '', kind: SourceKind = 'source', inline = false, key?: NodeKey) {
    super(key); this.__source = source; this.__kind = kind; this.__inline = inline;
  }
  static getType() { return 'writer-source'; }
  static clone(node: WriterSourceNode) { return new WriterSourceNode(node.__source, node.__kind, node.__inline, node.__key); }
  static importJSON(value: SerializedSource) { return $applyNodeReplacement(new WriterSourceNode(value.source, value.kind, value.inline)); }
  exportJSON(): SerializedSource { return { type: 'writer-source', version: 1, source: this.getSource(), kind: this.__kind, inline: this.__inline }; }
  getSource() { return this.getLatest().__source; }
  setSource(source: string) { this.getWritable().__source = source; }
  getTextContent() { return this.getSource(); }
  isInline() { return this.__inline; }
  createDOM() { return document.createElement(this.__inline ? 'span' : 'div'); }
  updateDOM() { return false; }
  decorate(editor: LexicalEditor) {
    const anchor = this.__kind === 'source' && this.getSource().match(/^<a\s+id=["']((?:block-|writer-page-marker-)[^"']+)["']\s*><\/a>\s*$/);
    return anchor ? <span id={anchor[1]} aria-hidden='true' /> : <LocalNodeEditor node={this} editor={editor} />;
  }
}

function LocalCodeEditor(props: CodeBlockEditorProps) {
  const languages = useContext(WriterCodeDisplayContext);
  const { setCode, parentEditor } = useCodeBlockEditorContext();
  const hints = languages.get(`${props.language ?? ''}\n${props.code}`);
  const hintedLanguage = hints && parentEditor.getEditorState().read(() => {
    const matches: string[] = [];
    const visit = (node: LexicalNode) => {
      if (node instanceof CodeBlockNode && node.getCode() === props.code && node.getLanguage() === props.language) matches.push(node.getKey());
      if ($isElementNode(node)) node.getChildren().forEach(visit);
    };
    visit($getRoot());
    return hints[matches.indexOf(props.nodeKey)];
  });
  const display = useRef({ sourceLanguage: props.language, hint: hintedLanguage });
  if (display.current.sourceLanguage !== props.language) display.current = { sourceLanguage: props.language, hint: hintedLanguage };
  else if (hintedLanguage) display.current.hint = hintedLanguage;
  const language = display.current.hint ?? props.language;
  const [editable, setEditable] = useState(parentEditor.isEditable());
  useEffect(() => parentEditor.registerEditableListener(setEditable), [parentEditor]);
  if (!['mermaid', 'math', 'latex', 'geojson', 'topojson', 'stl'].includes(language ?? '')) return <CodeMirrorEditor {...props} />;
  return <WriterLocalSourceEditor source={props.code} kind={language === 'latex' ? 'math' : language as 'math' | 'mermaid' | 'geojson' | 'topojson' | 'stl'} readOnly={!editable} onUndoRedo={redo => parentEditor.dispatchCommand(redo ? REDO_COMMAND : UNDO_COMMAND, undefined)} onChange={setCode} />;
}
export const writerLocalCodeEditor: CodeBlockEditorDescriptor = {
  priority: 10, match: () => true, Editor: LocalCodeEditor,
};

// Reuse remark-math's registered parser/serializer extensions, including inline math.
const extensions: Record<string, unknown[]> = {};
remarkMath.call({ data: () => extensions } as never);
type MdastNode = Parameters<Exclude<MdastImportVisitor<never>['testNode'], string>>[0];
const sourceVisitor: MdastImportVisitor<MdastNode> = {
  priority: 20,
  testNode: (node) => ['math', 'inlineMath', 'html', 'definition', 'footnoteDefinition', 'footnoteReference'].includes(node.type)
    || (['paragraph', 'blockquote'].includes(node.type) && /\[\[|\[!\w+\]|%%|\^\w+\s*$/.test(JSON.stringify(node))),
  visitNode({ mdastNode, mdastParent, lexicalParent }) {
    const source = 'value' in mdastNode ? String(mdastNode.value) : toMarkdown(mdastNode as never, { extensions: extensions.toMarkdownExtensions as never }).trimEnd().replace(/\\([\[\]])/g, '$1');
    if (!$isElementNode(lexicalParent)) return;
    lexicalParent.append($applyNodeReplacement(new WriterSourceNode(source,
      ['math', 'inlineMath'].includes(mdastNode.type) ? 'math' : 'source', mdastNode.type === 'inlineMath' || mdastParent?.type === 'paragraph')));
  },
};
export const writerLocalSourcePlugin = realmPlugin({
  init(realm) {
    for (const extension of extensions.micromarkExtensions ?? []) realm.pub(addSyntaxExtension$, extension as never);
    for (const extension of extensions.fromMarkdownExtensions ?? []) realm.pub(addMdastExtension$, extension as never);
    for (const extension of extensions.toMarkdownExtensions ?? []) realm.pub(addToMarkdownExtension$, extension as never);
    realm.pub(addLexicalNode$, WriterSourceNode);
    realm.pub(addImportVisitor$, sourceVisitor);
    realm.pub(addExportVisitor$, {
      testLexicalNode: (node): node is WriterSourceNode => node instanceof WriterSourceNode,
      visitLexicalNode({ lexicalNode, actions }) {
        if (!(lexicalNode instanceof WriterSourceNode)) return;
        actions.addAndStepInto(lexicalNode.__kind === 'math' ? lexicalNode.isInline() ? 'inlineMath' : 'math' : 'html', { value: lexicalNode.getSource() });
      },
    });
  },
});
