import type {ReactNode} from 'react';
import {render,screen,fireEvent} from '@testing-library/react';
import {expect,it,vi} from 'vitest';
import type {PdfArtifact} from '../../api/pdfArtifacts';
vi.mock('@/modules/knowledge/api/pdfArtifacts',()=>({pdfArtifactContentUrl:(_d:string,_p:string,id:string)=>id}));
vi.mock('@/modules/knowledge/components/FileViewer',()=>({default:({file,pdfHeaderEnd}:{file:string;pdfHeaderEnd:ReactNode})=><div>{pdfHeaderEnd}<div data-testid="viewer">{file}</div></div>}));
vi.mock('../../paper/translationAttachment',()=>({createTranslationAttachmentSaver:()=>vi.fn(),fetchTranslationFile:vi.fn()}));
import FullDocumentTranslationPanel from './FullDocumentTranslationPanel';
const artifact={id:'old',filename:'paper.pdf',has_draft:true,layout_preserved:true,supplemental_block_count:2,warning_count:3} as PdfArtifact;
function setup(progressNotice?:ReactNode){
 const onHalfWidth=vi.fn(),onRegenerate=vi.fn();
 const view=render(<FullDocumentTranslationPanel datasetId="library" documentId="paper" artifact={artifact} canWrite supported busy={false} onTranslate={vi.fn()} onRegenerate={onRegenerate} onRetranslateSelection={vi.fn()} onHalfWidth={onHalfWidth} progressNotice={progressNotice}/>);
 return {...view,onHalfWidth,onRegenerate};
}
it('shows the translated document without advisory banners or a rerender action',()=>{
 const {container}=setup();
 expect(container.querySelector('.ant-alert')).toBeNull();
 expect(screen.queryByText(/重新排版|保留原文|旧版排版/)).not.toBeInTheDocument();
 expect(screen.getByTestId('viewer')).toHaveTextContent('old');
 for(const name of ['重新翻译','下载译本','加入文献附件','半屏阅读']) expect(screen.getByRole('button',{name})).toBeInTheDocument();
});
it('changes the reading width only when clicked',()=>{
 const {onHalfWidth}=setup();
 const button=screen.getByRole('button',{name:'半屏阅读'});
 fireEvent.mouseEnter(button);
 expect(onHalfWidth).not.toHaveBeenCalled();
 fireEvent.click(button);
 expect(onHalfWidth).toHaveBeenCalledTimes(1);
});
it('preserves explicit translation progress and regeneration',()=>{
 const {onRegenerate}=setup(<div role="status">正在翻译文档</div>);
 expect(screen.getByRole('status')).toHaveTextContent('正在翻译文档');
 fireEvent.click(screen.getByRole('button',{name:'重新翻译'}));
 expect(onRegenerate).toHaveBeenCalledWith(artifact);
});
