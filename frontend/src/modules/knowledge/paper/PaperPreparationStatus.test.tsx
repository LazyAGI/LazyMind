import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import PaperPreparationStatus from './PaperPreparationStatus';
const api = vi.hoisted(() => ({ chat: vi.fn(), refresh: vi.fn(), loading: false }));
vi.mock('../api/pdfIR', () => ({ usePdfIR: () => ({loading:api.loading,state:{status:'ready',document:{blocks:[{type:'formula'},{type:'image'}]}}, refresh:api.refresh}) }));
vi.mock('../api/pdfArtifacts', () => ({ getDocumentParseStatus:api.chat }));
afterEach(() => {cleanup();vi.useRealTimers();vi.resetAllMocks();api.loading=false;});
it('shows independent parse states and stops polling when closed', async () => {
 vi.useFakeTimers();api.chat.mockResolvedValue({status:'parsing'});
 const view=render(<PaperPreparationStatus target={{dataset:'ds',document:'doc'}}/>);
 await act(async()=>{});
 expect(screen.getByText('已就绪')).toBeInTheDocument();
 expect(screen.getByText('准备中')).toBeInTheDocument();
 expect(screen.getByText('识别到 1 个公式、1 张图片、0 个表格')).toBeInTheDocument();
 await act(async()=>{await vi.advanceTimersByTimeAsync(2000);});
 expect(api.chat).toHaveBeenCalledTimes(2);
 view.unmount();await act(async()=>{await vi.advanceTimersByTimeAsync(5000);});
 expect(api.chat).toHaveBeenCalledTimes(2);
});
it('keeps the ready structure visible when chat status fails', async () => {
 api.chat.mockRejectedValue(new Error('offline'));
 render(<PaperPreparationStatus target={{dataset:'ds',document:'doc'}}/>);
 expect(await screen.findByText('读取失败')).toBeInTheDocument();
 expect(screen.getByText('已就绪')).toBeInTheDocument();
});

it('does not claim a structure status before its first observation completes', () => {
 api.loading=true;api.chat.mockImplementation(()=>new Promise(()=>{}));
 render(<PaperPreparationStatus target={{dataset:'ds',document:'pending'}}/>);
 expect(screen.getAllByText('读取中')).toHaveLength(2);
 expect(screen.queryByText('已就绪')).not.toBeInTheDocument();
 expect(screen.queryByText('尚未解析')).not.toBeInTheDocument();
});

it('shows every study capability and actual partial counts independently of batch success', async () => {
 api.chat.mockResolvedValue({status:'parsed'});
 render(<PaperPreparationStatus target={{dataset:'ds',document:'doc'}} capabilities={[
  {id:'formula',status:'partial',completed:2,total:8},
  {id:'figure',status:'running',completed:1,total:3,running:1},
  {id:'table',status:'empty',completed:0,total:0},
 ]}/>);
 await act(async()=>{});
 expect(screen.getByText('公式研读')).toBeInTheDocument();
 expect(screen.getByText('已完成 2 / 8 项')).toBeInTheDocument();
 expect(screen.getByText('部分完成')).toBeInTheDocument();
 expect(screen.getByText('进行中')).toBeInTheDocument();
 expect(screen.getByText('本文无此内容')).toBeInTheDocument();
});

it('keeps observing an initially idle document and cleans up on unmount', async () => {
 vi.useFakeTimers();api.chat.mockResolvedValue({status:'not_requested'});
 const view=render(<PaperPreparationStatus target={{dataset:'ds',document:'doc'}}/>);
 await act(async()=>{});
 api.chat.mockResolvedValue({status:'parsing'});
 await act(async()=>{await vi.advanceTimersByTimeAsync(5000);});
 expect(screen.getByText('准备中')).toBeInTheDocument();
 expect(api.refresh).toHaveBeenCalled();
 view.unmount();const reads=api.chat.mock.calls.length;
 await act(async()=>{await vi.advanceTimersByTimeAsync(5000);});
 expect(api.chat).toHaveBeenCalledTimes(reads);
});
