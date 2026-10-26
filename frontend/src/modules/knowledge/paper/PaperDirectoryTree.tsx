import { useEffect, useMemo, useRef, useState } from 'react';
import { Tree, Tooltip, type TreeDataNode } from 'antd';
import { DatabaseOutlined, DownOutlined, FolderOutlined, ReadOutlined, StarFilled } from '@ant-design/icons';
import { useDroppable } from '@dnd-kit/core';
import { descendantFolderIds, type PaperLibraryData } from './model';
export const PAPER_LIBRARY_ROOT = 'paper-library-root';
interface DirectoryNode extends TreeDataNode {
    key: string;
    label: string;
    folderId?: string | null;
    paperId?: string;
    count?: number;
    important?: boolean;
}
interface Props {
    data: PaperLibraryData;
    title: string;
    active: boolean;
    folderId: string | null;
    detailPaperId: string | null;
    disabled: boolean;
    draggedIds: string[];
    dropFolderId: string | null;
    onSelectFolder: (id: string | null) => void;
    onOpenPaper: (id: string) => void;
}
function FolderTitle({ node, acceptsDrop, dropTarget }: {
    node: DirectoryNode;
    acceptsDrop: boolean;
    dropTarget: boolean;
}) {
    const { setNodeRef, isOver } = useDroppable({ id: node.key, disabled: !acceptsDrop });
    return <span ref={setNodeRef} className={`paper-directory-folder-title ${dropTarget || isOver ? 'is-drop-target' : ''}`}><Tooltip title={node.label}><span className="paper-directory-node-name">{node.label}</span></Tooltip><span className="paper-directory-count" title={`含子目录共 ${node.count} 篇论文`}>{node.count}</span></span>;
}
export default function PaperDirectoryTree(props: Props) {
    const { data, title, active, folderId, detailPaperId, disabled, draggedIds, dropFolderId, onSelectFolder, onOpenPaper } = props;
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
                count: descendants ? data.papers.filter(paper => !!paper.folderId && descendants.has(paper.folderId)).length : data.papers.length,
                children: [
                    ...data.folders.filter(folder => (folder.parentId || null) === id).map(folder => build(folder.id, folder.name)),
                    ...data.papers.filter(paper => paper.folderId === id).map(paper => ({ key: `paper:${paper.id}`, label: paper.metadata.title, paperId: paper.id, important: paper.important, isLeaf: true, icon: <ReadOutlined /> })),
                ],
            };
        };
        return [build(null, title)];
    }, [data, title]);
    return <Tree<DirectoryNode> className="paper-directory-tree" aria-label="论文目录" showIcon blockNode switcherIcon={<DownOutlined />} treeData={nodes} expandedKeys={expanded} selectedKeys={detailPaperId ? [`paper:${detailPaperId}`] : active ? [folderId || PAPER_LIBRARY_ROOT] : []} onExpand={keys => setExpanded(keys.map(String))} onSelect={(_, info) => {
            if (info.node.paperId)
                onOpenPaper(info.node.paperId);
            else
                onSelectFolder(info.node.folderId || null);
        }} titleRender={node => {
            if (node.paperId)
                return <span className="paper-directory-paper-title"><Tooltip title={node.label}><span className="paper-directory-node-name">{node.label}</span></Tooltip>{node.important && <StarFilled className="paper-directory-star"/>}</span>;
            const target = node.folderId || 'none';
            const acceptsDrop = !disabled && data.papers.some(paper => draggedIds.includes(paper.id) && paper.folderId !== (node.folderId || null));
            return <FolderTitle node={node} acceptsDrop={acceptsDrop} dropTarget={dropFolderId === target}/>;
        }}/>;
}
