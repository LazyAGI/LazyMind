import { addExportVisitor$, addImportVisitor$, realmPlugin } from '@mdxeditor/editor';
import { $createListItemNode, $createListNode, $isListItemNode, $isListNode, type ListNode } from '@lexical/list';

// MDXEditor's default list visitors omit the ordered list's start on import and export.
export const writerListNumberingPlugin = realmPlugin({
  init(realm) {
    realm.pub(addImportVisitor$, {
      priority: 10,
      testNode: node => node.type === 'list' && Boolean(node.ordered)
        && !node.children.some(item => typeof item.checked === 'boolean'),
      visitNode({ mdastNode, lexicalParent, actions }) {
        if (mdastNode.type !== 'list') return;
        const list = $createListNode('number', mdastNode.start ?? 1);
        if ($isListItemNode(lexicalParent)) {
          // Keep the nested-list wrapper expected by MDXEditor's list-item visitors.
          const wrapper = $createListItemNode();
          wrapper.append(list);
          lexicalParent.insertAfter(wrapper);
          actions.visitChildren(mdastNode, list);
        } else {
          actions.addAndStepInto(list);
        }
      },
    });
    realm.pub(addExportVisitor$, {
      priority: 10,
      testLexicalNode: (node): node is ListNode => $isListNode(node) && node.getListType() === 'number',
      visitLexicalNode({ lexicalNode, mdastParent, actions }) {
        if (!$isListNode(lexicalNode)) return;
        const start = lexicalNode.getStart();
        // A nested list starting outside 1 needs a blank line after its parent's prose.
        if (mdastParent.type === 'listItem' && start !== 1) Object.assign(mdastParent, { spread: true });
        actions.addAndStepInto('list', { ordered: true, start, spread: false });
      },
    });
  },
});
