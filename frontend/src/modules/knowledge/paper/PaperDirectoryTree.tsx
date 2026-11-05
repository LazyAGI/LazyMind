import { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Tree, Tooltip, type TreeDataNode } from 'antd';
import { DatabaseOutlined, DeleteOutlined, DownOutlined, FolderOutlined, PlusOutlined, ReadOutlined, StarFilled } from '@ant-design/icons';
import { useDraggable, useDroppable } from '@dnd-kit/core';
import type { PaperLibraryData } from './model';
import { indexFolderCounts } from './libraryIndex';
export const PAPER_LIBRARY_ROOT = 'paper-library-root';
interface DirectoryNode extends TreeDataNode {
    key: string;
    label: string;
    path: string;
    folderId?: string | null;
    count?: number;
    paperId?: string;
    important?: boolean;
}
interface Props {
    data: PaperLibraryData;
    title: string;
    active: boolean;
    folderId: string | null;
    disabled: boolean;
    detailPaperId?: string | null;
    onOpenPaper?: (id: string) => void;
    draggedIds: string[];
    dropFolderId: string | null;
    onSelectFolder: (id: string | null) => void;
    onCreateFolder: (parentId: string | null) => void;
    onDeleteFolder: (id: string) => void;
}
function FolderTitle({ node, acceptsDrop, dropTarget, disabled, onCreateFolder, onDeleteFolder }: {
    node: DirectoryNode;
    acceptsDrop: boolean;
    dropTarget: boolean;
    disabled: boolean;
    onCreateFolder: Props['onCreateFolder'];
    onDeleteFolder: Props['onDeleteFolder'];
}) {
    const { setNodeRef, isOver } = useDroppable({ id: node.key, disabled: !acceptsDrop });
    return <span ref={setNodeRef} className={`paper-directory-folder-title ${dropTarget || isOver ? 'is-drop-target' : ''}`}><Tooltip title={node.path}><span className="paper-directory-node-name">{node.label}</span></Tooltip><span className="paper-directory-count" title={`含子目录共 ${node.count} 篇论文`}>{node.count}</span><span className="paper-directory-actions" onClick={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
      <Tooltip title="新建子目录"><Button type="text" size="small" disabled={disabled} icon={<PlusOutlined />} aria-label={`在「${node.label}」中新建文件夹`} onClick={() => onCreateFolder(node.folderId || null)} /></Tooltip>
      {node.folderId && <Tooltip title="删除目录"><Button type="text" size="small" disabled={disabled} icon={<DeleteOutlined />} aria-label={`删除文件夹「${node.label}」`} onClick={() => onDeleteFolder(node.folderId!)} /></Tooltip>}
    </span></span>;
}
function PaperTitle({ node, disabled }: { node: DirectoryNode; disabled: boolean }) {
    const { setNodeRef, attributes, listeners, isDragging } = useDraggable({ id: `tree-paper:${node.paperId}`, data: { paperId: node.paperId }, disabled });
    return <span ref={setNodeRef} {...attributes} {...listeners} aria-label={`拖动目录论文 ${node.label}`} className={`paper-directory-paper-title ${isDragging ? 'is-dragging' : ''}`}><Tooltip title={node.label}><span className="paper-directory-node-name">{node.label}</span></Tooltip>{node.important && <StarFilled className="paper-directory-star" />}</span>;
}
export default function PaperDirectoryTree(props: Props) {
    const { data, title, active, folderId, disabled, draggedIds, dropFolderId, onSelectFolder, onCreateFolder, onDeleteFolder, onOpenPaper, detailPaperId } = props;
    const [expanded, setExpanded] = useState<string[]>([PAPER_LIBRARY_ROOT]);
    const initialized = useRef(false);
    useEffect(() => {
        if (!initialized.current && data.folders.length) {
            initialized.current = true;
            setExpanded([PAPER_LIBRARY_ROOT, ...data.folders.map(folder => folder.id)]);
        }
    }, [data.folders]);
    useEffect(() => {
        if (!active)
            return;
        const parents = new Set<string>([PAPER_LIBRARY_ROOT]);
        let id = folderId;
        while (id && !parents.has(id)) {
            parents.add(id);
            id = data.folders.find(folder => folder.id === id)?.parentId || null;
        }
        setExpanded(current => [...new Set([...current, ...parents])]);
    }, [active, folderId, data.folders]);
    const nodes = useMemo(() => {
        const { counts } = indexFolderCounts(data);
        const children = new Map<string | null, typeof data.folders>();
        for (const folder of data.folders) {
            const parent = folder.parentId || null;
            const siblings = children.get(parent) || [];
            siblings.push(folder); children.set(parent, siblings);
        }
        const papersByFolder = new Map<string | null, typeof data.papers>();
        if (onOpenPaper) for (const paper of data.papers) {
            const id = paper.folderId || null;
            const papers = papersByFolder.get(id) || [];
            papers.push(paper);
            papersByFolder.set(id, papers);
        }
        const build = (id: string | null, label: string, parentPath = ''): DirectoryNode => ({
            key: id || PAPER_LIBRARY_ROOT, label, path: parentPath ? `${parentPath} / ${label}` : label, folderId: id,
            icon: id ? <FolderOutlined /> : <DatabaseOutlined />,
            count: id ? counts.get(id) || 0 : data.papers.length,
            children: [
                ...(children.get(id) || []).map(folder => build(folder.id, folder.name, parentPath ? `${parentPath} / ${label}` : label)),
                ...(onOpenPaper ? (papersByFolder.get(id) || []).map(paper => ({ key: `paper:${paper.id}`, path: paper.metadata.title, label: paper.metadata.title, paperId: paper.id, important: paper.important, isLeaf: true, icon: <ReadOutlined /> })) : []),
            ],
        });
        return [build(null, title)];
    }, [data.papers, data.folders, title, onOpenPaper]);
    const draggedPapers = useMemo(() => {
        const ids = new Set(draggedIds);
        return data.papers.filter(paper => ids.has(paper.id));
    }, [data.papers, draggedIds]);
    return <Tree<DirectoryNode> className="paper-directory-tree" aria-label="论文目录" showIcon blockNode switcherIcon={<DownOutlined />} treeData={nodes} expandedKeys={expanded} selectedKeys={detailPaperId ? [`paper:${detailPaperId}`] : active ? [folderId || PAPER_LIBRARY_ROOT] : []} onExpand={keys => setExpanded(keys.map(String))} onSelect={(_, info) => {
            if (info.node.paperId) onOpenPaper?.(info.node.paperId);
            else onSelectFolder(info.node.folderId || null);
        }} titleRender={node => {
            if (node.paperId) return <PaperTitle node={node} disabled={disabled}/>;
            const target = node.folderId || 'none';
            const acceptsDrop = !disabled && draggedPapers.some(paper => (paper.folderId !== (node.folderId || null) || paper.collectionIds?.some(id => id !== node.folderId)));
            return <FolderTitle node={node} acceptsDrop={acceptsDrop} dropTarget={dropFolderId === target} disabled={disabled} onCreateFolder={onCreateFolder} onDeleteFolder={onDeleteFolder}/>;
        }}/>;
}
