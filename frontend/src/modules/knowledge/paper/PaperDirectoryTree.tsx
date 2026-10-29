import { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Tree, Tooltip, type TreeDataNode } from 'antd';
import { DatabaseOutlined, DeleteOutlined, DownOutlined, FolderOutlined, PlusOutlined } from '@ant-design/icons';
import { useDroppable } from '@dnd-kit/core';
import { descendantFolderIds, paperBelongsToFolders, type PaperLibraryData } from './model';
export const PAPER_LIBRARY_ROOT = 'paper-library-root';
interface DirectoryNode extends TreeDataNode {
    key: string;
    label: string;
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
    return <span ref={setNodeRef} className={`paper-directory-folder-title ${dropTarget || isOver ? 'is-drop-target' : ''}`}><Tooltip title={node.label}><span className="paper-directory-node-name">{node.label}</span></Tooltip><span className="paper-directory-count" title={`含子目录共 ${node.count} 篇论文`}>{node.count}</span><span className="paper-directory-actions" onClick={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
      <Tooltip title="新建子目录"><Button type="text" size="small" disabled={disabled} icon={<PlusOutlined />} aria-label={`在「${node.label}」中新建文件夹`} onClick={() => onCreateFolder(node.folderId || null)} /></Tooltip>
      {node.folderId && <Tooltip title="删除目录"><Button type="text" size="small" disabled={disabled} icon={<DeleteOutlined />} aria-label={`删除文件夹「${node.label}」`} onClick={() => onDeleteFolder(node.folderId!)} /></Tooltip>}
    </span></span>;
}
export default function PaperDirectoryTree(props: Props) {
    const { data, title, active, folderId, disabled, draggedIds, dropFolderId, onSelectFolder, onCreateFolder, onDeleteFolder } = props;
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
        const build = (id: string | null, label: string): DirectoryNode => {
            const descendants = id ? descendantFolderIds(data.folders, id) : null;
            return {
                key: id || PAPER_LIBRARY_ROOT, label, folderId: id,
                icon: id ? <FolderOutlined /> : <DatabaseOutlined />,
                count: descendants ? data.papers.filter(paper => paperBelongsToFolders(paper, descendants)).length : data.papers.length,
                children: data.folders.filter(folder => (folder.parentId || null) === id).map(folder => build(folder.id, folder.name)),
            };
        };
        return [build(null, title)];
    }, [data, title]);
    return <Tree<DirectoryNode> className="paper-directory-tree" aria-label="论文目录" showIcon blockNode switcherIcon={<DownOutlined />} treeData={nodes} expandedKeys={expanded} selectedKeys={active ? [folderId || PAPER_LIBRARY_ROOT] : []} onExpand={keys => setExpanded(keys.map(String))} onSelect={(_, info) => {
            onSelectFolder(info.node.folderId || null);
        }} titleRender={node => {
            const target = node.folderId || 'none';
            const acceptsDrop = !disabled && data.papers.some(paper => draggedIds.includes(paper.id) && (paper.folderId !== (node.folderId || null) || paper.collectionIds?.some(id => id !== node.folderId)));
            return <FolderTitle node={node} acceptsDrop={acceptsDrop} dropTarget={dropFolderId === target} disabled={disabled} onCreateFolder={onCreateFolder} onDeleteFolder={onDeleteFolder}/>;
        }}/>;
}
