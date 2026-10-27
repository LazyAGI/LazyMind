import { usePaperImports, PaperImportDialog } from './usePaperImports';
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type ChangeEvent } from "react";
import { Alert, Button, Dropdown, Input, Modal, Popover, Select, Spin, Tooltip, message } from "antd";
import { DndContext, DragOverlay, PointerSensor, pointerWithin, useDraggable, useSensor, useSensors, type DragEndEvent, type DragStartEvent, type DragOverEvent } from "@dnd-kit/core";
import { ArrowLeftOutlined, DownOutlined, FileTextOutlined, FolderOutlined, LinkOutlined, PlusOutlined, DeleteOutlined, StarOutlined, CloseOutlined, UploadOutlined, FilterOutlined, ExportOutlined, HolderOutlined, EllipsisOutlined } from "@ant-design/icons";
import { v4 as uuidv4 } from "uuid";
import { applyPaperEdits, citationLabel, descendantFolderIds, papersInView, paperBelongsToFolders, paperFolderPath, filterPapers, filterLibraryView, formatCitation, paperLink, venueName, safePaperUrl, statusLabel, type PaperEdits, type CitationStyle, type LibraryView, type PaperRecord, type PaperLibraryData, type ReadingStatus } from "./model";
import { loadPersistedPaperLibrary, mergeLibraryChanges, patchLibraryPaper, moveLibraryPapers, removeLibraryPapers, detachLibraryAttachment, addLibraryNote, createLibraryFolder, deleteLibraryFolder, type LibraryChanges, type PersistedPaperLibrary } from "./store";
import { emptyLibrary } from "./libraryModel";
import PaperDetailDrawer, { type PaperDetailTab } from "./PaperDetailDrawer";
import RestartKnowledgeModal, { type IRestartKnowledgeProps } from "../pages/detail/components/RestartKnowledgeModal";
import type { ProcessingLevel } from "../utils/processingLevel";
import type { ParserConfig } from "@/api/generated/knowledge-client";
import { downloadPaperCitations, downloadLibraryJson } from "./exportLibrary";
import PaperImportStatus from "./PaperImportStatus";
import { retryAcademicImport, listAcademicImportBatches, emptyImportBatchPage, type ImportBatchPage, type PaperUploadContext } from '../api/academicReferences';
import PaperSearch from "./PaperSearch";
import PaperFilterPanel from "./PaperFilterPanel";
import { DEFAULT_PAPER_FILTERS, filterPaperRecords, hasPaperFilters, isReadablePdfAttachment, isStoredPdfAttachment, type PaperFilters } from "./paperFilters";
import PaperTasks from '../components/PaperTasks';
import PaperImportanceButton from '../components/PaperTasks/PaperImportanceButton';
import { updatePaperIdentity, setPaperImportance } from '../api/paperWorkflows';
import { paperDocument } from './libraryDocuments';
import PaperDirectoryTree, { PAPER_LIBRARY_ROOT } from './PaperDirectoryTree';
import { usePaperNotifications, type PaperNotification } from "./usePaperNotifications";
import ZoteroImportModal from './ZoteroImportModal';
import PaperBackupRestore from './PaperBackupRestore';
import { executeZoteroImport, importZoteroSnapshot, storeZoteroAttachment } from './zoteroCommit';
import type { ZoteroPreview, ZoteroImportConfig, ZoteroImportOptions } from './zoteroImport';
import "./PaperLibrary.scss";
interface Props {
    datasetId: string;
    processingLevel?: ProcessingLevel;
    parsers?: ParserConfig[];
    canParse?: boolean;
    onUploadPdf?: (file: File, importContext?: PaperUploadContext) => Promise<string | void>;
    title?: string;
    onBack?: () => void;
}
async function copyText(value: string, label: string) {
    try {
        await navigator.clipboard.writeText(value);
    }
    catch {
        const area = document.createElement("textarea");
        area.value = value;
        area.setAttribute("readonly", "true");
        document.body.appendChild(area);
        area.select();
        document.execCommand("copy");
        area.remove();
    }
    message.success(`已复制${label}`);
}
export default function PaperLibrary({ datasetId, onUploadPdf, title = "论文库", onBack, processingLevel = "parsed", parsers, canParse = true }: Props) {
    const dragSensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));
    const datasetScopeRef = useRef({ datasetId, active: true });
    if (datasetScopeRef.current.datasetId !== datasetId)
        datasetScopeRef.current = { datasetId, active: true };
    const datasetScope = datasetScopeRef.current;
    const isCurrentScope = () => datasetScopeRef.current === datasetScope && datasetScope.active;
    const { notifications, notify, resolve } = usePaperNotifications(datasetId);
    const [dataState, setData] = useState<PaperLibraryData>(emptyLibrary);
    const [snapshotReady, setSnapshotReady] = useState(false);
    const [libraryActive, setLibraryActive] = useState(false);
    const refreshPendingRef = useRef(false);
    const [taskActionsContainer, setTaskActionsContainer] = useState<HTMLDivElement | null>(null);
    const data = dataState;
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [confirmingImport, setConfirmingImport] = useState(false);
    const [zoteroOpen, setZoteroOpen] = useState(false);
    const [backupOpen, setBackupOpen] = useState(false);
    const zoteroKey = useRef({ preview: null as ZoteroPreview | null, key: '' });
    const [canWrite, setCanWrite] = useState(false);
    const [persistenceError, setPersistenceError] = useState<string | null>(null);
    const loadedRef = useRef(false);
    const canWriteRef = useRef(false);
    const busyRef = useRef(false);
    const reloadPromiseRef = useRef<Promise<void> | null>(null);
    const activeDatasetRef = useRef(datasetId);
    activeDatasetRef.current = datasetId;
    const disabled = loading || saving || confirmingImport || !canWrite;
    // Keep an already-open picker usable; check access after the refresh completes.
    const fileImportDisabled = saving || confirmingImport;
    const dataRef = useRef(dataState);
    const syncGeneration = useRef(0);
    const [syncRevision, setSyncRevision] = useState(0);
    const [syncError, setSyncError] = useState<string | null>(null);
    const initialLoading = !snapshotReady && loading && !persistenceError;
    const [importPage, setImportPage] = useState<ImportBatchPage>(emptyImportBatchPage);
    const [detailPaperId, setDetailPaperId] = useState<string | null>(null);
    const [detailTab, setDetailTab] = useState<PaperDetailTab>("overview");
    const [detailEditing, setDetailEditing] = useState(false);
    const [detailPresentation, setDetailPresentation] = useState<'drawer' | 'page'>('drawer');
    const restartRef = useRef<IRestartKnowledgeProps>(null);
    const openDetail = (id: string, tab: PaperDetailTab = "overview", presentation: 'drawer' | 'page' = 'drawer') => {
        setActiveTool(null); setDetailEditing(false); setDetailTab(tab); setDetailPaperId(id); setDetailPresentation(presentation);
        if (presentation === 'page') { setFolderId(dataRef.current.papers.find(paper => paper.id === id)?.folderId || null); setLibraryView('directory'); }
    };
    const [folderId, setFolderId] = useState<string | null>(null);
    const [libraryView, setLibraryView] = useState<LibraryView>("all");
    const [filters, setFilters] = useState<PaperFilters>(DEFAULT_PAPER_FILTERS);
    const [keyword, setKeyword] = useState("");
    const [selected, setSelected] = useState<string[]>([]);
    const [draggedIds, setDraggedIds] = useState<string[]>([]);
    const [dropFolderId, setDropFolderId] = useState<string | null>(null);
    const [activeTool, setActiveTool] = useState<"search" | "filter" | "imports" | null>(null);
    const setSearchOpen = useCallback((open: boolean) => setActiveTool((current) => open ? "search" : current === "search" ? null : current), []);
    const [folderDialogOpen, setFolderDialogOpen] = useState(false);
    const [newFolderParent, setNewFolderParent] = useState<string | null>(null);
    const [editing, setEditing] = useState<PaperRecord | null>(null);
    const editingRevisionRef = useRef<number | null>(null);
    const [editError, setEditError] = useState<string | null>(null);
    const [folderName, setFolderName] = useState("");
    const [folderError, setFolderError] = useState(false);
    const applySnapshot = useCallback((next: PaperLibraryData) => {
        dataRef.current = next;
        setData(next);
    }, []);
    const applySaved = useCallback((saved: PersistedPaperLibrary) => {
        canWriteRef.current = saved.canWrite;
        setCanWrite(saved.canWrite);
        applySnapshot(saved.data);
        setSnapshotReady(true);
        setLibraryActive(saved.active);
    }, [applySnapshot]);
    const reload = useCallback(() => {
        if (datasetScopeRef.current !== datasetScope || !datasetScope.active)
            return null;
        if (busyRef.current) {
            if (!reloadPromiseRef.current)
                refreshPendingRef.current = true;
            return reloadPromiseRef.current;
        }
        refreshPendingRef.current = false;
        busyRef.current = true;
        const generation = ++syncGeneration.current;
        setLoading(true);
        const current = () => generation === syncGeneration.current && activeDatasetRef.current === datasetId && datasetScope.active;
        const request = Promise.all([
            loadPersistedPaperLibrary(datasetId).then(saved => {
                if (!current())
                    return;
                loadedRef.current = true;
                setPersistenceError(null);
                applySaved(saved);
            }).catch(error => {
                if (current()) {
                    loadedRef.current = false;
                    canWriteRef.current = false;
                    setCanWrite(false);
                    setPersistenceError(error instanceof Error ? error.message : "无法加载已保存的文献，请重试");
                }
            }),
            listAcademicImportBatches(datasetId).then(queue => {
                if (current()) {
                    setImportPage(queue);
                    setSyncError(null);
                }
            }).catch(error => {
                if (current())
                    setSyncError(error instanceof Error ? error.message : "导入任务同步失败，请重试");
            }),
        ]).then(() => {}).finally(() => {
            if (current()) {
                busyRef.current = false;
                setLoading(false);
            }
            if (reloadPromiseRef.current === request)
                reloadPromiseRef.current = null;
        });
        reloadPromiseRef.current = request;
        return request;
    }, [datasetId, datasetScope, applySaved]);
    useEffect(() => {
        datasetScope.active = true;
        syncGeneration.current += 1;
        busyRef.current = false;
        reloadPromiseRef.current = null;
        loadedRef.current = false;
        canWriteRef.current = false;
        setCanWrite(false);
        setSaving(false);
        setConfirmingImport(false);
        setZoteroOpen(false);
        setBackupOpen(false);
        zoteroKey.current = { preview: null, key: '' };
        setPersistenceError(null);
        setSyncError(null);
        setSelected([]);
        setSnapshotReady(false);
        setLibraryActive(false);
        setImportPage(emptyImportBatchPage());
        refreshPendingRef.current = false;
        setDetailPaperId(null);
        setFolderId(null);
        setLibraryView("all");
        applySnapshot(emptyLibrary());
        void reload();
        const onFocus = () => { void reload(); };
        window.addEventListener("focus", onFocus);
        return () => {
            datasetScope.active = false;
            syncGeneration.current += 1;
            window.removeEventListener("focus", onFocus);
        };
    }, [datasetId, datasetScope, reload, applySnapshot]);
    const mutate = useCallback(async (operation: () => Promise<LibraryChanges>, notice: Omit<PaperNotification, "message"> = { id: "library-write", title: "论文保存失败" }) => {
        if (datasetScopeRef.current !== datasetScope || !datasetScope.active)
            throw new Error('文献库已切换，请重新打开后重试');
        if (busyRef.current || !loadedRef.current || !canWriteRef.current)
            throw new Error('文献库尚未就绪、正在保存或没有编辑权限');
        busyRef.current = true;
        const generation = ++syncGeneration.current;
        setSaving(true);
        try {
            const changes = await operation();
            if (generation !== syncGeneration.current || activeDatasetRef.current !== datasetId)
                throw new Error('文献库已切换，请重新加载查看保存结果');
            applySnapshot(mergeLibraryChanges(dataRef.current, changes));
            resolve(notice.id);
        }
        catch (error) {
            if (generation === syncGeneration.current && activeDatasetRef.current === datasetId)
                notify({ ...notice, message: error instanceof Error ? error.message : '保存失败，请重试' });
            throw error;
        }
        finally {
            if (generation === syncGeneration.current && activeDatasetRef.current === datasetId) {
                busyRef.current = false;
                setSaving(false);
            }
        }
    }, [datasetId, datasetScope, applySnapshot, notify, resolve]);
    const restoreBackup = async (backup: PaperLibraryData, key: string) => {
        if (!isCurrentScope() || disabled || busyRef.current || !loadedRef.current || !canWriteRef.current)
            throw new Error('文献库正在保存或没有编辑权限，请稍后重试');
        busyRef.current = true; ++syncGeneration.current; setConfirmingImport(true);
        try {
            const saved = await importZoteroSnapshot(datasetId, backup, 'library-backup', key);
            if (!isCurrentScope()) throw new Error('文献库已切换，请重新加载查看恢复结果');
            applySaved(saved); message.success('文献库数据已恢复');
        } finally {
            if (isCurrentScope()) { busyRef.current = false; setConfirmingImport(false); setSyncRevision(value => value + 1); }
        }
    };
    const importZotero = async (preview: ZoteroPreview, config: ZoteroImportConfig, options: ZoteroImportOptions) => {
        if (!isCurrentScope() || disabled || busyRef.current || !loadedRef.current || !canWriteRef.current)
            throw new Error('文献库正在保存或没有编辑权限，请稍后重试');
        if (zoteroKey.current.preview !== preview) zoteroKey.current = { preview, key: uuidv4() };
        busyRef.current = true;
        ++syncGeneration.current;
        setConfirmingImport(true);
        const currentLibrary = () => {
            if (!isCurrentScope()) throw new Error('文献库已切换，请重新打开后重试');
            return dataRef.current;
        };
        try {
            const result = await executeZoteroImport({ datasetId, preview, config, options, getLibrary: currentLibrary,
                saveLibrary: async next => {
                    currentLibrary();
                    const saved = await importZoteroSnapshot(datasetId, next, preview.source_id, zoteroKey.current.key);
                    currentLibrary(); applySaved(saved);
                },
                storeAttachment: async (file, candidate) => {
                    currentLibrary();
                    try {
                        const saved = await storeZoteroAttachment(datasetId, file, candidate);
                        currentLibrary(); applySaved(saved);
                    } catch (error) {
                        const saved = (error as { library?: PersistedPaperLibrary }).library;
                        if (saved && isCurrentScope()) applySaved(saved);
                        throw error;
                    }
                },
            });
            if (isCurrentScope() && !result.failures?.length) message.success(`Zotero 导入完成：${result.imported} 篇文献，${result.attachments} 份附件`);
            return result;
        } finally {
            if (isCurrentScope()) { busyRef.current = false; setConfirmingImport(false); setSyncRevision(value => value + 1); }
        }
    };
    const imports = usePaperImports({ datasetId, dataState, dataRef, disabled, saving, confirmingImport, busyRef, loadedRef, canWriteRef, syncGeneration, reloadPromiseRef, reload, isCurrentScope, mutate, applySnapshot, setConfirmingImport, setSyncRevision, libraryView, folderId, onUploadPdf, notify, resolve });
    const { openImport, resumeAssociation } = imports;
    const updatePaper = async (id: string, changes: {
        status?: ReadingStatus;
        tags?: string[];
    }) => {
        const current = dataRef.current.papers.find(p => p.id === id);
        if (!current)
            return;
        try {
            await mutate(() => patchLibraryPaper(datasetId, current, changes), { id: `paper-status:${id}`, title: `《${current.metadata.title}》阅读状态保存失败`, action: { label: "查看论文", onClick: () => openDetail(id) } });
        }
        catch { /* Preserve the server state and visible error. */ }
    };
    useEffect(() => {
        if (syncRevision > 0)
            void reload();
    }, [syncRevision, reload]);
    useEffect(() => {
        if (!saving && !confirmingImport && refreshPendingRef.current)
            void reload();
    }, [saving, confirmingImport, reload]);
    useEffect(() => {
        const active = libraryActive || importPage.notification.active > 0
            || dataState.papers.some(paper => paper.parse === 'parsing')
            || dataState.attachments.some(attachment => ['pending', 'processing'].includes(attachment.academicState?.status || ''));
        if (!snapshotReady || loading || saving || confirmingImport || !active)
            return;
        const timer = setTimeout(() => void reload(), 12000);
        return () => clearTimeout(timer);
    }, [dataState, libraryActive, importPage, snapshotReady, loading, saving, confirmingImport, reload]);
    useEffect(() => {
        if (!snapshotReady) return;
        for (const notice of notifications.filter(item => item.id.startsWith('identity:'))) {
            const documentId = notice.id.slice('identity:'.length);
            const state = dataState.attachments.find(item => item.contentRef === documentId && item.available !== false)?.academicState;
            if (!state || state.status !== 'failed' || state.manual) resolve(notice.id);
        }
    }, [dataState, snapshotReady, notifications, resolve]);
    const scoped = useMemo(() => libraryView === 'directory' ? papersInView(data, folderId) : data.papers, [data, folderId, libraryView]);
    const viewed = useMemo(() => filterLibraryView(scoped, libraryView), [scoped, libraryView]);
    const visible = useMemo(() => filterPaperRecords(filterPapers(viewed, keyword), dataState.attachments.filter(attachment => dataState.papers.some(paper => paper.id === attachment.paperId && paper.attachmentIds.includes(attachment.id))), filters), [viewed, keyword, dataState.attachments, dataState.papers, filters]);
    const selectedPapers = data.papers.filter((item) => selected.includes(item.id));
    const selectedVisibleCount = visible.filter((item) => selected.includes(item.id)).length;
    const exportPapers = selectedPapers.length ? selectedPapers : visible;
    const hasFilters = hasPaperFilters(filters);
    const confirmedDocument = (id: string) => {
        const identity = paperDocument(dataState, id);
        const paper = dataState.papers.find(item => item.id === id);
        if (paper?.associationConflict || (paper?.academicWorkId && identity?.state.paper_id && paper.academicWorkId !== identity.state.paper_id))
            return undefined;
        return identity?.state.kind === 'paper' && identity.state.paper_id ? identity.state.document_id : undefined;
    };
    const selectedDocuments = selectedPapers.map(item => confirmedDocument(item.id)).filter((id): id is string => !!id);
    const hasStoredPdf = (id: string) => dataState.attachments.some(item => dataState.papers.find(paper => paper.id === id)?.attachmentIds.includes(item.id) && isStoredPdfAttachment(item));
    const selectionError = [
        selectedPapers.some(item => !hasStoredPdf(item.id)) ? '所选文献尚未关联入库 PDF，请先添加 PDF' : '',
        selectedPapers.some(item => hasStoredPdf(item.id) && !confirmedDocument(item.id)) ? '所选文献的论文身份尚未确认，请先识别或手动确认' : '',
    ].filter(Boolean).join('；') || undefined;
    const stats = useMemo(() => ({
        total: data.papers.length,
        important: data.papers.filter((item) => item.important).length,
    }), [data.papers]);
    const materialByPaper = useMemo(() => new Map(dataState.papers.map((paper) => {
        const attachments = dataState.attachments.filter(item => paper.attachmentIds.includes(item.id) && item.available !== false);
        return [paper.id, { pdfs: attachments.filter(isStoredPdfAttachment), links: attachments.filter(item => isReadablePdfAttachment(item) && !item.contentRef), supplements: attachments.filter(item => item.kind !== 'pdf'), attachments: attachments.length, notes: paper.noteIds.length }];
    })), [dataState]);
    const addFolder = async () => {
        const name = folderName.trim();
        if (disabled || busyRef.current || !name)
            return;
        const parent = newFolderParent, id = uuidv4();
        try {
            await mutate(() => createLibraryFolder(datasetId, id, name, parent), { id: "folder-create", title: `目录「${name}」创建失败`, action: { label: "继续创建", onClick: () => { setActiveTool(null); setFolderDialogOpen(true); } } });
            setFolderError(false);
            setFolderName('');
            setFolderDialogOpen(false);
            setLibraryView('directory');
            setFolderId(id);
            setDetailPaperId(null);
            message.success('目录已创建');
        }
        catch (error) {
            setFolderError(true);
        }
    };
    const currentFolder = data.folders.find(folder => folder.id === folderId);
    const childFolders = data.folders.filter(folder => (folder.parentId || null) === folderId);
    const directoryPaperCount = folderId ? data.papers.filter(paper => paperBelongsToFolders(paper, descendantFolderIds(data.folders, folderId))).length : data.papers.length;
    const selectView = (id: string | null, view: LibraryView) => { setFolderId(view === 'directory' ? id : null); setLibraryView(view); setSelected([]); setDetailPaperId(null); };
    const openFolderDialog = (parentId: string | null) => { setNewFolderParent(parentId); setFolderName(''); setFolderError(false); setFolderDialogOpen(true); };
    const deleteFolder = (id: string) => {
        const folder = data.folders.find(f => f.id === id);
        if (!folder || disabled)
            return;
        const descendants = descendantFolderIds(data.folders, folder.id), count = data.papers.filter(p => paperBelongsToFolders(p, descendants)).length;
        Modal.confirm({ title: `删除「${folder.name}」及其子目录？`, content: `将移除 ${descendants.size} 个目录，把 ${count} 篇论文放回上一级；原文件、附件、笔记和星标保留。`, okText: '删除目录', okButtonProps: { danger: true }, onOk: async () => { await mutate(() => deleteLibraryFolder(datasetId, folder), { id: `folder-delete:${folder.id}`, title: `目录「${folder.name}」删除失败` }); if (folderId && descendants.has(folderId)) setFolderId(folder.parentId || null); } });
    };
    const dropPapers = async (ids: string[]) => {
        if (disabled || busyRef.current)
            return;
        const papers = dataRef.current.papers.filter(p => ids.includes(p.id));
        try {
            await mutate(() => removeLibraryPapers(datasetId, papers), { id: `paper-remove:${papers.map(paper => paper.id).sort().join(",")}`, title: `${papers.length} 篇论文移除失败` });
            setSyncRevision(value => value + 1);
            setSelected(current => current.filter(id => !ids.includes(id)));
            if (editing && ids.includes(editing.id))
                setEditing(null);
        }
        catch { /* The failed operation remains in paper notifications. */ }
    };
    const placePapers = async (ids: string[], folder: string) => {
        if (disabled || busyRef.current)
            return false;
        const target = folder === 'none' ? null : folder;
        const papers = dataRef.current.papers.filter(p => ids.includes(p.id) && (p.folderId !== target || p.collectionIds?.some(id => id !== target)));
        if (!papers.length)
            return false;
        try {
            await mutate(() => moveLibraryPapers(datasetId, papers, target), { id: `paper-move:${papers.map(paper => paper.id).sort().join(",")}`, title: `${papers.length} 篇论文移动失败` });
            setSelected(current => current.filter(id => !ids.includes(id)));
            message.success(`已移动 ${papers.length} 篇文献`);
            return true;
        }
        catch (error) {
            return false;
        }
    };
    const endPaperDrag = () => {
        setDraggedIds([]);
        setDropFolderId(null);
    };
    const saveEdits = async (edits: PaperEdits) => {
        if (disabled || busyRef.current || !editing)
            return false;
        const latest = dataRef.current.papers.find(p => p.id === editing.id);
        if (!latest) {
            setEditError('论文已被移除，请重新加载');
            return false;
        }
        const next = applyPaperEdits(latest, edits);
        setEditError(null);
        try {
            await mutate(() => patchLibraryPaper(datasetId, { ...latest, revision: editingRevisionRef.current || editing.revision }, { metadata: next.metadata, tags: next.tags }), { id: `paper-edit:${latest.id}`, title: `《${latest.metadata.title}》题录保存失败`, action: { label: "查看论文", onClick: () => openDetail(latest.id) } });
            const saved = dataRef.current.papers.find(p => p.id === editing.id)!;
            editingRevisionRef.current = saved.revision;
            setEditing(saved);
            message.success('题录已保存');
            return true;
        }
        catch (error) {
            setEditError('修改尚未保存，已保留当前输入；请重试或查看通知。');
            return false;
        }
    };
    const folderOptions = [{ value: "none", label: title }, ...data.folders.map((folder) => ({ value: folder.id, label: paperFolderPath(data.folders, folder.id) }))];
    const primaryAttachment = (paperId: string) => {
        const paper = dataState.papers.find(item => item.id === paperId);
        const attachments = dataState.attachments.filter(item => paper?.attachmentIds.includes(item.id) && isStoredPdfAttachment(item));
        return attachments.find(item => item.isPrimary) || attachments[0];
    };
    const primaryDocument = (paperId: string) => primaryAttachment(paperId)?.contentRef;
    const originalUrl = (item: PaperRecord) => safePaperUrl(primaryAttachment(item.id)?.sourceUrl) || safePaperUrl(item.sourceUrl) || paperLink(item);
    const reparseDisabledReason = (item: PaperRecord) => !canParse ? '文档解析服务当前不可用'
        : !primaryDocument(item.id) ? '请先添加 PDF 附件'
            : processingLevel === 'stored' ? '请先提高知识库处理级别'
                : item.parse === 'parsing' ? '正文正在解析中' : undefined;
    const openReparse = (item: PaperRecord) => {
        const document = primaryDocument(item.id);
        if (disabled || reparseDisabledReason(item) || !document)
            return;
        restartRef.current?.onOpen({ dataset: datasetId, ids: [document], names: [item.metadata.title], title: '重新解析', processingLevel });
    };
    const renderImportance = (item: PaperRecord) => (<PaperImportanceButton important={item.important} onError={error => { if (isCurrentScope()) notify({ id: `paper-star:${item.id}`, title: `《${item.metadata.title}》重点标记保存失败`, message: error instanceof Error ? error.message : "标记保存失败，请重试" }); }} onToggle={async (important) => {
                if (!item.academicWorkId)
                    throw new Error('论文身份尚未保存');
                await setPaperImportance(datasetId, item.academicWorkId, important, true);
                if (isCurrentScope()) resolve(`paper-star:${item.id}`);
                if (isCurrentScope())
                    applySnapshot({ ...dataRef.current, papers: dataRef.current.papers.map(p => p.id === item.id ? { ...p, important } : p) });
            }}/>);
    const detailPaper = dataState.papers.find(item => item.id === detailPaperId);
    const detailContent = detailPaper && (<PaperDetailDrawer datasetId={datasetId} mode={detailPresentation} importanceControl={renderImportance(detailPaper)} key={`${detailPaperId}:${detailTab}:${detailEditing}`} initialTab={detailTab} initialEditing={detailEditing} academic={paperDocument(dataState, detailPaper.id)} editError={editError} onEdit={() => { editingRevisionRef.current = detailPaper.revision; setEditing(data.papers.find(item => item.id === detailPaperId)!); setEditError(null); }} onSave={saveEdits} onRetryIdentification={async () => {
                const document = paperDocument(dataRef.current, detailPaper.id)?.state.document_id;
                if (!document)
                    throw new Error("请先关联 PDF 附件");
                try {
                    await updatePaperIdentity(document, { reidentify: true }, true);
                    if (isCurrentScope()) resolve(`identity:${document}`);
                } catch (error) {
                    if (isCurrentScope()) notify({ id: `identity:${document}`, title: `《${detailPaper.metadata.title}》自动识别重试失败`, message: error instanceof Error ? error.message : "重试提交失败，请重试", action: { label: "查看论文", onClick: () => openDetail(detailPaper.id) } });
                    throw error;
                }
                if (isCurrentScope()) {
                    syncGeneration.current += 1;
                    setSyncRevision(value => value + 1);
                }
            }} paper={detailPaper} onReparse={() => openReparse(detailPaper)} reparseDisabledReason={reparseDisabledReason(detailPaper)} data={dataState} onNotification={notify} onNotificationResolved={resolve} onClose={() => setDetailPaperId(null)} onAddNote={async (id, content) => { await mutate(() => addLibraryNote(datasetId, detailPaper.id, id, content), { id: `paper-note:${detailPaper.id}`, title: `《${detailPaper.metadata.title}》笔记保存失败`, action: { label: "查看笔记", onClick: () => openDetail(detailPaper.id, "notes") } }); }} disabled={disabled} onStatusChange={status => updatePaper(detailPaper.id, { status })} onSetPrimaryAttachment={async attachment => {
                await mutate(() => patchLibraryPaper(datasetId, detailPaper, { primary_attachment_id: attachment.id }), { id: `paper-primary:${detailPaper.id}`, title: '主附件设置失败', action: { label: '查看附件', onClick: () => openDetail(detailPaper.id, 'attachments', detailPresentation) } });
            }} onOpenPaper={id => openDetail(id, 'relations', detailPresentation)} onAddPdf={() => openImport('upload', detailPaper.id)} onDetachAttachment={async (attachment) => { await mutate(() => detachLibraryAttachment(datasetId, detailPaper, attachment.id), { id: `paper-attachment:${attachment.id}`, title: `附件「${attachment.name}」移除关联失败`, action: { label: "查看附件", onClick: () => openDetail(detailPaper.id, "attachments") } }); }}/>);
    const libraryNotices: PaperNotification[] = [
        ...(persistenceError ? [{ id: "library-load", title: "论文库加载失败", message: persistenceError,
            action: { label: "重新加载", disabled: loading || saving, onClick: () => { void reload(); } } }] : []),
        ...dataState.attachments.filter((attachment, index, all) => attachment.available !== false
            && attachment.academicState?.status === "failed" && !attachment.academicState.manual
            && all.findIndex(item => item.contentRef === attachment.contentRef) === index).map(attachment => ({
                id: `identity:${attachment.contentRef}`,
                title: `《${dataState.papers.find(paper => paper.id === attachment.paperId)?.metadata.title || attachment.name}》自动识别失败`,
                message: attachment.academicState?.error || "PDF 已保存，请重试自动识别或手动确认论文信息。",
                action: { label: "查看论文", onClick: () => openDetail(attachment.paperId) },
            })),
        ...notifications,
    ];
    return (<DndContext sensors={dragSensors} collisionDetection={pointerWithin} accessibility={{
            screenReaderInstructions: { draggable: "拖到左侧文件夹以移动文献，也可按回车选择目标文件夹。" },
            announcements: {
                onDragStart: () => "已开始拖动文献，请移到左侧文件夹。",
                onDragOver: ({ over }: DragOverEvent) => over ? `目标文件夹：${data.folders.find((folder) => folder.id === over.id)?.name || ""}` : "请移到左侧文件夹。",
                onDragEnd: () => "拖动结束。",
                onDragCancel: () => "已取消拖动。",
            },
        }} onDragStart={({ active }: DragStartEvent) => {
            if (disabled || busyRef.current)
                return;
            const item = data.papers.find((paper) => paper.id === (active.data.current?.paperId || active.id));
            if (!item)
                return;
            const ids = selected.includes(item.id) ? selectedPapers.map((paper) => paper.id) : [item.id];
            setDraggedIds(ids);
        }} onDragEnd={({ over }: DragEndEvent) => {
            if (over && (over.id === PAPER_LIBRARY_ROOT || data.folders.some(folder => folder.id === over.id)))
                void placePapers(draggedIds, over.id === PAPER_LIBRARY_ROOT ? 'none' : String(over.id));
            endPaperDrag();
        }} onDragCancel={endPaperDrag}>
    <section className="paper-library">
      <header className="paper-library-head">
        <div className="paper-library-heading">
          {onBack && <Tooltip title="返回知识库"><Button className="paper-library-back" type="text" icon={<ArrowLeftOutlined />} aria-label="返回知识库" onClick={onBack}/></Tooltip>}
          <h1 title={title}>{title}</h1>
          {loading && !initialLoading && <span className="paper-library-refreshing" role="status">正在刷新…</span>}
        </div>
        <div className="paper-library-tools" role="toolbar" aria-label="文献工具">
          <PaperSearch open={activeTool === "search"} value={keyword} onChange={setKeyword} onOpenChange={setSearchOpen}/>
          <Popover trigger="click" placement="bottomRight" align={{ overflow: { adjustX: true, adjustY: true, shiftX: true, shiftY: true } }} arrow={false} destroyOnHidden overlayClassName="paper-library-tool-popover" open={activeTool === "filter"} onOpenChange={(open: boolean) => setActiveTool((current) => open ? "filter" : current === "filter" ? null : current)} content={<div role="dialog" aria-label="筛选文献" onKeyDown={(event) => {
                if (event.key === "Escape")
                    setActiveTool(null);
            }}>
              <PaperFilterPanel papers={data.papers} value={filters} onChange={setFilters}/>
            </div>}>
            <Button type="text" className={`paper-library-tool ${hasFilters ? "is-active" : ""}`} icon={<FilterOutlined />} aria-label="筛选" aria-expanded={activeTool === "filter"}>筛选{hasFilters && <span className="paper-library-tool-dot" aria-hidden="true"/>}</Button>
          </Popover>
          <Dropdown trigger={["click"]} placement="bottomRight" onOpenChange={(open: boolean) => {
            if (open)
                setActiveTool(null);
        }} menu={{ items: [{
                    type: "group", label: `${selectedPapers.length ? "已选文献" : "当前结果"} · ${exportPapers.length} 篇`,
                    children: [...(["bib", "ris", "csv"] as const).map((exportFormat) => ({ key: exportFormat, label: exportFormat === "bib" ? "BibTeX（.bib）· LaTeX 写作" : exportFormat === "ris" ? "RIS（.ris）· 文献管理" : "CSV（.csv）· 表格整理",
                        onClick: () => downloadPaperCitations({ papers: exportPapers, folders: data.folders }, exportFormat, title),
                    })), { key: 'csl', label: 'CSL JSON（.json）', onClick: () => downloadLibraryJson({ ...data, papers: exportPapers }, title) }],
                }, { type: 'divider' }, { key: 'backup', label: '完整数据备份（JSON，不含 PDF 文件）', onClick: () => downloadLibraryJson(data, title, true, datasetId) }] }}>
            <Button type="text" className="paper-library-tool" icon={<ExportOutlined />} aria-label="导出题录" title="导出题录或包含笔记、批注与附件引用的数据备份" disabled={!snapshotReady}>导出题录</Button>
          </Dropdown>
          <PaperImportStatus key={datasetId} datasetId={datasetId} page={importPage} notifications={libraryNotices} syncError={syncError} refreshing={loading} onRefresh={() => { void reload(); }} canWrite={canWrite} onOpenPaper={id => openDetail(id)} onResumeAssociation={resumeAssociation} open={activeTool === "imports"} onOpenChange={open => setActiveTool(current => open ? "imports" : current === "imports" ? null : current)} onRetry={async (batch) => { await retryAcademicImport(batch, true); setSyncRevision(value => value + 1); }}/>
        </div>
      </header>
      {!loading && !canWrite && !persistenceError && <Alert type="info" showIcon message="当前文献库为只读，无法编辑"/>}
      <div className="paper-library-layout">
        <nav className="paper-library-rail" aria-label="文献浏览">
          <Dropdown trigger={["click"]} placement="bottomLeft" menu={{ items: [
                { key: "upload", icon: <UploadOutlined />, label: "直接上传", onClick: () => openImport("upload") },
                { key: "identifier", icon: <LinkOutlined />, label: "arXiv / DOI 链接", onClick: () => openImport("identifier") },
                { key: "bibliography", icon: <FileTextOutlined />, label: "BibTeX / RIS / CSV", onClick: () => openImport("bibliography") },
                { type: 'divider' },
                { key: 'zotero', icon: <FolderOutlined />, label: '从 Zotero 导入', onClick: () => setZoteroOpen(true) },
                { key: 'restore', icon: <UploadOutlined />, label: '恢复文献库备份', onClick: () => setBackupOpen(true) },
            ] }}>
            <Button className="paper-library-import-button" disabled={disabled} type="primary" icon={<PlusOutlined />}>导入<DownOutlined /></Button>
          </Dropdown>
          <div className="paper-library-folders">
          <div className="paper-library-browse-head"><span className="paper-library-rail-label">浏览视图</span></div>
          <FolderButton icon={<FileTextOutlined />} name="全部" count={data.papers.length} current={libraryView === "all"} onClick={() => selectView(null, 'all')}/>
          <FolderButton icon={<StarOutlined />} name="重点论文" count={stats.important} current={libraryView === "important"} onClick={() => selectView(null, "important")}/>
          <div className="paper-library-directory-head paper-library-browse-head">
            <span className="paper-library-rail-label">目录</span>
          </div>
          <PaperDirectoryTree key={datasetId} data={data} title={title} active={libraryView === 'directory'} folderId={folderId} detailPaperId={detailPresentation === 'page' ? detailPaperId : null} disabled={disabled} draggedIds={draggedIds} dropFolderId={dropFolderId} onSelectFolder={id => selectView(id, 'directory')} onOpenPaper={id => openDetail(id, 'overview', 'page')} onCreateFolder={openFolderDialog} onDeleteFolder={deleteFolder}/>
          {!!draggedIds.length && <p className="paper-library-drag-hint" role="status">移动 {draggedIds.length} 篇文献<br />拖到目标文件夹</p>}
          </div>
          <div ref={setTaskActionsContainer} className="paper-library-task-actions-slot"/>
        </nav>
        <div className="paper-library-board">
          {detailPresentation === 'page' && detailContent}
          <div hidden={detailPresentation === 'page' && !!detailPaper}>
          <div className="paper-library-view-heading">
            <div><span className="paper-library-view-path">{libraryView === 'directory' ? [title, paperFolderPath(data.folders, folderId)].filter(Boolean).join(' / ') : '浏览视图'}</span><h2>{libraryView === 'all' ? '全部论文' : libraryView === 'important' ? '重点论文' : currentFolder?.name || title}</h2></div>
            <span>{libraryView === 'directory' ? `本目录及子目录 ${directoryPaperCount} 篇` : `${viewed.length} 篇论文`}</span>
          </div>
          {childFolders.length > 0 && <div className="paper-library-child-folders" aria-label="子目录">{childFolders.map(folder => {
                const descendants = descendantFolderIds(data.folders, folder.id);
                const count = data.papers.filter(paper => paperBelongsToFolders(paper, descendants)).length;
                return <Button type="text" key={folder.id} icon={<FolderOutlined />} onClick={() => selectView(folder.id, 'directory')}><span>{folder.name}</span><span className="paper-library-child-count">{count} 篇</span></Button>;
            })}</div>}
          <PaperTasks key={datasetId} dataset={datasetId} actionsContainer={taskActionsContainer} selected={selectedDocuments} selectedTitles={selectedPapers.map(item => item.metadata.title)} selectionError={selectionError} canImport={canWrite} onImportSubmitted={() => setSyncRevision(value => value + 1)} onNotification={notify} onNotificationResolved={resolve}/>
          {selectedPapers.length > 0 && (<div className="paper-library-selection-bar" role="region" aria-label="已选论文">
          <div className="paper-library-selection-summary"><Button type="text" size="small" aria-label="取消选择" icon={<CloseOutlined />} onClick={() => setSelected([])}/><span>已选 <strong>{selectedPapers.length}</strong> 篇</span></div>
          <Dropdown trigger={["click"]} menu={{ items: folderOptions.map((folder) => ({ key: folder.value, label: folder.label, onClick: () => placePapers(selected, folder.value) })) }}><Button type="text" size="small" disabled={disabled} icon={<FolderOutlined />}>移动到<DownOutlined /></Button></Dropdown>
          <Button type="text" size="small" disabled={disabled} danger icon={<DeleteOutlined />} onClick={() => Modal.confirm({ title: `移除已选的 ${selectedPapers.length} 篇题录？`, okText: "移除", cancelText: "取消", onOk: () => dropPapers(selected) })}>移除</Button>
          </div>)}
          <div className="paper-library-table-scroll" tabIndex={0} aria-label="论文列表，可横向滚动" aria-busy={initialLoading || loading}>
          <table className="paper-library-table" aria-label="论文列表">
            <colgroup><col className="paper-library-controls-col"/><col /><col className="paper-library-year-col"/><col className="paper-library-venue-col"/><col className="paper-library-actions-col"/></colgroup>
            <thead>
              <tr>
                <th scope="col" className={`paper-library-selection-head ${selectedVisibleCount ? "has-selection" : ""}`}>
                  <input type="checkbox" className="paper-library-checkbox" aria-label="选择当前列表全部论文" disabled={!visible.length} checked={visible.length > 0 && selectedVisibleCount === visible.length} ref={(element) => {
            if (element)
                element.indeterminate = selectedVisibleCount > 0 && selectedVisibleCount < visible.length;
        }} onChange={(event) => setSelected(event.target.checked ? [...new Set([...selected, ...visible.map((item) => item.id)])] : selected.filter((id) => !visible.some((item) => item.id === id)))}/>
                </th>
                <th scope="col">标题</th><th scope="col">年份</th><th scope="col">会议／期刊</th><th scope="col"><span className="paper-library-visually-hidden">文献操作</span></th>
              </tr>
            </thead>
            <tbody>
          {initialLoading && <tr><td colSpan={5}><div className="paper-library-loading" role="status"><Spin size="small"/>正在加载文献…</div></td></tr>}
          {!initialLoading && !visible.length && <tr><td colSpan={5}><div className="paper-library-empty">{persistenceError && !snapshotReady ? "论文库尚未加载成功，请打开右上角的通知重新加载。" : "没有符合条件的论文。可以导入 DOI、arXiv、BibTeX、RIS、CSV，或上传 PDF。"}</div></td></tr>}
          {!initialLoading && visible.map((item) => (<tr className={`paper-library-row ${selected.includes(item.id) ? "selected" : ""} ${draggedIds.includes(item.id) ? "is-dragging" : ""}`} key={item.id}>
              <td className="paper-library-controls-cell"><div className="paper-library-row-controls">
              <input type="checkbox" className="paper-library-checkbox" aria-label={`选择 ${item.metadata.title}`} checked={selected.includes(item.id)} onChange={(event: ChangeEvent<HTMLInputElement>) => setSelected(event.target.checked ? [...selected, item.id] : selected.filter((id) => id !== item.id))}/>
              <PaperDragHandle disabled={disabled} item={item} folders={folderOptions} onMove={(folder) => placePapers(selected.includes(item.id) ? selected : [item.id], folder)}/>
              </div></td>
              <td className="paper-library-main">
                  <div className="paper-library-title-line"><h2 className="paper-library-title"><button type="button" className="paper-library-title-button" onClick={() => openDetail(item.id)}>{item.metadata.title}</button></h2>
                    {renderImportance(item)}

                  </div>
                <p className="paper-library-cite">{item.metadata.authors.length ? item.metadata.authors.join("、") : "作者待补全"}</p>
                {(() => {
                const identity = paperDocument(dataState, item.id);
                const status = identity?.state.status;
                return status && status !== 'succeeded' ? <button type="button" className="paper-library-state" onClick={() => openDetail(item.id)}>{status === 'failed' ? '论文信息识别失败' : '论文信息识别中'}</button> : null;
            })()}
                <div className="paper-library-meta">
                  {item.tags.filter((tag) => tag !== "paper" && !tag.startsWith("arxiv:")).map((tag) => (<span key={tag} className="paper-library-tag">{tag}</span>))}
                  {item.incomplete && snapshotReady && <Tooltip title="尚未提取到正式论文题名，当前使用文件名。点击补全题名"><button type="button" className="paper-library-state" disabled={disabled} onClick={() => {
                    openDetail(item.id);
                    setDetailEditing(true);
                    editingRevisionRef.current = item.revision;
                    setEditing(item);
                    setEditError(null);
                }}>待补全论文题名</button></Tooltip>}
                  {item.parse === "failed" && <Tooltip title={item.processingError || "正文解析失败，请检查导入任务"}><span className="paper-library-warn">正文解析失败</span></Tooltip>}
                  {item.parse === "parsing" && <span className="paper-library-source">正文解析中</span>}
                  {item.associationConflict && <button type="button" className="paper-library-warn" onClick={() => openDetail(item.id, 'attachments')}>附件归属待核对</button>}
                  <button type="button" className="paper-library-material" onClick={() => openDetail(item.id, "attachments")}>{materialByPaper.get(item.id)?.pdfs.length ? `PDF · ${materialByPaper.get(item.id)!.pdfs.length} 份` : materialByPaper.get(item.id)?.links.length ? '原文链接' : materialByPaper.get(item.id)?.supplements.length ? '无 PDF' : '仅题录'}</button>
                  {!!materialByPaper.get(item.id)?.supplements.length && <button type="button" className="paper-library-material" onClick={() => openDetail(item.id, 'attachments')}>补充材料 · {materialByPaper.get(item.id)!.supplements.length} 份</button>}
                  {!!materialByPaper.get(item.id)?.notes && <button type="button" className="paper-library-material" onClick={() => openDetail(item.id, "notes")}>{materialByPaper.get(item.id)?.notes} 条笔记</button>}
                </div>
              </td>
              <td className="paper-library-year">{item.metadata.year || "—"}</td>
              <td className="paper-library-venue"><span title={venueName(item)}>{item.metadata.venue?.trim() ? venueName(item) : "—"}</span></td>
              <td><div className="paper-library-row-actions">
                    <Select className="paper-library-status" size="small" value={item.status} disabled={disabled} aria-label={`${item.metadata.title} 的阅读状态`} options={(Object.keys(statusLabel) as ReadingStatus[]).map((key) => ({ value: key, label: statusLabel[key] }))} onChange={(value: ReadingStatus) => void updatePaper(item.id, { status: value })}/>
                  <Dropdown menu={{ items: [
                    { key: "detail", label: "查看详情", onClick: () => openDetail(item.id) },
                    { key: "citation", label: "复制引用", children: (Object.keys(citationLabel) as CitationStyle[]).map(style => ({
                            key: style, label: citationLabel[style], onClick: () => copyText(formatCitation(item, style), `${citationLabel[style]}引用`),
                        })) },
                    { key: "link", label: "打开原文", disabled: !originalUrl(item), onClick: () => {
                            const href = originalUrl(item);
                            if (href)
                                window.open(href, "_blank", "noopener");
                        } },
                    { key: "reparse", label: "重新解析", disabled: disabled || !!reparseDisabledReason(item),
                        title: reparseDisabledReason(item), onClick: () => openReparse(item) },
                    { type: "divider" },
                    { key: "move", label: "移动到", disabled, children: folderOptions.map((option) => ({ key: option.value, label: option.label, onClick: () => placePapers([item.id], option.value) })) },
                    { key: "remove", label: "从论文集移除", disabled, danger: true, onClick: () => Modal.confirm({ title: "从论文集移除这篇题录？", okText: "移除", cancelText: "取消", onOk: () => dropPapers([item.id]) }) },
                ] }}>
                    <button type="button" className="paper-library-more" aria-label={`${item.metadata.title} 的更多操作`}><EllipsisOutlined /></button>
                  </Dropdown>
              </div></td>
            </tr>))}
            </tbody>
          </table>
          </div>
          </div>
        </div>
      </div>
      <Modal title="新建文件夹" open={folderDialogOpen} onCancel={() => {
            if (!saving)
                setFolderDialogOpen(false);
        }} onOk={addFolder} okText="创建" cancelText="取消" confirmLoading={saving} okButtonProps={{ disabled: disabled || !folderName.trim() }} width={400} centered destroyOnHidden>
        {folderError && <p role="status">目录尚未创建，请重试或查看通知。</p>}
        <label className="paper-library-folder-form">文件夹名称
          <Input autoFocus maxLength={80} value={folderName} placeholder="例如：长上下文研究" onChange={(event: ChangeEvent<HTMLInputElement>) => setFolderName(event.target.value)} onPressEnter={addFolder}/>
        </label>
      </Modal>
      <PaperBackupRestore open={backupOpen} datasetId={datasetId} disabled={disabled} onCancel={() => setBackupOpen(false)} onRestore={restoreBackup}/>
      <ZoteroImportModal open={zoteroOpen} datasetId={datasetId} canWrite={canWrite} initialFolderId={libraryView === 'directory' ? folderId : null} onCancel={() => setZoteroOpen(false)} onImport={importZotero}/>
      <PaperImportDialog imports={imports} data={data} disabled={disabled} loading={loading} saving={saving} confirmingImport={confirmingImport} fileImportDisabled={fileImportDisabled}/>
      {detailPresentation === 'drawer' && detailContent}
      <RestartKnowledgeModal ref={restartRef} parsers={parsers} onError={(error, ids) => {
            if (!isCurrentScope()) return;
            for (const id of ids) notify({ id: `reparse:${id}`, title: '正文解析任务提交失败', message: error instanceof Error ? error.message : '解析任务提交失败，请重试' });
        }} onFinish={(ids = []) => { if (isCurrentScope()) { ids.forEach(id => resolve(`reparse:${id}`)); setSyncRevision(value => value + 1); } }}/>
    </section>
    <DragOverlay dropAnimation={null}>{draggedIds.length > 0 && <div className="paper-library-drag-preview"><HolderOutlined /><span><strong>移动 {draggedIds.length} 篇文献</strong><small>{data.papers.find(p => p.id === draggedIds[0])?.metadata.title}</small></span></div>}</DragOverlay>
    </DndContext>);
}
function PaperDragHandle({ item, folders, onMove, disabled }: {
    item: PaperRecord;
    folders: Array<{
        value: string;
        label: string;
    }>;
    onMove: (folder: string) => void;
    disabled: boolean;
}) {
    const { attributes, listeners, setNodeRef } = useDraggable({ id: item.id, disabled });
    return <Dropdown trigger={['click']} menu={{ items: folders.map(f => ({ key: f.value, label: f.label, onClick: () => onMove(f.value) })) }}><button {...attributes} {...listeners} ref={setNodeRef} type="button" className="paper-library-drag-handle" disabled={disabled} aria-label={`拖动 ${item.metadata.title}`} title="拖到左侧文件夹；点击选择目标文件夹"><HolderOutlined /></button></Dropdown>;
}
function FolderButton({ name, icon, count, current, onClick }: {
    name: string;
    icon: ReactNode;
    count: number;
    current: boolean;
    onClick: () => void;
}) {
    return <div className={`paper-library-folder ${current ? 'current' : ''}`}><button type="button" aria-current={current ? 'true' : undefined} onClick={onClick}><span className="paper-library-folder-name">{icon}<span title={name}>{name}</span></span><span className="paper-library-folder-count">{count}</span></button></div>;
}
