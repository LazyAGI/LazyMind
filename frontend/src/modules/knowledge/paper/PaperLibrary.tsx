import ReadingPreferencesModal from "./ReadingPreferencesModal";
import { readLibraryViewCache, writeLibraryViewCache } from "./libraryViewCache";
import { usePaperImports, PaperImportDialog } from './usePaperImports';
import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode, type ChangeEvent } from "react";
import { Alert, Button, Checkbox, Dropdown, Input, Modal, Popover, Pagination, Select, Spin, Tooltip, message } from "antd";
import { DndContext, DragOverlay, PointerSensor, pointerWithin, useDraggable, useSensor, useSensors, type DragEndEvent, type DragStartEvent, type DragOverEvent } from "@dnd-kit/core";
import { ArrowLeftOutlined, DownOutlined, FileTextOutlined, FolderOutlined, LinkOutlined, PlusOutlined, DeleteOutlined, StarOutlined, CloseOutlined, UploadOutlined, FilterOutlined, ExportOutlined, SettingOutlined, HolderOutlined, EllipsisOutlined } from "@ant-design/icons";
import { v4 as uuidv4 } from "uuid";
import { applyPaperEdits, citationLabel, descendantFolderIds, papersInView, paperBelongsToFolders, paperFolderPath, filterPapers, filterLibraryView, formatCitation, getPaperTags, paperLink, paperTagOrder, venueName, safePaperUrl, statusLabel, type PaperEdits, type CitationStyle, type LibraryView, type PaperRecord, type PaperLibraryData, type ReadingStatus } from "./model";
import { loadPersistedPaperLibrary, mergeLibraryChanges, patchLibraryPaper, moveLibraryPapers, deleteLibraryPapers, detachLibraryAttachment, addLibraryNote, createLibraryFolder, deleteLibraryFolder, type LibraryChanges, type PersistedPaperLibrary } from "./store";
import { emptyLibrary } from "./libraryModel";
import PaperDetailDrawer, { type PaperDetailTab } from "./PaperDetailDrawer";
import PaperAuthors from './PaperAuthors';
import RestartKnowledgeModal, { type IRestartKnowledgeProps } from "../pages/detail/components/RestartKnowledgeModal";
import type { ProcessingLevel } from "../utils/processingLevel";
import type { ParserConfig } from "@/api/generated/knowledge-client";
import { downloadPaperCitations, downloadLibraryJson } from "./exportLibrary";
import PaperImportStatus from "./PaperImportStatus";
import { retryAcademicImport, listAcademicImportBatches, emptyImportBatchPage, type ImportBatchPage, type PaperUploadContext } from '../api/academicReferences';
import PaperBreadcrumb from "./PaperBreadcrumb";
import PaperSearch from "./PaperSearch";
import PaperFilterPanel from "./PaperFilterPanel";
import { DEFAULT_PAPER_FILTERS, filterPaperRecords, hasPaperFilters, PAPER_TAG_MATCH_LABELS, type PaperFilters } from "./paperFilters";
import PaperTags, { type PaperTagChange } from "./PaperTags";
import PaperTasks from '../components/PaperTasks';
import PaperImportanceButton from '../components/PaperTasks/PaperImportanceButton';
import { updatePaperIdentity, setPaperImportance } from '../api/paperWorkflows';
import { indexLibrary } from "./libraryIndex";
import { paperDocument } from './libraryDocuments';
import PaperDirectoryTree, { PAPER_LIBRARY_ROOT } from './PaperDirectoryTree';
import { usePaperNotifications, type PaperNotification } from "./usePaperNotifications";
import type { CheckboxChangeEvent } from "antd/es/checkbox";
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT, AUTH_LOGOUT_EVENT } from "@/components/auth";
import ZoteroImportModal from './ZoteroImportModal';
import PaperBackupRestore from './PaperBackupRestore';
import { executeZoteroImport, importZoteroSnapshot, storeZoteroAttachment } from './zoteroCommit';
import { listZoteroPapers } from './zoteroSelection';
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
    onOpenAttachment?: (documentId: string) => void;
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
export default function PaperLibrary(props: Props) {
    const [userId, setUserId] = useState(() => AgentAppsAuth.getUserInfo()?.userId || '');
    useEffect(() => {
        const syncUser = () => setUserId(AgentAppsAuth.getUserInfo()?.userId || '');
        window.addEventListener(AUTH_USER_CHANGE_EVENT, syncUser);
        window.addEventListener(AUTH_LOGOUT_EVENT, syncUser);
        return () => {
            window.removeEventListener(AUTH_USER_CHANGE_EVENT, syncUser);
            window.removeEventListener(AUTH_LOGOUT_EVENT, syncUser);
        };
    }, []);
    return <PaperLibraryContent key={userId} {...props} userId={userId}/>;
}
function PaperLibraryContent({ userId, datasetId, onUploadPdf, title = "论文库", onBack, onOpenAttachment, processingLevel = "parsed", parsers, canParse = true }: Props & { userId: string }) {
    const sessionToken = AgentAppsAuth.getAccessToken();
    const cachedView = useMemo(() => readLibraryViewCache(sessionToken, datasetId), [sessionToken, datasetId]);
    const lastRefresh = useRef(0);
    const dragSensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));
    const datasetScopeRef = useRef({ datasetId, active: true });
    if (datasetScopeRef.current.datasetId !== datasetId)
        datasetScopeRef.current = { datasetId, active: true };
    const datasetScope = datasetScopeRef.current;
    const [readingPreferencesOpen, setReadingPreferencesOpen] = useState(false);
    const isCurrentScope = () => datasetScopeRef.current === datasetScope && datasetScope.active && (AgentAppsAuth.getUserInfo()?.userId || '') === userId;
    const { notifications, notify, resolve } = usePaperNotifications(datasetId);
    const [dataState, setData] = useState<PaperLibraryData>(() => cachedView?.data || emptyLibrary());
    const [snapshotReady, setSnapshotReady] = useState(!!cachedView);
    const [libraryActive, setLibraryActive] = useState(cachedView?.active || false);
    const refreshPendingRef = useRef(false);
    const [taskActionsContainer, setTaskActionsContainer] = useState<HTMLDivElement | null>(null);
    const data = dataState;
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [confirmingImport, setConfirmingImport] = useState(false);
    const [zoteroOpen, setZoteroOpen] = useState(false);
    const [backupOpen, setBackupOpen] = useState(false);
    const zoteroKey = useRef({ signature: '', key: '' });
    const [canWrite, setCanWrite] = useState(cachedView?.canWrite || false);
    const [persistenceError, setPersistenceError] = useState<string | null>(null);
    const loadedRef = useRef(false);
    const canWriteRef = useRef(false);
    const busyRef = useRef(false);
    const reloadPromiseRef = useRef<Promise<void> | null>(null);
    const activeDatasetRef = useRef(datasetId);
    activeDatasetRef.current = datasetId;
    const personalDisabled = saving || confirmingImport || !snapshotReady || !!persistenceError;
    const disabled = personalDisabled || !canWrite;
    const displayKey = `paper-library-display:${userId}`;
    const [display, setDisplay] = useState(() => {
        try {
            const saved = JSON.parse(localStorage.getItem(displayKey) || '{}');
            return { status: saved?.status !== false, importance: saved?.importance !== false };
        } catch { return { status: true, importance: true }; }
    });
    const setDisplayOption = (key: 'status' | 'importance', value: boolean) => {
        const next = { ...display, [key]: value };
        setDisplay(next);
        try { localStorage.setItem(displayKey, JSON.stringify(next)); }
        catch { message.warning('显示设置已生效，但浏览器无法保存此偏好'); }
    };
    // Keep an already-open picker usable; check access after the refresh completes.
    const fileImportDisabled = saving || confirmingImport;
    const dataRef = useRef(dataState);
    const syncGeneration = useRef(0);
    const [syncRevision, setSyncRevision] = useState(0);
    const [syncError, setSyncError] = useState<string | null>(null);
    const initialLoading = !snapshotReady && loading && !persistenceError;
    const [importPage, setImportPage] = useState<ImportBatchPage>(emptyImportBatchPage);
    const [currentImportPage, setCurrentImportPage] = useState<ImportBatchPage>(emptyImportBatchPage);
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
    const [activeTool, setActiveTool] = useState<"search" | "filter" | "imports" | "display" | null>(null);
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
        const cached = readLibraryViewCache(sessionToken, datasetId);
        if (cached) writeLibraryViewCache(sessionToken, datasetId, { ...cached, data: next });
    }, [sessionToken, datasetId]);
    const applySaved = useCallback((saved: PersistedPaperLibrary) => {
        writeLibraryViewCache(sessionToken, datasetId, saved);
        canWriteRef.current = saved.canWrite;
        setCanWrite(saved.canWrite);
        applySnapshot(saved.data);
        setSnapshotReady(true);
        setLibraryActive(saved.active);
    }, [applySnapshot, sessionToken, datasetId]);
    const reload = useCallback(() => {
        if (datasetScopeRef.current !== datasetScope || !datasetScope.active)
            return null;
        if (busyRef.current) {
            if (!reloadPromiseRef.current)
                refreshPendingRef.current = true;
            return reloadPromiseRef.current;
        }
        if (reloadPromiseRef.current) return reloadPromiseRef.current;
        refreshPendingRef.current = false;
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
                    writeLibraryViewCache(sessionToken, datasetId, null);
                    loadedRef.current = false;
                    canWriteRef.current = false;
                    setCanWrite(false);
                    setPersistenceError(error instanceof Error ? error.message : "无法加载已保存的文献，请重试");
                }
            }),
            Promise.all([listAcademicImportBatches(datasetId), listAcademicImportBatches(datasetId, undefined, 'current')]).then(([queue, currentQueue]) => {
                if (current()) {
                    setImportPage(queue);
                    setCurrentImportPage(currentQueue);
                    setSyncError(null);
                }
            }).catch(error => {
                if (current())
                    setSyncError(error instanceof Error ? error.message : "导入任务同步失败，请重试");
            }),
        ]).then(() => {}).finally(() => {
            if (current()) {
                lastRefresh.current = Date.now();
            }
            if (datasetScope.active && activeDatasetRef.current === datasetId) {
                setLoading(false);
                if (!current()) setSyncRevision(value => value + 1);
            }
            if (reloadPromiseRef.current === request)
                reloadPromiseRef.current = null;
        });
        reloadPromiseRef.current = request;
        return request;
    }, [datasetId, datasetScope, applySaved, sessionToken]);
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
        zoteroKey.current = { signature: '', key: '' };
        setPersistenceError(null);
        setSyncError(null);
        setSelected([]);
        setSnapshotReady(false);
        setLibraryActive(false);
        setImportPage(emptyImportBatchPage());
        setCurrentImportPage(emptyImportBatchPage());
        refreshPendingRef.current = false;
        setDetailPaperId(null);
        setFolderId(null);
        setLibraryView("all");
        const cached = readLibraryViewCache(sessionToken, datasetId);
        if (cached) { applySaved(cached); loadedRef.current = true; }
        else applySnapshot(emptyLibrary());
        void reload();
        const onFocus = () => { if (Date.now() - lastRefresh.current > 15_000) void reload(); };
        const onVisibility = () => { if (document.visibilityState === "visible") onFocus(); };
        window.addEventListener("focus", onFocus);
        document.addEventListener("visibilitychange", onVisibility);
        return () => {
            datasetScope.active = false;
            syncGeneration.current += 1;
            window.removeEventListener("focus", onFocus);
            document.removeEventListener("visibilitychange", onVisibility);
        };
    }, [datasetId, datasetScope, reload, applySnapshot, applySaved, sessionToken]);
    const mutate = useCallback(async (operation: () => Promise<LibraryChanges>, notice: Omit<PaperNotification, "message"> = { id: "library-write", title: "论文保存失败" }, personal = false) => {
        if (datasetScopeRef.current !== datasetScope || !datasetScope.active || (AgentAppsAuth.getUserInfo()?.userId || '') !== userId)
            throw new Error('文献库已切换，请重新打开后重试');
        if (busyRef.current || !loadedRef.current || (!personal && !canWriteRef.current))
            throw new Error('文献库尚未就绪、正在保存或没有编辑权限');
        busyRef.current = true;
        const generation = ++syncGeneration.current;
        setSaving(true);
        try {
            const changes = await operation();
            if (generation !== syncGeneration.current || activeDatasetRef.current !== datasetId || !datasetScope.active || (AgentAppsAuth.getUserInfo()?.userId || '') !== userId)
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
    }, [datasetId, datasetScope, userId, applySnapshot, notify, resolve]);
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
        const signature = JSON.stringify([preview.source_id, listZoteroPapers(preview).map(paper => paper.key).sort()]);
        if (zoteroKey.current.signature !== signature) zoteroKey.current = { signature, key: uuidv4() };
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
                    const saved = await importZoteroSnapshot(datasetId, next, preview.source_id, zoteroKey.current.key, listZoteroPapers(preview).map(paper => paper.key));
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
            if (isCurrentScope()) {
                if (result.failures?.length) notify({ id: 'zotero-import', title: 'Zotero 附件导入未完成', message: result.failures.join('\n'), action: { label: '继续导入', onClick: () => setZoteroOpen(true) } });
                else { resolve('zotero-import'); message.success(`Zotero 已入库：${result.imported} 篇文献，${result.attachments} 份附件；解析在后台进行`); }
            }
            return result;
        } catch (error) {
            if (isCurrentScope()) notify({ id: 'zotero-import', title: 'Zotero 导入未完成', message: error instanceof Error ? error.message : '导入失败，请重试', action: { label: '继续导入', onClick: () => setZoteroOpen(true) } });
            throw error;
        } finally {
            if (isCurrentScope()) { busyRef.current = false; setConfirmingImport(false); setSyncRevision(value => value + 1); }
        }
    };
    const imports = usePaperImports({ datasetId, dataState, dataRef, disabled, saving, confirmingImport, busyRef, loadedRef, canWriteRef, syncGeneration, reloadPromiseRef, reload, isCurrentScope, mutate, applySnapshot, setConfirmingImport, setSyncRevision, libraryView, folderId, onUploadPdf, notify, resolve });
    const { openImport, resumeAssociation } = imports;
    const updatePaper = async (id: string, changes: { status: ReadingStatus }) => {
        const current = dataRef.current.papers.find(p => p.id === id);
        if (!current)
            return;
        try {
            await mutate(() => patchLibraryPaper(datasetId, current, changes), { id: `paper-status:${id}`, title: `《${current.metadata.title}》阅读状态保存失败`, action: { label: "查看论文", onClick: () => openDetail(id) } }, true);
        }
        catch { /* Preserve the server state and visible error. */ }
    };
    const updatePaperTags = (paper: PaperRecord, change: PaperTagChange) => mutate(() => {
        const current = dataRef.current.papers.find(item => item.id === paper.id);
        if (!current) throw new Error('此论文不存在，请刷新列表');
        const tags = 'add' in change
            ? [...current.tags, ...change.add.filter(tag => !current.tags.includes(tag))]
            : current.tags.filter(tag => tag !== change.remove);
        return patchLibraryPaper(datasetId, current, { tags });
    }, { id: `paper-tags:${paper.id}`, title: `《${paper.metadata.title}》标签保存失败` });
    const updateReadingStatuses = async (papers: PaperRecord[], status: ReadingStatus) => {
        const failed: { paper: PaperRecord; message: string }[] = [];
        const succeeded = new Set<string>();
        try {
            await mutate(async () => {
                for (const paper of papers) {
                    if (!isCurrentScope()) break;
                    try {
                        const changes = await patchLibraryPaper(datasetId, paper, { status });
                        if (!isCurrentScope()) break;
                        applySnapshot(mergeLibraryChanges(dataRef.current, changes));
                        succeeded.add(paper.id);
                    } catch (error) {
                        failed.push({ paper, message: error instanceof Error ? error.message : '保存失败，请重试' });
                    }
                }
                return { data: emptyLibrary() };
            }, { id: 'reading-status-batch', title: '批量阅读状态保存失败' }, true);
            if (!isCurrentScope()) return;
            setSelected(current => current.filter(id => !succeeded.has(id)));
            if (failed.length) {
                notify({ id: 'reading-status-batch', title: `阅读状态：${succeeded.size} 篇成功，${failed.length} 篇失败`,
                    message: failed.map(({ paper, message }) => `《${paper.metadata.title}》：${message}`).join('；'),
                    action: { label: '仅重试失败项', onClick: () => updateReadingStatuses(failed.map(item => item.paper), status) } });
                setActiveTool('imports');
            } else message.success(`已将 ${succeeded.size} 篇论文标为${statusLabel[status]}`);
        } catch { /* Mutation notification retains the failure. */ }
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
        const timer = setTimeout(() => { if (document.visibilityState !== 'hidden') void reload(); }, 12000);
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
    const libraryIndex = useMemo(() => indexLibrary(dataState), [dataState]);
    const materialByPaper = libraryIndex.materials;
    const scoped = useMemo(() => libraryView === 'directory' ? papersInView(data, folderId) : data.papers, [data, folderId, libraryView]);
    const collectionTags = useMemo(() => getPaperTags(data.papers.flatMap(paper => paper.tags)).sort(paperTagOrder.compare), [data.papers]);
    const viewed = useMemo(() => filterLibraryView(scoped, libraryView), [scoped, libraryView]);
    const visible = useMemo(() => filterPaperRecords(filterPapers(viewed, keyword), libraryIndex.attachments, filters), [viewed, keyword, libraryIndex, filters]);
    const [listPage, setListPage] = useState(1);
    const listScrollRef = useRef<HTMLDivElement>(null);
    const pageSize = 50;
    const currentPage = Math.min(listPage, Math.max(1, Math.ceil(visible.length / pageSize)));
    const pagePapers = visible.slice((currentPage - 1) * pageSize, currentPage * pageSize);
    useEffect(() => { if (listScrollRef.current) listScrollRef.current.scrollTop = 0; }, [currentPage]);
    useEffect(() => setListPage(1), [datasetId, libraryView, folderId, keyword, filters]);
    const selectedIds = useMemo(() => new Set(selected), [selected]);
    const selectedPapers = data.papers.filter((item) => selectedIds.has(item.id));
    const selectedVisibleCount = visible.filter((item) => selectedIds.has(item.id)).length;
    const exportPapers = selectedPapers.length ? selectedPapers : visible;
    const hasFilters = hasPaperFilters(filters);
    const confirmedDocument = (id: string) => {
        const identity = libraryIndex.identities.get(id);
        const paper = libraryIndex.papers.get(id);
        if (paper?.associationConflict || (paper?.academicWorkId && identity?.state.paper_id && paper.academicWorkId !== identity.state.paper_id))
            return undefined;
        return identity?.state.kind === 'paper' && identity.state.paper_id ? identity.state.document_id : undefined;
    };
    const selectedDocuments = selectedPapers.map(item => confirmedDocument(item.id)).filter((id): id is string => !!id);
    const hasStoredPdf = (id: string) => !!materialByPaper.get(id)?.pdfs.length;
    const selectionError = [
        selectedPapers.some(item => !hasStoredPdf(item.id)) ? '所选文献尚未关联入库 PDF，请先添加 PDF' : '',
        selectedPapers.some(item => hasStoredPdf(item.id) && !confirmedDocument(item.id)) ? '所选文献的论文身份尚未确认，请先识别或手动确认' : '',
    ].filter(Boolean).join('；') || undefined;
    const stats = useMemo(() => ({
        total: data.papers.length,
        important: data.papers.filter((item) => item.important).length,
    }), [data.papers]);
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
    const directoryPaperCount = folderId ? data.papers.filter(paper => paper.folderId && descendantFolderIds(data.folders, folderId).has(paper.folderId)).length : data.papers.length;
    const selectView = (id: string | null, view: LibraryView) => { setFolderId(view === 'directory' ? id : null); setLibraryView(view); setSelected([]); setDetailPaperId(null); };
    const openFolderDialog = (parentId: string | null) => { setNewFolderParent(parentId); setFolderName(''); setFolderError(false); setFolderDialogOpen(true); };
    const deleteFolder = (id: string) => {
        const folder = data.folders.find(f => f.id === id);
        if (!folder || disabled)
            return;
        const descendants = descendantFolderIds(data.folders, folder.id), count = data.papers.filter(p => paperBelongsToFolders(p, descendants)).length;
        Modal.confirm({ title: `删除「${folder.name}」及其子目录？`, content: `将移除 ${descendants.size} 个目录，把 ${count} 篇论文放回上一级；原文件、附件、笔记和收藏保留。`, okText: '删除目录', okButtonProps: { danger: true }, onOk: async () => { await mutate(() => deleteLibraryFolder(datasetId, folder), { id: `folder-delete:${folder.id}`, title: `目录「${folder.name}」删除失败` }); if (folderId && descendants.has(folderId)) setFolderId(folder.parentId || null); } });
    };
    const dropPapers = async (ids: string[]) => {
        if (disabled || busyRef.current)
            return;
        const papers = dataRef.current.papers.filter(p => ids.includes(p.id));
        await mutate(async () => {
            const changes = await deleteLibraryPapers(datasetId, papers);
            if (changes.cleanupPending)
                notify({ id: "paper-delete-cleanup", title: "论文已删除，文件清理未完成", message: "论文已从本库删除；部分源文件或检索数据清理失败，请联系管理员检查。" });
            return changes;
        }, { id: `paper-delete:${papers.map(paper => paper.id).sort().join(",")}`, title: `${papers.length} 篇论文删除失败` });
        setSyncRevision(value => value + 1);
        setSelected(current => current.filter(id => !ids.includes(id)));
        if (editing && ids.includes(editing.id))
            setEditing(null);
    };
    const confirmDeletePapers = (ids: string[]) => {
        const papers = dataRef.current.papers.filter(p => ids.includes(p.id));
        if (!papers.length) return;
        Modal.confirm({
            title: `彻底删除 ${papers.length} 篇论文？`,
            content: '将删除这些论文在本库中的原文件、附件和笔记，无法撤销。其他知识库中的副本不受影响。若只想整理目录，请选择“从此文件夹中移出”。',
            okText: '彻底删除', cancelText: '取消', okButtonProps: { danger: true },
            onOk: () => dropPapers(ids),
        });
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
            message.success('文献信息已保存');
            return true;
        }
        catch (error) {
            setEditError('修改尚未保存，已保留当前输入；请重试或查看通知。');
            return false;
        }
    };
    const folderOptions = [{ value: "none", label: title }, ...data.folders.map((folder) => ({ value: folder.id, label: paperFolderPath(data.folders, folder.id) }))];
    const primaryAttachment = (paperId: string) => {
        const attachments = materialByPaper.get(paperId)?.pdfs || [];
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
    const renderImportance = (item: PaperRecord) => (<PaperImportanceButton important={item.important} disabled={personalDisabled} onError={error => {
        if (isCurrentScope()) notify({ id: `paper-star:${item.id}`, title: `《${item.metadata.title}》重点标记保存失败`, message: error instanceof Error ? error.message : '标记保存失败，请重试' });
    }} onToggle={async (important) => {
        await mutate(async () => {
            if (!item.academicWorkId) throw new Error('论文身份尚未保存');
            await setPaperImportance(datasetId, item.academicWorkId, important, true);
            const current = dataRef.current;
            return { data: { ...emptyLibrary(), papers: current.papers.filter(p => p.id === item.id).map(p => ({ ...p, important })),
                attachments: current.attachments.filter(a => a.paperId === item.id), notes: current.notes.filter(n => n.paperId === item.id) } };
        }, { id: `paper-star:${item.id}`, title: `《${item.metadata.title}》重点标记保存失败` }, true);
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
            }} onOpenAttachment={onOpenAttachment} paper={detailPaper} onReparse={() => openReparse(detailPaper)} reparseDisabledReason={reparseDisabledReason(detailPaper)} data={dataState} onNotification={notify} onNotificationResolved={resolve} onClose={() => setDetailPaperId(null)} onAddNote={async (id, content) => { await mutate(() => addLibraryNote(datasetId, detailPaper.id, id, content), { id: `paper-note:${detailPaper.id}`, title: `《${detailPaper.metadata.title}》笔记保存失败`, action: { label: "查看笔记", onClick: () => openDetail(detailPaper.id, "notes") } }); }} disabled={disabled} personalDisabled={personalDisabled} onStatusChange={status => updatePaper(detailPaper.id, { status })} onSetPrimaryAttachment={async attachment => {
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
            screenReaderInstructions: { draggable: "拖到左侧文件夹以移动文献；键盘操作请使用文献操作菜单中的移动到。" },
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
          {onBack && <Button className="paper-library-back" type="text" icon={<ArrowLeftOutlined />} aria-label="返回知识库" onClick={onBack} />}
          <PaperBreadcrumb libraryName={title} onKnowledgeClick={onBack} />

        </div>
        <div className="paper-library-tools" role="toolbar" aria-label="文献工具">
          <PaperSearch open={activeTool === "search"} value={keyword} onChange={setKeyword} onOpenChange={setSearchOpen}/>
          <Popover trigger="click" placement="bottomRight" align={{ overflow: { adjustX: true, adjustY: true, shiftX: true, shiftY: true } }} arrow={false} destroyOnHidden overlayClassName="paper-library-tool-popover" open={activeTool === "filter"} onOpenChange={(open: boolean) => setActiveTool((current) => open ? "filter" : current === "filter" ? null : current)} content={<div role="dialog" aria-label="筛选文献" onKeyDown={(event) => {
                if (event.key === "Escape")
                    setActiveTool(null);
            }}>
              <PaperFilterPanel papers={data.papers} tags={collectionTags} value={filters} onChange={setFilters}/>
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
            <Button type="text" className="paper-library-tool" icon={<ExportOutlined />} aria-label="导出" title="导出题录或包含笔记、批注与附件引用的数据备份" disabled={!snapshotReady}>导出</Button>
          </Dropdown>
          <Tooltip title="预设 / AI 阅读准备"><Button type="text" className="paper-library-tool" icon={<SettingOutlined />} aria-label="预设 / AI 阅读准备" onClick={() => { setActiveTool(null); setReadingPreferencesOpen(true); }}>预设</Button></Tooltip>
          <ReadingPreferencesModal open={readingPreferencesOpen} onClose={() => setReadingPreferencesOpen(false)} />
          <Popover trigger="click" placement="bottomRight" open={activeTool === 'display'} onOpenChange={(open: boolean) => setActiveTool(current => open ? 'display' : current === 'display' ? null : current)} content={<div role="dialog" aria-label="列表显示设置">
            <Checkbox checked={display.status} onChange={(event: CheckboxChangeEvent) => setDisplayOption('status', event.target.checked)}>显示阅读状态</Checkbox>
            <Checkbox checked={display.importance} onChange={(event: CheckboxChangeEvent) => setDisplayOption('importance', event.target.checked)}>显示重要标记</Checkbox>
          </div>}><Button type="text" className="paper-library-tool" aria-label="列表显示设置">显示<DownOutlined/></Button></Popover>
          <PaperImportStatus key={datasetId} datasetId={datasetId} page={importPage} currentPage={currentImportPage} notifications={libraryNotices} syncError={syncError} refreshing={loading} onRefresh={() => { void reload(); }} canWrite={canWrite} onOpenPaper={id => openDetail(id)} onResumeAssociation={resumeAssociation} open={activeTool === "imports"} onOpenChange={open => setActiveTool(current => open ? "imports" : current === "imports" ? null : current)} onRetry={async (batch, key) => { await retryAcademicImport(batch, true, key); setSyncRevision(value => value + 1); }}/>
        </div>
      </header>
      {!loading && !canWrite && !persistenceError && <Alert type="info" showIcon message="当前文献库为只读；你仍可修改自己的阅读状态和重要标记"/>}
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
          <div className="paper-library-browse">
            <div className="paper-library-browse-head"><span className="paper-library-rail-label">浏览视图</span></div>
            <FolderButton icon={<FileTextOutlined />} name="全部" count={data.papers.length} current={libraryView === "all"} onClick={() => selectView(null, 'all')}/>
            <FolderButton icon={<StarOutlined />} name="收藏" count={stats.important} current={libraryView === "important"} onClick={() => selectView(null, "important")}/>
          </div>
          <div className="paper-library-directory">
            <div className="paper-library-browse-head"><span className="paper-library-rail-label">目录</span></div>
            <div className="paper-library-directory-scroll">
              <PaperDirectoryTree key={datasetId} data={data} title={title} active={libraryView === 'directory'} folderId={folderId} detailPaperId={detailPresentation === 'page' ? detailPaperId : null} onOpenPaper={id => openDetail(id, 'overview', 'page')} disabled={disabled} draggedIds={draggedIds} dropFolderId={dropFolderId} onSelectFolder={id => selectView(id, 'directory')} onCreateFolder={openFolderDialog} onDeleteFolder={deleteFolder}/>
            </div>
            {!!draggedIds.length && <p className="paper-library-drag-hint" role="status">移动 {draggedIds.length} 篇文献<br />拖到目标文件夹</p>}
          </div>
          <div ref={setTaskActionsContainer} className="paper-library-task-actions-slot"/>
        </nav>
        <div className="paper-library-board">
          {detailPresentation === 'page' && detailContent}
          <div hidden={detailPresentation === 'page' && !!detailPaper}>
          <div className="paper-library-view-heading">
            <span className="paper-library-view-path">{libraryView === 'directory' ? currentFolder ? [title, paperFolderPath(data.folders, currentFolder.parentId)].filter(Boolean).join(' / ') : '目录' : '浏览视图'}</span>
            <h2>{libraryView === 'all' ? '全部论文' : libraryView === 'important' ? '重点论文' : currentFolder?.name || title}</h2>
            <p>{filters.tags.length ? `筛选结果 ${visible.length} 篇` : libraryView === 'directory' ? `本层 ${scoped.length} 篇 · 含子目录 ${directoryPaperCount} 篇` : `${viewed.length} 篇论文`}</p>
            {filters.tags.length > 0 && <div className="paper-library-active-tags" role="group" aria-label="已选标签筛选">
              {filters.tags.length > 1 && <span className="paper-library-active-tag-mode">{PAPER_TAG_MATCH_LABELS[filters.tagMatch]}</span>}
              {filters.tags.map(tag => <button type="button" key={tag} aria-label={`移除标签筛选 ${tag}`}
                  onClick={() => setFilters(current => ({ ...current, tags: current.tags.filter(item => item !== tag) }))}><span>{tag}</span><CloseOutlined /></button>)}
            </div>}
          </div>
          <PaperChildFolders key={`${datasetId}:${libraryView}:${folderId}`} data={data} parentId={folderId} onSelect={id => selectView(id, 'directory')}/>
          <PaperTasks key={datasetId} dataset={datasetId} actionsContainer={taskActionsContainer} selected={selectedDocuments} selectedTitles={selectedPapers.map(item => item.metadata.title)} selectionError={selectionError} canImport={canWrite} onImportSubmitted={() => setSyncRevision(value => value + 1)} onNotification={notify} onNotificationResolved={resolve}/>
          {selectedPapers.length > 0 && (<div className="paper-library-selection-bar" role="region" aria-label="已选论文">
          <div className="paper-library-selection-summary"><Button type="text" size="small" aria-label="取消选择" icon={<CloseOutlined />} onClick={() => setSelected([])}/><span>已选 <strong>{selectedPapers.length}</strong> 篇</span></div>
          <Dropdown trigger={["click"]} menu={{ items: (Object.keys(statusLabel) as ReadingStatus[]).map(status => ({ key: status, label: `标为${statusLabel[status]}`, onClick: () => void updateReadingStatuses(selectedPapers, status) })) }}><Button type="text" size="small" disabled={personalDisabled}>阅读状态<DownOutlined/></Button></Dropdown>
          <Dropdown trigger={["click"]} menu={{ items: folderOptions.map((folder) => ({ key: folder.value, label: folder.label, onClick: () => placePapers(selected, folder.value) })) }}><Button type="text" size="small" disabled={disabled} icon={<FolderOutlined />}>移动到<DownOutlined /></Button></Dropdown>
          <Button type="text" size="small" disabled={disabled || !selectedPapers.some(paper => paper.folderId || paper.collectionIds?.length)} onClick={() => void placePapers(selected, 'none')}>从文件夹中移出</Button>
          <Button type="text" size="small" disabled={disabled} danger icon={<DeleteOutlined />} onClick={() => confirmDeletePapers(selected)}>彻底删除</Button>
          </div>)}
          <div className="paper-library-table-scroll" aria-busy={initialLoading || loading}>
          <table className="paper-library-table" aria-label="论文列表">
            <colgroup><col className="paper-library-controls-col"/><col /><col className="paper-library-actions-col"/></colgroup>
            <thead>
              <tr>
                <th scope="col" className={`paper-library-selection-head ${selectedVisibleCount ? "has-selection" : ""}`}>
                  <input type="checkbox" className="paper-library-checkbox" aria-label="选择当前列表全部论文" disabled={!visible.length} checked={visible.length > 0 && selectedVisibleCount === visible.length} ref={(element) => {
            if (element)
                element.indeterminate = selectedVisibleCount > 0 && selectedVisibleCount < visible.length;
        }} onChange={(event) => setSelected(event.target.checked ? [...new Set([...selected, ...visible.map((item) => item.id)])] : selected.filter((id) => !visible.some((item) => item.id === id)))}/>
                </th>
                <th scope="col">{libraryView === 'directory' ? '本层论文' : '论文'}</th><th scope="col"><span className="paper-library-visually-hidden">文献操作</span></th>
              </tr>
            </thead>
            <tbody>
          {initialLoading && <tr><td colSpan={3}><div className="paper-library-loading" role="status"><Spin size="small"/>正在加载文献…</div></td></tr>}
          {!initialLoading && !visible.length && <tr><td colSpan={3}><div className="paper-library-empty">{persistenceError && !snapshotReady ? "论文库尚未加载成功，请打开右上角的通知重新加载。" : "没有符合条件的论文。可以导入 DOI、arXiv、BibTeX、RIS、CSV，或上传 PDF。"}</div></td></tr>}
          {!initialLoading && pagePapers.map((item) => (<tr className={`paper-library-row ${selected.includes(item.id) ? "selected" : ""} ${draggedIds.includes(item.id) ? "is-dragging" : ""}`} key={item.id}>
              <td className="paper-library-controls-cell"><div className="paper-library-row-controls">
              <input type="checkbox" className="paper-library-checkbox" aria-label={`选择 ${item.metadata.title}`} checked={selected.includes(item.id)} onChange={(event: ChangeEvent<HTMLInputElement>) => setSelected(event.target.checked ? [...selected, item.id] : selected.filter((id) => id !== item.id))}/>
              <PaperDragHandle disabled={disabled} item={item}/>
              </div></td>
              <td className="paper-library-main">
                  <div className="paper-library-title-line"><h2 className="paper-library-title">{(() => {
                    const primary = materialByPaper.get(item.id)?.pdfs.find(attachment => attachment.isPrimary) || materialByPaper.get(item.id)?.pdfs[0];
                    return onOpenAttachment && primary?.contentRef ? <a className="paper-library-title-button" href={`/lib/knowledge/knowledge/${encodeURIComponent(datasetId)}/${encodeURIComponent(primary.contentRef)}`} onClick={event => { if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) { event.preventDefault(); onOpenAttachment(primary.contentRef!); } }}>{item.metadata.title}</a> : <button type="button" className="paper-library-title-button" onClick={() => openDetail(item.id)}>{item.metadata.title}</button>;
                  })()}</h2>
                    {display.importance && renderImportance(item)}

                  </div>
                <p className="paper-library-cite"><PaperAuthors authors={item.metadata.authors} empty="作者待补全" fitToWidth /></p>
                {(() => {
                const identity = libraryIndex.identities.get(item.id);
                const status = identity?.state.status;
                return status && status !== 'succeeded' ? <button type="button" className="paper-library-state" onClick={() => openDetail(item.id)}>{status === 'failed' ? '论文信息识别失败' : '论文信息识别中'}</button> : null;
            })()}
                <div className="paper-library-meta">
                  <span className="paper-library-year">{item.metadata.year || "—"}</span>
                  <span className="paper-library-venue" title={venueName(item)}>{item.metadata.venue?.trim() ? venueName(item) : "—"}</span>
                  {item.incomplete && snapshotReady && <Tooltip title="论文题录信息不完整，点击补全"><button type="button" className="paper-library-state" disabled={disabled} onClick={() => {
                    openDetail(item.id);
                    setDetailEditing(true);
                    editingRevisionRef.current = item.revision;
                    setEditing(item);
                    setEditError(null);
                }}>题录待补全</button></Tooltip>}
                  {item.parse === "failed" && <Tooltip title={item.processingError || "正文解析失败，请检查导入任务"}><span className="paper-library-warn">正文解析失败</span></Tooltip>}
                  {item.parse === "parsing" && <span className="paper-library-source">正文解析中</span>}
                  {item.associationConflict && <button type="button" className="paper-library-warn" onClick={() => openDetail(item.id, 'attachments')}>附件归属待核对</button>}
                  <button type="button" className="paper-library-material" onClick={() => openDetail(item.id, "attachments")}>{materialByPaper.get(item.id)?.pdfs.length ? `PDF · ${materialByPaper.get(item.id)!.pdfs.length} 份` : materialByPaper.get(item.id)?.links.length ? '原文链接' : materialByPaper.get(item.id)?.supplements.length ? '无 PDF' : '暂无 PDF'}</button>
                  {!!materialByPaper.get(item.id)?.supplements.length && <button type="button" className="paper-library-material" onClick={() => openDetail(item.id, 'attachments')}>补充材料 · {materialByPaper.get(item.id)!.supplements.length} 份</button>}
                  {!!materialByPaper.get(item.id)?.notes && <button type="button" className="paper-library-material" onClick={() => openDetail(item.id, "notes")}>{materialByPaper.get(item.id)?.notes} 条笔记</button>}
                  <PaperTags paper={item} collectionTags={collectionTags} canWrite={canWrite} disabled={disabled} onChange={change => updatePaperTags(item, change)} />
                </div>
              </td>
              <td><div className="paper-library-row-actions">
                    {display.status && <Select className="paper-library-status" variant="filled" size="small" value={item.status} disabled={personalDisabled} aria-label={`${item.metadata.title} 的阅读状态`} options={(Object.keys(statusLabel) as ReadingStatus[]).map((key) => ({ value: key, label: statusLabel[key] }))} onChange={(value: ReadingStatus) => void updatePaper(item.id, { status: value })}/>}
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
                    { key: "unfile", label: "从文件夹中移出", disabled: disabled || !(item.folderId || item.collectionIds?.length), onClick: () => void placePapers([item.id], 'none') },
                    { key: "delete", label: "彻底删除", disabled, danger: true, onClick: () => confirmDeletePapers([item.id]) },
                ] }}>
                    <button type="button" className="paper-library-more" aria-label={`${item.metadata.title} 的更多操作`}><EllipsisOutlined /></button>
                  </Dropdown>
              </div></td>
            </tr>))}
            </tbody>
          </table>
          </div>
          {visible.length > pageSize && <Pagination className="paper-library-pagination" size="small" current={currentPage} pageSize={pageSize} total={visible.length} showSizeChanger={false} showTotal={(total: number) => `共 ${total} 篇`} onChange={setListPage} />}
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
      <ZoteroImportModal persistedPapers={data.papers} open={zoteroOpen} datasetId={datasetId} canWrite={canWrite} initialFolderId={libraryView === 'directory' ? folderId : null} onCancel={() => { setZoteroOpen(false); zoteroKey.current = { signature: '', key: '' }; }} onImport={importZotero}/>
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
function PaperChildFolders({ data, parentId, onSelect }: {
    data: PaperLibraryData;
    parentId: string | null;
    onSelect: (id: string) => void;
}) {
    const gridRef = useRef<HTMLDivElement>(null);
    const gridId = useId();
    const [columns, setColumns] = useState(3);
    const [expanded, setExpanded] = useState(false);
    const folders = data.folders.filter(folder => (folder.parentId || null) === parentId);
    useEffect(() => {
        const grid = gridRef.current;
        if (!grid) return;
        const measure = () => {
            if (!grid.clientWidth) return;
            setColumns(getComputedStyle(grid).gridTemplateColumns.split(' ').length);
        };
        measure();
        const observer = new ResizeObserver(measure);
        observer.observe(grid);
        return () => observer.disconnect();
    }, [folders.length]);
    if (!folders.length) return null;
    return <section className="paper-library-child-folders" aria-label="子目录">
      <div className="paper-library-child-heading"><span>子目录 · {folders.length}</span>{folders.length > columns * 2 && <Button type="text" size="small" aria-expanded={expanded} aria-controls={gridId} onClick={() => setExpanded(value => !value)}>{expanded ? '收起' : `展开全部（${folders.length}）`}</Button>}</div>
      <div ref={gridRef} id={gridId} className="paper-library-child-grid">{folders.slice(0, expanded ? folders.length : columns * 2).map(folder => {
          const descendants = descendantFolderIds(data.folders, folder.id);
          const count = data.papers.filter(paper => !!paper.folderId && descendants.has(paper.folderId)).length;
          return <Tooltip title={folder.name} key={folder.id}><Button type="text" icon={<FolderOutlined />} onClick={() => onSelect(folder.id)}><span className="paper-library-child-name">{folder.name}</span><span className="paper-library-child-count">{count} 篇</span></Button></Tooltip>;
      })}</div>
    </section>;
}
function PaperDragHandle({ item, disabled }: {
    item: PaperRecord;
    disabled: boolean;
}) {
    const { attributes, listeners, setNodeRef } = useDraggable({ id: item.id, disabled });
    return <button {...attributes} {...listeners} ref={setNodeRef} type="button" className="paper-library-drag-handle" disabled={disabled} aria-label={`拖动 ${item.metadata.title}`} title="拖到左侧文件夹" onClick={event => event.stopPropagation()}><HolderOutlined /></button>;
}
function FolderButton({ name, icon, count, current, onClick }: {
    name: string;
    icon: ReactNode;
    count: number;
    current: boolean;
    onClick: () => void;
}) {
    return <div className={`paper-library-folder ${current ? 'current' : ''}`}><button className="paper-library-folder-select" type="button" aria-current={current ? 'true' : undefined} onClick={onClick}><span className="paper-library-folder-name">{icon}<span title={name}>{name}</span></span><span className="paper-library-folder-count">{count}</span></button></div>;
}
