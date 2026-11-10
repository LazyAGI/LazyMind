import type {ReactNode} from 'react';
import {render,screen,fireEvent,waitFor} from '@testing-library/react';
import {beforeEach,expect,it,vi} from 'vitest';
import type {PdfArtifact} from '../../api/pdfArtifacts';
const calls=vi.hoisted(()=>({rerender:vi.fn()}));
vi.mock('@/modules/knowledge/api/pdfArtifacts',()=>({pdfArtifactContentUrl:(_d:string,_p:string,id:string)=>id,rerenderPdfTranslation:calls.rerender}));
vi.mock('@/modules/knowledge/components/FileViewer',()=>({default:({file,pdfHeaderEnd}:{file:string;pdfHeaderEnd:ReactNode})=><div>{pdfHeaderEnd}<div data-testid="viewer">{file}</div></div>}));
vi.mock('../../paper/translationAttachment',()=>({createTranslationAttachmentSaver:()=>vi.fn(),fetchTranslationFile:vi.fn()}));
import FullDocumentTranslationPanel from './FullDocumentTranslationPanel';
const artifact={id:'old',filename:'paper.pdf',has_draft:true} as PdfArtifact;
const updated={...artifact,id:'new',layout_preserved:true,supplemental_block_count:2};
beforeEach(()=>vi.clearAllMocks());
function setup(){
 const onArtifactUpdated=vi.fn(),onTranslate=vi.fn(),onRegenerate=vi.fn();
 render(<FullDocumentTranslationPanel datasetId="library" documentId="paper" artifact={artifact} canWrite supported busy={false} onTranslate={onTranslate} onRegenerate={onRegenerate} onRetranslateSelection={vi.fn()} onArtifactUpdated={onArtifactUpdated}/>);
 return {onArtifactUpdated,onTranslate,onRegenerate};
}
it('rerenders saved translation without requesting translation and publishes only after success',async()=>{
 calls.rerender.mockResolvedValue(updated);
 const callbacks=setup();
 fireEvent.click(screen.getByRole('button',{name:'重新排版（免翻译）'}));
 await waitFor(()=>expect(callbacks.onArtifactUpdated).toHaveBeenCalledWith(updated));
 expect(calls.rerender).toHaveBeenCalledWith('library','paper','old');
 expect(callbacks.onTranslate).not.toHaveBeenCalled();
 expect(callbacks.onRegenerate).not.toHaveBeenCalled();
});
it('keeps the current artifact when rerender fails',async()=>{
 calls.rerender.mockRejectedValue(new Error('render failed'));
 const callbacks=setup();
 fireEvent.click(screen.getByRole('button',{name:'重新排版（免翻译）'}));
 await waitFor(()=>expect(screen.queryByRole('status')).not.toBeInTheDocument());
 expect(callbacks.onArtifactUpdated).not.toHaveBeenCalled();
 expect(screen.getByTestId('viewer')).toHaveTextContent('old');
});
it('asks old appendix artifacts to rerender instead of offering appendix navigation',()=>{
 render(<FullDocumentTranslationPanel datasetId="library" documentId="paper" artifact={{...artifact,layout_preserved:true,supplemental_block_count:2}} canWrite supported busy={false} onTranslate={vi.fn()} onRegenerate={vi.fn()} onArtifactUpdated={vi.fn()}/>);
 expect(screen.getByText('这是旧版排版结果，请点击“重新排版”生成页内译文。')).toBeInTheDocument();
 expect(screen.queryByText(/点击原页蓝框/)).not.toBeInTheDocument();
});
