import { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Tree, Tooltip, type TreeDataNode } from 'antd';
import { DeleteOutlined, DownOutlined, FolderOutlined, PlusOutlined } from '@ant-design/icons';
import { useDroppable } from '@dnd-kit/core';
import type { PaperLibraryData } from './model';
import { indexFolderCounts } from './libraryIndex';
interface DirectoryNode extends TreeDataNode {
    key: string;
    label: string;
    path: string;
    folderId?: string | null;
    count?: number;
}
interface Props {
    data: PaperLibraryData;
    title: string;
    active: boolean;
    folderId: string | null;
    disabled: boolean;
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
export default function PaperDirectoryTree(props: Props) {
    const { data, title, active, folderId, disabled, draggedIds, dropFolderId, onSelectFolder, onCreateFolder, onDeleteFolder } = props;
    const [expanded, setExpanded] = useState<string[]>([]);
    const initialized = useRef(false);
    useEffect(() => {
        if (!initialized.current && data.folders.length) {
            initialized.current = true;
            setExpanded(data.folders.map(folder => folder.id));
        }
    }, [data.folders]);
    useEffect(() => {
        if (!active)
            return;
        const parents = new Set<string>();
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
        const build = (id: string, label: string, parentPath: string): DirectoryNode => ({
            key: id, label, path: `${parentPath} / ${label}`, folderId: id,
            icon: <FolderOutlined />,
            count: counts.get(id) || 0,
            children: (children.get(id) || []).map(folder => build(folder.id, folder.name, `${parentPath} / ${label}`)),
        });
        return (children.get(null) || []).map(folder => build(folder.id, folder.name, title));
    }, [data.papers, data.folders, title]);
    const draggedPapers = useMemo(() => {
        const ids = new Set(draggedIds);
        return data.papers.filter(paper => ids.has(paper.id));
    }, [data.papers, draggedIds]);
    return <Tree<DirectoryNode> className="paper-directory-tree" aria-label="论文目录" showIcon blockNode switcherIcon={<DownOutlined />} treeData={nodes} expandedKeys={expanded} selectedKeys={active && folderId ? [folderId] : []} onExpand={keys => setExpanded(keys.map(String))} onSelect={(_, info) => {
            onSelectFolder(info.node.folderId || null);
        }} titleRender={node => {
            const target = node.folderId || 'none';
            const acceptsDrop = !disabled && draggedPapers.some(paper => (paper.folderId !== (node.folderId || null) || paper.collectionIds?.some(id => id !== node.folderId)));
            return <FolderTitle node={node} acceptsDrop={acceptsDrop} dropTarget={dropFolderId === target} disabled={disabled} onCreateFolder={onCreateFolder} onDeleteFolder={onDeleteFolder}/>;
        }}/>;
}
