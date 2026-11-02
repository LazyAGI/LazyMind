import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { DndContext } from '@dnd-kit/core';
import { afterEach, expect, it, vi } from 'vitest';
import PaperDirectoryTree from './PaperDirectoryTree';
import { libraryFixture } from './__tests__/fixtures';

afterEach(cleanup);
it('starts a directory paper drag with the same paper identity used by library moves', async () => {
    const start = vi.fn();
    render(<DndContext onDragStart={start}><PaperDirectoryTree data={libraryFixture()} title="论文库" active folderId={null} detailPaperId={null} disabled={false} draggedIds={[]} dropFolderId={null} onSelectFolder={vi.fn()} onOpenPaper={vi.fn()} onCreateFolder={vi.fn()} onDeleteFolder={vi.fn()} /></DndContext>);
    const event = new MouseEvent('pointerdown', { bubbles: true, button: 0, clientX: 20, clientY: 20 });
    Object.defineProperty(event, 'isPrimary', { value: true });
    fireEvent(screen.getByRole('button', { name: '拖动目录论文 A study' }), event);
    await waitFor(() => expect(start).toHaveBeenCalledOnce());
    expect(start.mock.calls[0][0].active.data.current.paperId).toBe('paper');
    fireEvent(document, new MouseEvent('pointercancel', { bubbles: true }));
});
