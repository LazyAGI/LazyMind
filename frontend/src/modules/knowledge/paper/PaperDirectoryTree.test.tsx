import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { DndContext } from '@dnd-kit/core';
import { afterEach, expect, it, vi } from 'vitest';
import PaperDirectoryTree from './PaperDirectoryTree';
import { libraryFixture, paperFixture } from './__tests__/fixtures';

afterEach(cleanup);
it('shows only folders, preserves nesting and counts, and allows folder selection and actions', () => {
    const select = vi.fn(); const create = vi.fn(); const remove = vi.fn();
    const data = libraryFixture({ folders: [{id:'parent',name:'父目录',parentId:null},{id:'child',name:'子目录',parentId:'parent'}], papers: [paperFixture({folderId:'child'})] });
    render(<DndContext><PaperDirectoryTree data={data} title="论文库" active folderId="child" disabled={false} draggedIds={['paper']} dropFolderId="parent" onSelectFolder={select} onCreateFolder={create} onDeleteFolder={remove} /></DndContext>);
    expect(screen.queryByText('A study')).not.toBeInTheDocument();
    expect(screen.queryByRole('button',{name:/拖动目录论文/})).not.toBeInTheDocument();
    expect(screen.getAllByRole('treeitem')).toHaveLength(2);
    expect(screen.getAllByTitle('含子目录共 1 篇论文')).toHaveLength(2);
    fireEvent.click(screen.getByText('子目录')); expect(select).toHaveBeenLastCalledWith('child');
    fireEvent.click(screen.getByRole('button',{name:'在「父目录」中新建文件夹'})); expect(create).toHaveBeenLastCalledWith('parent');
    fireEvent.click(screen.getByRole('button',{name:'删除文件夹「子目录」'})); expect(remove).toHaveBeenLastCalledWith('child');
    expect(screen.getByText('父目录').closest('.paper-directory-folder-title')).toHaveClass('is-drop-target');
});

it('does not render a virtual root or unfiled papers in an empty directory', () => {
    render(<DndContext><PaperDirectoryTree data={libraryFixture()} title="论文库" active folderId={null} disabled={false} draggedIds={[]} dropFolderId={null} onSelectFolder={vi.fn()} onCreateFolder={vi.fn()} onDeleteFolder={vi.fn()} /></DndContext>);
    expect(screen.queryAllByRole('treeitem')).toHaveLength(0);
    expect(screen.queryByText('论文库')).not.toBeInTheDocument();
    expect(screen.queryByText('A study')).not.toBeInTheDocument();
});

it('shows the full folder path on hover', async () => {
 const data = libraryFixture({folders:[{id:'a',name:'父目录',parentId:null},{id:'b',name:'子目录',parentId:'a'}]});
 render(<DndContext><PaperDirectoryTree data={data} title="论文库" active folderId="b" disabled={false} draggedIds={[]} dropFolderId={null} onSelectFolder={vi.fn()} onCreateFolder={vi.fn()} onDeleteFolder={vi.fn()} /></DndContext>);
 fireEvent.mouseOver(screen.getByText('子目录'));
 expect(await screen.findByRole('tooltip')).toHaveTextContent('论文库 / 父目录 / 子目录');
});
