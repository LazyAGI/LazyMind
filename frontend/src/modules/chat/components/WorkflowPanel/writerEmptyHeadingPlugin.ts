import { createRootEditorSubscription$, realmPlugin } from '@mdxeditor/editor';
import {
  $getSelection,
  $isRangeSelection,
  COMMAND_PRIORITY_HIGH,
  DELETE_CHARACTER_COMMAND,
  DELETE_LINE_COMMAND,
  DELETE_WORD_COMMAND,
  REMOVE_TEXT_COMMAND,
} from 'lexical';

export const writerEmptyHeadingPlugin = realmPlugin({
  init(realm) {
    realm.pub(createRootEditorSubscription$, (editor) => {
      const preserveHeading = () => {
        if (!editor.isEditable() || editor.isComposing()) return false;
        const selection = $getSelection();
        if (!$isRangeSelection(selection)) return false;
        const heading = selection.anchor.getNode().getTopLevelElement();
        if (
          heading?.getType() !== 'heading'
          || !heading.is(selection.focus.getNode().getTopLevelElement())
        ) return false;

        if (selection.isCollapsed()) {
          if (heading.getTextContent()) return false;
          const level = Number(editor.getElementByKey(heading.getKey())?.tagName.slice(1));
          let sibling = heading.getNextSibling();
          while (sibling && sibling.getType() !== 'heading') sibling = sibling.getNextSibling();
          const siblingLevel = sibling
            ? Number(editor.getElementByKey(sibling.getKey())?.tagName.slice(1))
            : 0;
          // Keep an empty parent as the numbering anchor for following child headings.
          return siblingLevel > level;
        }
        if (selection.getTextContent() !== heading.getTextContent()) return false;

        // Clearing the heading itself keeps its level even at the document start,
        // where Lexical's default range deletion replaces it with a paragraph.
        heading.clear();
        heading.selectStart();
        return true;
      };
      const unregister = [
        editor.registerCommand(DELETE_CHARACTER_COMMAND, preserveHeading, COMMAND_PRIORITY_HIGH),
        editor.registerCommand(DELETE_WORD_COMMAND, preserveHeading, COMMAND_PRIORITY_HIGH),
        editor.registerCommand(DELETE_LINE_COMMAND, preserveHeading, COMMAND_PRIORITY_HIGH),
        editor.registerCommand(REMOVE_TEXT_COMMAND, () => preserveHeading(), COMMAND_PRIORITY_HIGH),
      ];
      return () => unregister.forEach((remove) => remove());
    });
  },
});
