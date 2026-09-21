import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import type { MDXEditorMethods, MDXEditorProps } from '@mdxeditor/editor';
const capture=vi.hoisted(()=>({change:undefined as MDXEditorProps['onChange'],editor:null as MDXEditorMethods|null}));
vi.mock('@mdxeditor/editor',async()=>{
 const actual=await vi.importActual<typeof import('@mdxeditor/editor')>('@mdxeditor/editor');
 const React=await import('react');
 return {...actual,MDXEditor:React.forwardRef<MDXEditorMethods,MDXEditorProps>((props,ref)=>{
  capture.change=props.onChange;
  return <actual.MDXEditor {...props} ref={(editor)=>{capture.editor=editor;if(typeof ref==='function')ref(editor);else if(ref)ref.current=editor;}} />;
 })};
});
it.each([false,true])('keeps source read-only and allows rich editing after returning (reopening more menu: %s)',async(viaPreview)=>{
 const original='Old [https://example.org](https://example.org) A & B.\n';
 const onSave=vi.fn(async(markdown:string)=>({markdown,revision:2}));
 const {container,getByRole}=render(<MarkdownArtifactEditor markdown={original} sourceRevision={1} onSave={onSave} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Old'));
 container.querySelector('details.writer-document-options')?.setAttribute('open', '');
 fireEvent.click(getByRole('button',{name:'chat.writerSource.source'}));
 const source=original;
 expect(getByRole('textbox',{name:'chat.writerSource.source'})).toHaveAttribute('readonly');
 fireEvent.change(getByRole('textbox',{name:'chat.writerSource.source'}),{target:{value:source+'Attempted source edit'}});
 if(viaPreview){ container.querySelector('details.writer-document-options')?.removeAttribute('open'); container.querySelector('details.writer-document-options')?.setAttribute('open', ''); }
 fireEvent.click(getByRole('button',{name:'chat.writerLocal.backToDocument'}));
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Old'));
 await act(async()=>capture.editor!.setMarkdown(source.replace('Old','Rich')));
 await act(async()=>capture.change?.(capture.editor!.getMarkdown(),false));
 await waitFor(()=>expect(onSave).toHaveBeenCalled(),{timeout:2500});
 expect(onSave.mock.calls[0][0]).toBe(source.replace('Old','Rich'));
});
vi.mock('react-i18next',()=>({initReactI18next:{type:'3rdParty',init:()=>undefined},useTranslation:()=>({t:(key:string)=>key})}));
vi.mock('./ArtifactRewriteDialog',()=>({ArtifactRewriteInlineDiff:()=>null}));
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
beforeEach(()=>{capture.change=undefined;});
it('preserves file margins when real MDX does not emit initial normalization',async()=>{
 const onSave=vi.fn(async(markdown:string)=>({markdown,revision:2}));
 const {container}=render(<MarkdownArtifactEditor markdown={'Old plain\n'} sourceRevision={1} onSave={onSave} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Old plain'));
 await act(async()=>capture.change?.('New plain',false));
 await waitFor(()=>expect(onSave).toHaveBeenCalledTimes(1),{timeout:2500});
 expect(onSave.mock.calls[0][0]).toBe('New plain\n');
});
it('establishes a fresh source baseline after a clean document update',async()=>{
 const onSave=vi.fn(async(markdown:string)=>({markdown,revision:3}));
 const props={sourceRevision:1,onSave};
 const {container,rerender}=render(<MarkdownArtifactEditor markdown={'Old plain\n'} {...props} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Old plain'));
 rerender(<MarkdownArtifactEditor markdown={'Other plain\n\n'} {...props} sourceRevision={2} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Other plain'));
 await act(async()=>capture.change?.('Updated plain',false));
 await waitFor(()=>expect(onSave).toHaveBeenCalledTimes(1),{timeout:2500});
 expect(onSave.mock.calls[0][0]).toBe('Updated plain\n\n');
});
it('uses the returned source as the baseline when a save normalizes content',async()=>{
 let revision=1;
 const onSave=vi.fn(async(markdown:string)=>({markdown:revision++===1?'Normalized plain\n':markdown,revision}));
 const {container}=render(<MarkdownArtifactEditor markdown={'Old plain\n'} sourceRevision={1} onSave={onSave} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Old plain'));
 await act(async()=>capture.change?.('First edit',false));
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Normalized plain'),{timeout:2500});
 await act(async()=>capture.change?.('First edit',false));
 await waitFor(()=>expect(onSave).toHaveBeenCalledTimes(2),{timeout:2500});
 expect(onSave.mock.calls[1][0]).toBe('First edit\n');
});
it('preserves returned URL and ampersand spelling across another rich save',async()=>{
 let revision=1;
 const onSave=vi.fn(async(markdown:string)=>({markdown:revision++===1?'Normalized https://example.org A & B.\n':markdown,revision}));
 const {container}=render(<MarkdownArtifactEditor markdown={'Old plain\n'} sourceRevision={1} onSave={onSave} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Old plain'));
 await act(async()=>capture.change?.('First edit',false));
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Normalized https://example.org A & B.'),{timeout:2500});
 await act(async()=>capture.editor?.setMarkdown('First edit https://example.org A & B.'));
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('First edit https://example.org A & B.'));
 await act(async()=>capture.change?.(capture.editor!.getMarkdown(),false));
 await waitFor(()=>expect(onSave).toHaveBeenCalledTimes(2),{timeout:2500});
 expect(onSave.mock.calls[1][0]).toBe('First edit https://example.org A & B.\n');
});

it('keeps a newer rich edit after viewing source while an older save finishes',async()=>{
 let release: (()=>void) | undefined;
 let count=0;
 const onSave=vi.fn(async(markdown:string)=>{
   if(++count===1)await new Promise<void>(resolve=>{release=resolve;});
   return {markdown,revision:count+1};
 });
 const {container,getByRole}=render(<MarkdownArtifactEditor markdown={'Old [https://example.org](https://example.org)\n'} sourceRevision={1} onSave={onSave} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Old'));
 await act(async()=>capture.editor!.setMarkdown('First [https://example.org](https://example.org)'));
 await act(async()=>capture.change?.(capture.editor!.getMarkdown(),false));
 await waitFor(()=>expect(onSave).toHaveBeenCalledTimes(1),{timeout:2500});
 container.querySelector('details.writer-document-options')?.setAttribute('open', '');
 fireEvent.click(getByRole('button',{name:'chat.writerSource.source'}));
 const source='First [https://example.org](https://example.org)\n';
 expect(getByRole('textbox',{name:'chat.writerSource.source'})).toHaveAttribute('readonly');
 fireEvent.change(getByRole('textbox',{name:'chat.writerSource.source'}),{target:{value:source+'Attempted source edit'}});
 fireEvent.click(getByRole('button',{name:'chat.writerLocal.backToDocument'}));
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('First'));
 await act(async()=>capture.editor!.setMarkdown(source.replace('First','Second')));
 await act(async()=>capture.change?.(capture.editor!.getMarkdown(),false));
 await act(async()=>release!());
 await waitFor(()=>expect(onSave).toHaveBeenCalledTimes(2),{timeout:2500});
 expect(onSave.mock.calls[1][0]).toBe(source.replace('First','Second'));
});

it.each([
 {original:'Old plain\n',source:'Old plain\n\n',viaPreview:false},
 {original:'Old plain\n',source:'\n\nOld plain\n',viaPreview:false},
 {original:'\nOld plain\n\n',source:'Old plain',viaPreview:false},
 {original:'Old plain\n',source:'Old plain\n\n',viaPreview:true},
 {original:'First\n\nSecond\n',source:'First\n\n\nSecond\n',viaPreview:false},
])('ignores source whitespace changes and preserves the original on return: %j',async({original,source,viaPreview})=>{
 const onSave=vi.fn(async(markdown:string)=>({markdown,revision:2}));
 const {container,getByRole}=render(<MarkdownArtifactEditor markdown={original} sourceRevision={1} onSave={onSave} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).not.toBeNull());
 container.querySelector('details.writer-document-options')?.setAttribute('open', '');
 fireEvent.click(getByRole('button',{name:'chat.writerSource.source'}));
 expect(getByRole('textbox',{name:'chat.writerSource.source'})).toHaveAttribute('readonly');
 fireEvent.change(getByRole('textbox',{name:'chat.writerSource.source'}),{target:{value:source}});
 if(viaPreview){ container.querySelector('details.writer-document-options')?.removeAttribute('open'); container.querySelector('details.writer-document-options')?.setAttribute('open', ''); }
 vi.useFakeTimers();
 try {
  fireEvent.click(getByRole('button',{name:'chat.writerLocal.backToDocument'}));
  await act(async()=>vi.advanceTimersByTimeAsync(1500));
  expect(onSave).not.toHaveBeenCalled();
  container.querySelector('details.writer-document-options')?.setAttribute('open', '');
  fireEvent.click(getByRole('button',{name:'chat.writerSource.source'}));
  expect(getByRole('textbox',{name:'chat.writerSource.source'})).toHaveValue(original);
 } finally {vi.useRealTimers();}
});

it.each([false,true])('does not save attempted source changes or an unchanged mode switch: %s',async(revert)=>{
 const original='Old plain\n';
 const onSave=vi.fn(async(markdown:string)=>({markdown,revision:2}));
 const {container,getByRole}=render(<MarkdownArtifactEditor markdown={original} sourceRevision={1} onSave={onSave} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).not.toBeNull());
 container.querySelector('details.writer-document-options')?.setAttribute('open', '');
 fireEvent.click(getByRole('button',{name:'chat.writerSource.source'}));
 if(revert){
  fireEvent.change(getByRole('textbox',{name:'chat.writerSource.source'}),{target:{value:original+'\n'}});
  fireEvent.change(getByRole('textbox',{name:'chat.writerSource.source'}),{target:{value:original}});
 }
 vi.useFakeTimers();
 try {
  fireEvent.click(getByRole('button',{name:'chat.writerLocal.backToDocument'}));
  await act(async()=>vi.advanceTimersByTimeAsync(1500));
  expect(onSave).not.toHaveBeenCalled();
 } finally {vi.useRealTimers();}
});

it('preserves link and text spelling after inspecting read-only source',async()=>{
 const original='Old [https://example.org](https://example.org) A & B.\n';
 const source=original;
 let revision=1;
 const onSave=vi.fn(async(markdown:string)=>({markdown,revision:++revision}));
 const {container,getByRole}=render(<MarkdownArtifactEditor markdown={original} sourceRevision={1} onSave={onSave} />);
 await waitFor(()=>expect(container.querySelector('[contenteditable=true]')).toHaveTextContent('Old'));
 container.querySelector('details.writer-document-options')?.setAttribute('open', '');
 fireEvent.click(getByRole('button',{name:'chat.writerSource.source'}));
 fireEvent.change(getByRole('textbox',{name:'chat.writerSource.source'}),{target:{value:source+'Attempted source edit'}});
 fireEvent.click(getByRole('button',{name:'chat.writerLocal.backToDocument'}));
 expect(onSave).not.toHaveBeenCalled();
 await act(async()=>capture.editor!.setMarkdown(source.replace('Old','New')));
 await act(async()=>capture.change?.(capture.editor!.getMarkdown(),false));
 await waitFor(()=>expect(onSave).toHaveBeenCalledTimes(1),{timeout:2500});
 expect(onSave.mock.calls[0][0]).toBe(source.replace('Old','New'));
});
