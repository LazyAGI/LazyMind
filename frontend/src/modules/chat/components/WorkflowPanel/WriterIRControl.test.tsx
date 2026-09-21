import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { WriterIRControl } from './WriterIRControl';
vi.mock('./WriterIRDocumentEditor',()=>({WriterIRDocumentEditor:()=>null}));
vi.mock('@/modules/knowledge/utils/imageUrl',()=>({resolveMarkdownImageUrlAsync:async(value:string)=>value}));
vi.mock('react-i18next',()=>({useTranslation:()=>({t:(key:string)=>key})}));

it('renders structured tables, formulas and existing task state in the read view',()=>{
 const {container}=render(<WriterIRControl readOnly document={{document_id:'read-doc',stage:'final',title:'Read',blocks:[
  {node_id:'table',type:'table',children:[{node_id:'row',type:'table_row',children:[{node_id:'cell',type:'table_cell',content:'Heading',numbering:{header:true,column_span:2,align:"right"}}]}]},
  {node_id:'math',type:'math',content:'$$x^2$$',editable:false},
  {node_id:'task',type:'list_item',content:'Done',numbering:{task:true,checked:true}},
 ]}} />);
 expect(screen.getByRole('columnheader',{name:'Heading'})).toHaveAttribute('colspan','2');
 expect(screen.getAllByText('Heading')).toHaveLength(1);
 expect(screen.getByRole('columnheader',{name:'Heading'})).toHaveStyle({textAlign:'right'});
 expect(container.querySelector('.katex')).not.toBeNull();
 expect(screen.getByRole('checkbox',{name:'Done'})).toBeChecked();
});
it('shows a historical Markdown table without inventing or saving a new IR grid',()=>{
 const onDocumentChange=vi.fn();
 const onSave=vi.fn();
 render(<WriterIRControl readOnly onSave={onSave} onDocumentChange={onDocumentChange} document={{document_id:'legacy-doc',stage:'final',title:'Legacy',blocks:[{node_id:'table',type:'table',content:'| Metric | Value |\n|---|---|\n| DAU | 100 |'}]}} />);
 expect(screen.getByRole('cell',{name:'DAU'})).toBeInTheDocument();
 expect(onSave).not.toHaveBeenCalled();
 expect(onDocumentChange).toHaveBeenLastCalledWith({document_id:'legacy-doc',stage:'final',title:'Legacy',blocks:[{node_id:'table',type:'table',content:'| Metric | Value |\n|---|---|\n| DAU | 100 |'}]});
});
