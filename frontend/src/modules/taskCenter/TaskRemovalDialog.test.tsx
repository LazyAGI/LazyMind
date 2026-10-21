import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import TaskRemovalDialog from './TaskRemovalDialog';
import { getTaskRemovalPreview, removeTask, type Task } from './api';
vi.mock('./api',()=>({getTaskRemovalPreview:vi.fn(),removeTask:vi.fn()}));
vi.mock('react-i18next',()=>({useTranslation:()=>({t:(key:string,args?:Record<string,unknown>)=>key+(args?.tasks ? ` ${args.tasks}/${args.conversations}`:'')})}));
const task={id:'t1',title:'Example',conversation_id:'c'} as Task;
const preview={task_id:'t1',task_count:3,conversation_count:1,active_task_count:1,impact_version:'v1'};
const result={removed_task_ids:['t1'],trashed_conversation_ids:['c'],cancellation_pending:true};
beforeEach(()=>{vi.resetAllMocks();vi.mocked(getTaskRemovalPreview).mockResolvedValue(preview);vi.mocked(removeTask).mockResolvedValue(result)});
it('previews all affected tasks and cancel does not submit',async()=>{
 const close=vi.fn();render(<TaskRemovalDialog task={task} onClose={close}/>);
 await screen.findByText('taskCenter.removalImpact 3/1');
 expect(removeTask).not.toHaveBeenCalled();fireEvent.click(screen.getByText('common.cancel'));expect(close).toHaveBeenCalledOnce();expect(removeTask).not.toHaveBeenCalled();
});
it('requires confirmation and prevents duplicate requests while pending',async()=>{
 let resolve!:(value:typeof result)=>void;vi.mocked(removeTask).mockReturnValue(new Promise(done=>{resolve=done}));
 const close=vi.fn();render(<TaskRemovalDialog task={task} onClose={close}/>);await screen.findByText('taskCenter.removalImpact 3/1');
 fireEvent.click(screen.getByText('settingsPage.recovery.moveToTrash'));fireEvent.click(screen.getByText('settingsPage.recovery.moveToTrash'));
 expect(removeTask).toHaveBeenCalledTimes(1);expect(removeTask).toHaveBeenCalledWith('t1','v1');expect(close).not.toHaveBeenCalled();
 await act(async()=>resolve(result));await waitFor(()=>expect(close).toHaveBeenCalledOnce());
});
it('preserves the dialog and selection on failure and permits retry',async()=>{
 vi.mocked(removeTask).mockRejectedValueOnce(new Error('unavailable'));
 const close=vi.fn();render(<TaskRemovalDialog task={task} onClose={close}/>);await screen.findByText('taskCenter.removalImpact 3/1');
 fireEvent.click(screen.getByText('settingsPage.recovery.moveToTrash'));await screen.findByText('taskCenter.taskRemoveFailed');expect(close).not.toHaveBeenCalled();
 fireEvent.click(screen.getByText('settingsPage.recovery.moveToTrash'));await waitFor(()=>expect(close).toHaveBeenCalledOnce());
});
it('refreshes changed impact and requires a second explicit confirmation',async()=>{
 vi.mocked(removeTask).mockRejectedValueOnce({response:{data:{code:2004811}}});vi.mocked(getTaskRemovalPreview).mockResolvedValueOnce(preview).mockResolvedValue({...preview,task_count:4,impact_version:'v2'});
 const close=vi.fn();render(<TaskRemovalDialog task={task} onClose={close}/>);await screen.findByText('taskCenter.removalImpact 3/1');fireEvent.click(screen.getByText('settingsPage.recovery.moveToTrash'));
 await screen.findByText('taskCenter.removalImpact 4/1');expect(removeTask).toHaveBeenCalledTimes(1);expect(close).not.toHaveBeenCalled();
 fireEvent.click(screen.getByText('settingsPage.recovery.moveToTrash'));await waitFor(()=>expect(removeTask).toHaveBeenLastCalledWith('t1','v2'));
});
it('does not apply a stale preview after switching tasks',async()=>{
 let resolve!:(value:typeof preview)=>void;vi.mocked(getTaskRemovalPreview).mockReturnValueOnce(new Promise(done=>{resolve=done}));
 const view=render(<TaskRemovalDialog task={task} onClose={vi.fn()}/>);
 view.rerender(<TaskRemovalDialog task={{...task,id:'t2'}} onClose={vi.fn()}/>);await screen.findByText('taskCenter.removalImpact 3/1');
 await act(async()=>resolve({...preview,task_count:99}));expect(screen.queryByText('taskCenter.removalImpact 99/1')).not.toBeInTheDocument();
});
