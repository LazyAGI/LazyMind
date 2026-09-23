import { createRef } from 'react';
import { act, render } from '@testing-library/react';
import { MDXEditor, listsPlugin, type MDXEditorMethods } from '@mdxeditor/editor';
import MarkdownIt from 'markdown-it';
import { expect, it } from 'vitest';
import { writerListNumberingPlugin } from './writerListNumberingPlugin';

const parser = new MarkdownIt();
const starts = (markdown: string) => parser.parse(markdown, {})
  .filter(token => token.type === 'ordered_list_open')
  .map(token => Number(token.attrGet('start') ?? 1));

it.each([[0, false], [1, false], [3, false], [12, true]] as const)(
  'renders and exports an ordered list starting at %i (readOnly: %s)', (start, readOnly) => {
    const editor = createRef<MDXEditorMethods>();
    const { container } = render(<MDXEditor ref={editor} markdown={`${start}. First\n${start + 1}. Second`}
      readOnly={readOnly} plugins={[listsPlugin(), writerListNumberingPlugin()]} />);
    const list = container.querySelector('ol')!;
    expect(list.start).toBe(start);
    expect(Array.from(list.children).map(item => (item as HTMLLIElement).value)).toEqual([start, start + 1]);
    expect(starts(editor.current!.getMarkdown())).toEqual([start]);
  },
);

it('retains independent starts for nested lists and separate lists through export and reimport', async () => {
  const editor = createRef<MDXEditorMethods>();
  const markdown = '3. Parent\n\n   7. Child\n   8. Child next\n4. Next\n\nSeparator\n\n9. Separate\n10. Following\n\n- Bullet\n- [x] Done';
  const { container } = render(<MDXEditor ref={editor} markdown={markdown}
    plugins={[listsPlugin(), writerListNumberingPlugin()]} />);
  expect(Array.from(container.querySelectorAll('ol')).map(list => list.start)).toEqual([3, 7, 9]);
  const exported = editor.current!.getMarkdown();
  expect(starts(exported)).toEqual([3, 7, 9]);
  expect(container.querySelector('li[aria-checked="true"]')).toHaveTextContent('Done');
  await act(async () => editor.current!.setMarkdown(exported.replace('Child next', 'Edited child')));
  expect(container).toHaveTextContent('Edited child');
  expect(Array.from(container.querySelectorAll('ol')).map(list => list.start)).toEqual([3, 7, 9]);
  expect(starts(editor.current!.getMarkdown())).toEqual([3, 7, 9]);
  expect(container.querySelector('li[aria-checked="true"]')).toHaveTextContent('Done');
});
