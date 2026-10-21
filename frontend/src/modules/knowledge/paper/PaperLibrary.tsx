import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type ChangeEvent, type DragEvent, type DragEventHandler, type CSSProperties } from "react";
import { Alert, Button, Dropdown, Input, Modal, Popover, Radio, Select, Tooltip, Upload, message, type RadioChangeEvent, type MenuProps } from "antd";
import { DndContext, DragOverlay, PointerSensor, pointerWithin, useDraggable, useDroppable, useSensor, useSensors, type DragEndEvent, type DragStartEvent, type DragOverEvent } from "@dnd-kit/core";
import { ArrowLeftOutlined, DownOutlined, RightOutlined, EditOutlined, FileTextOutlined, FolderOutlined, InfoCircleOutlined, LinkOutlined, PlusOutlined, DeleteOutlined, StarOutlined, StarFilled, CloseOutlined, UploadOutlined, FilterOutlined, ExportOutlined, HolderOutlined, EllipsisOutlined } from "@ant-design/icons";
import { v4 as uuidv4 } from "uuid";
import {
  applyPaperEdits,
  assignFolders,
  citationLabel,
  classifyIdentifier,
  duplicateOf,
  filterPapers,
  filterLibraryView,
  formatCitation,
  paperLink,
  papersInView,
  venueName,
  parseBibTeX,
  parseCSV,
  parseRIS,
  planImport,
  sourceLabel,
  statusLabel,
  usablePaper,
  type PaperEdits,
  type CitationStyle,
  type LibraryView,
  type PaperItem,
  type PaperLibraryData,
  type PaperFolder,
  type ReadingStatus,
} from "./model";
import { loadPreviewPaperLibrary, savePreviewPaperLibrary, loadPersistedPaperLibrary, savePersistedPaperLibrary, type PersistedPaperLibrary } from "./store";
import { asLegacyLibrary, emptyLibraryV3, createAttachment, type Attachment, type PaperLibraryDataV3 } from "./libraryModel";
import PaperDetailDrawer from "./PaperDetailDrawer";
import { lookupIdentifier } from "./scholar";
import { paperFromPdf } from "./pdfMeta";
import { downloadPaperCitations } from "./exportLibrary";
import { importArxivPaper, isArxivInput, listImportedArxivPapers } from "./arxivImport";
import { mergeArxivImports } from "./arxivLibrary";
import PaperSearch from "./PaperSearch";
import PaperFilterPanel from "./PaperFilterPanel";
import { DEFAULT_PAPER_FILTERS, filterPaperRecords, hasPaperFilters, isReadablePdfAttachment, type PaperFilters } from "./paperFilters";
import { deleteFolderSubtree, descendantFolderIds, flattenFolderTree, folderPath, movePaperRecords, syncFolderCollections, updateFolderTree } from "./folderTree";
import ZoteroImportModal from "./ZoteroImportModal";
import { executeZoteroImport, storeZoteroAttachment } from "./zoteroCommit";
import type { ZoteroImportConfig, ZoteroImportOptions, ZoteroPreview } from "./zoteroImport";
import "./PaperLibrary.scss";

interface Props {
  datasetId: string;
  preview?: boolean;
  onUploadPdf?: (file: File) => Promise<string | void>;
  title?: string;
  onBack?: () => void;
}

type Sheet = "review" | "edit" | null;
const PAPER_DRAG_TYPE = "application/x-lazymind-papers";
type ImportMethod = "upload" | "identifier" | "bibliography";
const importTitles: Record<ImportMethod, string> = {
  upload: "上传文献", identifier: "通过链接导入", bibliography: "导入文献题录",
};

async function copyText(value: string, label: string) {
  try {
    await navigator.clipboard.writeText(value);
  } catch {
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

export default function PaperLibrary({ datasetId, preview, onUploadPdf, title = "论文库", onBack }: Props) {
  const dragSensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));
  const isDemo = !!preview || datasetId === "paper-preview";
  const datasetScopeRef = useRef({ datasetId, isDemo, active: true });
  if (datasetScopeRef.current.datasetId !== datasetId || datasetScopeRef.current.isDemo !== isDemo) datasetScopeRef.current = { datasetId, isDemo, active: true };
  const datasetScope = datasetScopeRef.current;
  const isCurrentScope = () => datasetScopeRef.current === datasetScope && datasetScope.active;
  const [modernData, setModernData] = useState<PaperLibraryDataV3>(() => isDemo ? loadPreviewPaperLibrary(datasetId, true) : emptyLibraryV3());
  const [data, setData] = useState<PaperLibraryData>(() => asLegacyLibrary(modernData));
  const [loading, setLoading] = useState(!isDemo);
  const [saving, setSaving] = useState(false);
  const [confirmingImport, setConfirmingImport] = useState(false);
  const [canWrite, setCanWrite] = useState(isDemo);
  const [persistenceError, setPersistenceError] = useState<string | null>(null);
  const revisionRef = useRef(0);
  const loadedRef = useRef(isDemo);
  const canWriteRef = useRef(isDemo);
  const busyRef = useRef(false);
  const zoteroImportingRef = useRef(false);
  const activeDatasetRef = useRef(datasetId);
  activeDatasetRef.current = datasetId;
  const disabled = loading || saving || confirmingImport || !canWrite;
  const modernDataRef = useRef(modernData);
  const syncGeneration = useRef(0);
  const [syncRevision, setSyncRevision] = useState(0);
  const [syncError, setSyncError] = useState<string | null>(null);
  const [pendingArxiv, setPendingArxiv] = useState<PaperItem | null>(null);
  const [detailPaperId, setDetailPaperId] = useState<string | null>(null);
  const [folderId, setFolderId] = useState<string | null>(null);
  const [libraryView, setLibraryView] = useState<LibraryView>("all");
  const [filters, setFilters] = useState<PaperFilters>(DEFAULT_PAPER_FILTERS);
  const [keyword, setKeyword] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [draggedIds, setDraggedIds] = useState<string[]>([]);
  const [dropFolderId, setDropFolderId] = useState<string | null>(null);
  const paperDrag = useRef<{ datasetId: string; ids: string[] } | null>(null);
  const [sheet, setSheet] = useState<Sheet>(null);
  const [activeTool, setActiveTool] = useState<"search" | "filter" | null>(null);
  const setSearchOpen = useCallback((open: boolean) => setActiveTool((current) => open ? "search" : current === "search" ? null : current), []);
  const [importMethod, setImportMethod] = useState<ImportMethod | null>(null);
  const [zoteroOpen, setZoteroOpen] = useState(false);
  const [folderDialogOpen, setFolderDialogOpen] = useState(false);
  const [editing, setEditing] = useState<PaperItem | null>(null);
  const editingRevisionRef = useRef<number | null>(null);
  const [editError, setEditError] = useState<string | null>(null);
  const [folderName, setFolderName] = useState("");
  const [folderParentId, setFolderParentId] = useState<string | null>(null);
  const [editingFolderId, setEditingFolderId] = useState<string | null>(null);
  const [folderError, setFolderError] = useState<string | null>(null);
  const [collapsedFolders, setCollapsedFolders] = useState<Set<string>>(() => new Set());
  const folderDialogRevisionRef = useRef(0);
  const [identifier, setIdentifier] = useState("");
  const [bibliography, setBibliography] = useState("");
  const bibliographyKind = "local";
  const [format, setFormat] = useState<"bib" | "ris" | "csv">("bib");
  const [searching, setSearching] = useState(false);
  const [pending, setPending] = useState<{ fresh: PaperItem[]; skipped: PaperItem[]; folders: PaperLibraryData["folders"]; baseFolderIds?: string[]; attachTo?: PaperItem } | null>(null);
  const [pendingAttachments, setPendingAttachments] = useState<Attachment[]>([]);
  const [pendingPdf, setPendingPdf] = useState<{ paperId: string; file: File } | null>(null);
  const [importError, setImportError] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const applySnapshot = useCallback((next: PaperLibraryDataV3) => {
    modernDataRef.current = next;
    setModernData(next);
    setData(asLegacyLibrary(next));
  }, []);

  const reload = useCallback(async () => {
    if (isDemo || busyRef.current || zoteroImportingRef.current || datasetScopeRef.current !== datasetScope || !datasetScope.active) return;
    busyRef.current = true;
    const generation = ++syncGeneration.current;
    setLoading(true);
    setPersistenceError(null);
    try {
      const saved = await loadPersistedPaperLibrary(datasetId);
      if (generation !== syncGeneration.current || activeDatasetRef.current !== datasetId) return;
      revisionRef.current = saved.revision;
      loadedRef.current = true;
      canWriteRef.current = saved.canWrite;
      setCanWrite(saved.canWrite);
      applySnapshot(saved.data);
      setSyncRevision((value) => value + 1);
    } catch (error) {
      if (generation === syncGeneration.current && activeDatasetRef.current === datasetId) {
        loadedRef.current = false;
        canWriteRef.current = false;
        setCanWrite(false);
        setPersistenceError(error instanceof Error ? error.message : "无法加载已保存的文献，请重试");
      }
    } finally {
      if (generation === syncGeneration.current && activeDatasetRef.current === datasetId) {
        busyRef.current = false;
        setLoading(false);
      }
    }
  }, [datasetId, isDemo, datasetScope, applySnapshot]);

  useEffect(() => {
    datasetScope.active = true;
    syncGeneration.current += 1;
    busyRef.current = false;
    zoteroImportingRef.current = false;
    loadedRef.current = isDemo;
    canWriteRef.current = isDemo;
    setCanWrite(isDemo);
    setSaving(false);
    setConfirmingImport(false);
    setPersistenceError(null);
    setSyncError(null);
    setSelected([]);
    setDetailPaperId(null);
    setFolderId(null);
    setFolderDialogOpen(false);
    setCollapsedFolders(new Set());
    setSheet(null);
    setImportMethod(null);
    setZoteroOpen(false);
    setSearching(false);
    setPending(null);
    setPendingArxiv(null);
    setPendingAttachments([]);
    setPendingPdf(null);
    if (isDemo) {
      applySnapshot(loadPreviewPaperLibrary(datasetId, true));
      setLoading(false);
    } else {
      applySnapshot(emptyLibraryV3());
      void reload();
    }
    const onFocus = () => { void reload(); };
    window.addEventListener("focus", onFocus);
    return () => {
      datasetScope.active = false;
      syncGeneration.current += 1;
      window.removeEventListener("focus", onFocus);
    };
  }, [datasetId, isDemo, datasetScope, reload, applySnapshot]);

  const persistModern = useCallback(async (next: PaperLibraryDataV3) => {
    if (datasetScopeRef.current !== datasetScope || !datasetScope.active) throw new Error("文献库已切换，请重新打开后重试");
    if (busyRef.current || !loadedRef.current || !canWriteRef.current) throw new Error("文献库尚未就绪、正在保存或没有编辑权限，请稍后重试");
    busyRef.current = true;
    const generation = ++syncGeneration.current;
    setSaving(true);
    setPersistenceError(null);
    try {
      if (isDemo) {
        savePreviewPaperLibrary(datasetId, next);
        applySnapshot(next);
      } else {
        const saved = await savePersistedPaperLibrary(datasetId, next, revisionRef.current);
        if (generation !== syncGeneration.current || activeDatasetRef.current !== datasetId) throw new Error("文献库已切换，请重新加载查看保存结果");
        revisionRef.current = saved.revision;
        canWriteRef.current = saved.canWrite;
        setCanWrite(saved.canWrite);
        applySnapshot(saved.data);
      }
    } catch (error) {
      if (generation === syncGeneration.current && activeDatasetRef.current === datasetId) setPersistenceError(error instanceof Error ? error.message : "文献保存失败，请重试");
      throw error;
    } finally {
      if (generation === syncGeneration.current && activeDatasetRef.current === datasetId) {
        busyRef.current = false;
        setSaving(false);
      }
    }
  }, [datasetId, isDemo, datasetScope, applySnapshot]);

  const importZotero = async (previewData: ZoteroPreview, config: ZoteroImportConfig, options: ZoteroImportOptions) => {
    if (!isCurrentScope() || disabled || busyRef.current || zoteroImportingRef.current) throw new Error("文献库正在保存或没有编辑权限，请稍后重试");
    if (isDemo) throw new Error("请进入实际论文知识库后导入 Zotero，演示页面无法保存附件");
    zoteroImportingRef.current = true;
    syncGeneration.current += 1;
    setConfirmingImport(true);
    const checkScope = () => {
      if (!isCurrentScope()) throw new Error("文献库已切换，已保存的导入结果可在原文献库查看");
    };
    const accept = (saved: PersistedPaperLibrary) => {
      checkScope();
      revisionRef.current = saved.revision;
      canWriteRef.current = saved.canWrite;
      setCanWrite(saved.canWrite);
      applySnapshot(saved.data);
    };
    try {
      const result = await executeZoteroImport({
        datasetId, preview: previewData, config, options,
        getLibrary: () => { checkScope(); return modernDataRef.current; },
        saveLibrary: persistModern,
        storeAttachment: async (file, candidate) => {
          checkScope();
          try {
            accept(await storeZoteroAttachment(datasetId, file, candidate));
          } catch (error) {
            checkScope();
            const snapshot = (error as { library?: PersistedPaperLibrary }).library;
            if (snapshot) accept(snapshot);
            else {
              // A lost response may follow a committed transaction. Reload before any retry.
              try { accept(await loadPersistedPaperLibrary(datasetId)); }
              catch (reloadError) {
                if (isCurrentScope()) setPersistenceError(reloadError instanceof Error ? reloadError.message : "无法确认附件保存结果，请重新加载");
                throw reloadError;
              }
            }
            throw error;
          }
        },
      });
      checkScope();
      if (!result.failures?.length) message.success(`已导入 ${result.imported} 篇文献${result.attachments ? `、${result.attachments} 个 PDF` : ""}`);
      return result;
    } finally {
      if (isCurrentScope()) { zoteroImportingRef.current = false; setConfirmingImport(false); }
    }
  };

  const persist = async (next: PaperLibraryData, attachments: Attachment[] = []) => {
    const current = modernDataRef.current;
    await persistModern({
      ...current,
      folders: next.folders,
      attachments: [...current.attachments.filter((item) => !attachments.some((attachment) => attachment.id === item.id)), ...attachments],
      papers: next.papers.map((item) => {
        const previous = current.papers.find((paper) => paper.id === item.id);
        const attachmentIds = attachments.filter((attachment) => attachment.paperId === item.id).map((attachment) => attachment.id);
        return previous ? { ...previous, ...item, attachmentIds: [...new Set([...previous.attachmentIds, ...attachmentIds])], metadata: { ...previous.metadata, identifiers: { ...previous.metadata.identifiers, doi: item.doi, arxiv: item.arxiv } } } : { ...item, metadata: { identifiers: { doi: item.doi, arxiv: item.arxiv }, confidence: {} }, attachmentIds, noteIds: [], annotationIds: [], collectionIds: [], relatedPaperIds: [] };
      }),
    });
  };

  const updatePaper = async (id: string, changes: Partial<PaperItem>) => {
    try {
      const current = asLegacyLibrary(modernDataRef.current);
      await persist({ ...current, papers: current.papers.map((paper) => paper.id === id ? { ...paper, ...changes } : paper) });
    } catch { /* persistModern keeps the error visible and retains the last saved snapshot. */ }
  };

  useEffect(() => {
    if (isDemo || !loadedRef.current) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const retry = () => { if (!cancelled) timer = setTimeout(sync, 12000); };
    const sync = async () => {
      if (busyRef.current || zoteroImportingRef.current) { retry(); return; }
      const generation = syncGeneration.current;
      try {
        const imported = await listImportedArxivPapers(datasetId);
        if (cancelled) return;
        if (generation !== syncGeneration.current || busyRef.current || zoteroImportingRef.current) { retry(); return; }
        if (imported.length) {
          const current = modernDataRef.current;
          const next = mergeArxivImports(current, imported);
          if (JSON.stringify(next) !== JSON.stringify(current)) {
            if (canWriteRef.current) await persistModern(next);
            else applySnapshot(next);
          }
        }
        if (cancelled) return;
        setSyncError(null);
        if (imported.some((item) => item.processing_status === "parsing")) retry();
      } catch (error) {
        if (!cancelled) setSyncError(error instanceof Error ? error.message : "无法同步已保存的 arXiv 文献");
      }
    };
    void sync();
    return () => { cancelled = true; if (timer) clearTimeout(timer); };
  }, [datasetId, isDemo, syncRevision, persistModern, applySnapshot]);

  const scoped = useMemo(() => papersInView(modernData, folderId), [modernData, folderId]);
  const viewed = useMemo(() => filterLibraryView(scoped, libraryView), [scoped, libraryView]);
  const visible = useMemo(
    () => filterPaperRecords(filterPapers(viewed, "all", [], "all", keyword), modernData.attachments, filters),
    [viewed, keyword, modernData.attachments, filters],
  );
  const selectedPapers = data.papers.filter((item) => selected.includes(item.id));
  const selectedVisibleCount = visible.filter((item) => selected.includes(item.id)).length;
  const exportPapers = selectedPapers.length ? selectedPapers : visible;
  const hasFilters = hasPaperFilters(filters);
  const readyCount = selectedPapers.filter(usablePaper).length;
  const stats = useMemo(() => ({
    total: data.papers.length,
    important: data.papers.filter((item) => item.important).length,
  }), [data.papers]);
  const materialByPaper = useMemo(() => new Map(modernData.papers.map((paper) => [paper.id, {
    attachments: paper.attachmentIds.length,
    notes: paper.noteIds.length,
  }])), [modernData.papers]);

  const clearImportResult = () => {
    setImportError(null);
    setPendingArxiv(null);
    setPending(null);
    setPendingAttachments([]);
    setPendingPdf(null);
  };

  const showImportError = (error: unknown, fallback: string) => {
    setImportError(error instanceof Error && error.message ? error.message : fallback);
  };

  const stage = (items: PaperItem[], folderNames?: Array<string | null>) => {
    setPendingAttachments([]);
    if (!items.length) {
      setPending(null);
      throw new Error("没有识别到文献，请检查文件内容或题录格式");
    }
    const current = asLegacyLibrary(modernDataRef.current);
    const targetFolder = current.folders.some((folder) => folder.id === folderId) ? folderId : null;
    const placed = assignFolders(current, items, folderNames || items.map(() => null), targetFolder);
    const planned = planImport(current.papers, placed.papers);
    setPending({ ...planned, folders: placed.folders, baseFolderIds: current.folders.map((folder) => folder.id) });
    if (!planned.fresh.length) message.info(`库中已有，跳过 ${planned.skipped.length} 篇`);
  };

  const commit = async () => {
    if (disabled || busyRef.current || searching || !pending || (!pending.fresh.length && !pending.attachTo)) return;
    busyRef.current = true;
    syncGeneration.current += 1;
    setConfirmingImport(true);
    setImportError(null);
    try {
      const current = asLegacyLibrary(modernDataRef.current);
      const folders = [...current.folders];
      const folderIds = new Map(current.folders.map((folder) => [folder.id, folder.id]));
      for (const folder of pending.folders) {
        if (folderIds.has(folder.id) || pending.baseFolderIds?.includes(folder.id)) continue;
        const existing = folders.find((item) => item.name === folder.name && (item.parentId || null) === (folder.parentId || null));
        if (!existing) folders.push(folder);
        folderIds.set(folder.id, existing?.id || folder.id);
      }
      const planned = planImport(current.papers, pending.fresh.map((paper) => ({ ...paper, folderId: paper.folderId ? folderIds.get(paper.folderId) || null : null })));
      const pdfCandidate = pendingPdf && pending.fresh.find((paper) => paper.id === pendingPdf.paperId);
      const pdfTarget = pendingPdf ? pending.attachTo
        ? current.papers.find((paper) => paper.id === pending.attachTo!.id)
        : pdfCandidate && (duplicateOf(current.papers, pdfCandidate) || planned.fresh.find((paper) => paper.id === pdfCandidate.id))
        : undefined;
      if (pendingPdf && !pdfTarget) throw new Error("目标文献已被移除，请重新识别 PDF 后确认导入");
      if (planned.fresh.length || pdfTarget) {
        let attachments = pendingPdf ? pendingAttachments : pendingAttachments.filter((attachment) => planned.fresh.some((paper) => paper.id === attachment.paperId));
        if (pdfTarget) {
          const existing = modernDataRef.current.papers.find((paper) => paper.id === pdfTarget.id);
          const hasPdf = modernDataRef.current.attachments.some((attachment) => attachment.paperId === pdfTarget.id && existing?.attachmentIds.includes(attachment.id) && isReadablePdfAttachment(attachment));
          attachments = attachments.map((attachment) => {
            const saved = modernDataRef.current.attachments.find((item) => item.id === attachment.id && item.paperId === pdfTarget.id);
            return { ...attachment, paperId: pdfTarget.id, isPrimary: saved?.isPrimary ?? !hasPdf };
          });
        }
        if (pendingPdf && !isDemo && !attachments.some((attachment) => attachment.contentRef)) {
          if (!onUploadPdf) throw new Error("当前论文库无法上传 PDF，请刷新页面后重试");
          const documentId = await onUploadPdf(pendingPdf.file);
          if (!isCurrentScope()) return;
          if (typeof documentId !== "string" || !documentId.trim()) throw new Error("PDF 上传未返回有效文档 ID，请重试");
          attachments = attachments.map((attachment) => ({ ...attachment, contentRef: documentId.trim() }));
          // Keep the accepted upload for retry if the subsequent library save fails.
          setPendingAttachments(attachments);
        }
        busyRef.current = false;
        await persist({ folders, papers: [...planned.fresh, ...current.papers] }, attachments);
        if (!isCurrentScope()) return;
        message.success(pdfTarget && !planned.fresh.length ? "已为文献添加 PDF" : `已加入 ${planned.fresh.length} 篇，跳过 ${pending.skipped.length + planned.skipped.length} 篇`);
      } else message.info(`库中已有，跳过 ${planned.skipped.length} 篇`);
      clearImportResult();
      setImportMethod(null);
    } catch (error) {
      if (isCurrentScope()) showImportError(error, "文献保存失败，请重试");
    } finally {
      if (isCurrentScope()) { busyRef.current = false; setConfirmingImport(false); }
    }
  };

  const expandFolderPath = (parentId: string | null, folders = modernDataRef.current.folders) => {
    const expanded = new Set<string>();
    let id = parentId;
    while (id && !expanded.has(id)) {
      expanded.add(id);
      id = folders.find((folder) => folder.id === id)?.parentId || null;
    }
    setCollapsedFolders((current) => new Set([...current].filter((id) => !expanded.has(id))));
  };

  const openFolderDialog = (folder?: PaperFolder, parentId = folderId) => {
    if (disabled || busyRef.current) return;
    setEditingFolderId(folder?.id || null);
    setFolderName(folder?.name || "");
    setFolderParentId(folder ? folder.parentId || null : parentId);
    setFolderError(null);
    folderDialogRevisionRef.current = revisionRef.current;
    setFolderDialogOpen(true);
  };

  const saveFolder = async () => {
    if (disabled || busyRef.current) return;
    const name = folderName.trim();
    if (!name) return;
    setFolderError(null);
    if (folderDialogRevisionRef.current !== revisionRef.current) {
      setFolderError("文献库已更新，请重新打开文件夹设置后再保存");
      return;
    }
    const current = modernDataRef.current;
    const existing = current.folders.find((folder) => folder.id === editingFolderId);
    if (editingFolderId && !existing) { setFolderError("文件夹已被删除，请重新加载"); return; }
    const folder: PaperFolder = { ...existing, id: existing?.id || `folder-${uuidv4()}`, name, parentId: folderParentId };
    try {
      await persistModern(updateFolderTree(current, folder));
      if (!isCurrentScope()) return;
      expandFolderPath(folderParentId);
      setFolderName("");
      setFolderDialogOpen(false);
      selectView(folder.id);
    } catch (error) {
      if (isCurrentScope()) setFolderError(error instanceof Error ? error.message : "文件夹保存失败，请重试");
    }
  };

  const selectView = (id: string | null, view: LibraryView = "all") => {
    setFolderId(id);
    setLibraryView(view);
    setSelected([]);
  };

  const currentFolder = data.folders.find((folder) => folder.id === folderId);
  const deleteFolder = (folder = currentFolder) => {
    if (disabled || busyRef.current || !folder) return;
    const count = descendantFolderIds(modernDataRef.current.folders, folder.id).size - 1;
    Modal.confirm({
      title: `删除文件夹「${folder.name}」？`,
      content: `${count > 0 ? `将同时删除 ${count} 个子文件夹。` : ""}其中文献、附件和笔记仍保留在“全部”中。`,
      okText: "删除文件夹", cancelText: "取消", okButtonProps: { danger: true },
      onOk: async () => {
        const latest = modernDataRef.current;
        const removed = descendantFolderIds(latest.folders, folder.id);
        await persistModern(deleteFolderSubtree(latest, folder.id));
        if (!isCurrentScope()) return;
        if (folderId && removed.has(folderId)) selectView(null);
        message.success("已删除文件夹，文献已保留");
      },
    });
  };

  const openImport = (method: ImportMethod) => {
    if (disabled || busyRef.current) return;
    clearImportResult();
    setImportMethod(method);
  };

  const dropPapers = async (ids: string[]) => {
    if (disabled || busyRef.current) return;
    const current = modernDataRef.current;
    const removed = current.papers.filter((paper) => ids.includes(paper.id) && paper.academicWorkId);
    const workIds = new Set(removed.map((paper) => paper.academicWorkId!));
    try {
      await persistModern({
        ...current,
        papers: current.papers.filter((paper) => !ids.includes(paper.id)),
        hiddenArxivWorkIds: [...new Set([...(current.hiddenArxivWorkIds || []), ...workIds])],
        hiddenArxivPapers: [...(current.hiddenArxivPapers || []).filter((paper) => !workIds.has(paper.academicWorkId || "")), ...removed],
      });
    } catch (error) {
      message.error(error instanceof Error ? error.message : "移除文献失败，请重试");
      return;
    }
    setSelected((currentSelected) => currentSelected.filter((id) => !ids.includes(id)));
    if (editing && ids.includes(editing.id)) {
      setEditing(null);
      setSheet(null);
    }
  };

  const placePapers = async (ids: string[], folder: string) => {
    if (disabled || busyRef.current) return false;
    const current = modernDataRef.current;
    const target = folder === "none" ? null : folder;
    if (target && !current.folders.some((item) => item.id === target)) {
      message.error("目标文件夹已不存在，请重新选择");
      return false;
    }
    const moving = current.papers.filter((item) => ids.includes(item.id) && (item.folderId !== target || item.collectionIds.some((id) => id !== target))).map((item) => item.id);
    if (!moving.length) return false;
    try {
      await persistModern({ ...current, collections: syncFolderCollections(current), papers: movePaperRecords(current.papers, moving, target) });
      if (!isCurrentScope()) return false;
      expandFolderPath(target);
      setSelected((current) => current.filter((id) => !moving.includes(id)));
      message.success(`已将 ${moving.length} 篇文献移至${target ? `「${folderPath(current.folders, target)}」` : "未分组"}`);
      return true;
    } catch (error) {
      message.error(error instanceof Error ? error.message : "移动文献失败，请重试");
      return false;
    }
  };

  const endPaperDrag = () => {
    paperDrag.current = null;
    setDraggedIds([]);
    setDropFolderId(null);
  };
  const startPaperDrag = (event: DragEvent<HTMLButtonElement>, item: PaperItem) => {
    if (disabled || busyRef.current) { event.preventDefault(); return; }
    const ids = selected.includes(item.id) ? selectedPapers.map((paper) => paper.id) : [item.id];
    paperDrag.current = { datasetId, ids };
    event.dataTransfer.setData(PAPER_DRAG_TYPE, JSON.stringify(paperDrag.current));
    event.dataTransfer.effectAllowed = "move";
    const row = event.currentTarget.closest("tr");
    if (row && event.dataTransfer.setDragImage) event.dataTransfer.setDragImage(row, 32, 24);
    setDraggedIds(ids);
  };
  const canDropPapers = (event: DragEvent<HTMLButtonElement>, folder: string) => {
    const drag = paperDrag.current;
    return !disabled && !busyRef.current && drag?.datasetId === datasetId && Array.from(event.dataTransfer.types).includes(PAPER_DRAG_TYPE)
      && data.folders.some((item) => item.id === folder)
      && modernData.papers.some((item) => drag.ids.includes(item.id) && (item.folderId !== folder || item.collectionIds.some((id) => id !== folder)));
  };
  const receivePapers = (event: DragEvent<HTMLButtonElement>, folder: string) => {
    if (!canDropPapers(event, folder)) { endPaperDrag(); return; }
    event.preventDefault();
    try {
      const payload = JSON.parse(event.dataTransfer.getData(PAPER_DRAG_TYPE));
      const drag = paperDrag.current;
      if (payload?.datasetId !== datasetId || !Array.isArray(payload.ids) || !drag
        || payload.ids.length !== drag.ids.length || !payload.ids.every((id: unknown, index: number) => id === drag.ids[index])) return;
      placePapers(drag.ids, folder);
    } catch {
      message.error("无法读取拖动的文献，请重新拖拽");
    } finally {
      endPaperDrag();
    }
  };

  const saveEdits = async (edits: PaperEdits) => {
    if (disabled || busyRef.current || !editing) return;
    if (editingRevisionRef.current !== revisionRef.current) {
      setEditError("文献库已更新。为避免覆盖其他修改，请保留当前草稿并重新打开编辑。");
      return;
    }
    setEditError(null);
    const latest = modernDataRef.current.papers.find((paper) => paper.id === editing.id);
    if (!latest) { setEditError("文献已被移除，请重新加载后编辑"); return; }
    const next = applyPaperEdits(latest, edits);
    const clash = duplicateOf(data.papers.filter((item) => item.id !== editing.id), next);
    if (clash) {
      message.info(`库中已有：${clash.title}`);
      return;
    }
    try {
      await persist({ ...data, papers: data.papers.map((item) => item.id === editing.id ? next : item) });
      message.success(next.incomplete ? "题录仍待补全" : "题录已保存");
      setEditing(next);
      setSheet(null);
    } catch (error) { setEditError(error instanceof Error ? error.message : "文献保存失败，请重试"); }
  };

  const folderOptions = [{ value: "none", label: "未分组" }, ...flattenFolderTree(data.folders).map(({ folder }) => ({ value: folder.id, label: folderPath(data.folders, folder.id) }))];
  const folderRows = useMemo(() => flattenFolderTree(data.folders, collapsedFolders), [data.folders, collapsedFolders]);
  const forbiddenParents = editingFolderId ? descendantFolderIds(data.folders, editingFolderId) : new Set<string>();
  const folderParentOptions = [{ value: "", label: "顶层文件夹" }, ...folderOptions.filter((option) => option.value !== "none" && !forbiddenParents.has(option.value))];

  const importBibliography = () => {
    if (disabled || busyRef.current) return;
    clearImportResult();
    try {
      const parsed = format === "ris"
        ? parseRIS(bibliography, bibliographyKind)
        : format === "csv"
          ? parseCSV(bibliography, bibliographyKind)
          : { papers: parseBibTeX(bibliography, bibliographyKind), folderNames: [] as Array<string | null> };
      const papers = "papers" in parsed ? parsed.papers : parsed;
      const folderNames = "folderNames" in parsed ? parsed.folderNames : undefined;
      stage(papers, folderNames);
    } catch (error) {
      showImportError(error, "题录解析失败，请检查内容和格式");
    }
  };

  const importIdentifier = async () => {
    if (disabled || busyRef.current || searching) return;
    clearImportResult();
    const kind = classifyIdentifier(identifier);
    const arxivInput = isArxivInput(identifier);
    if (!kind && !arxivInput) {
      showImportError(null, "请粘贴有效的 DOI、arXiv 编号或链接");
      return;
    }
    if (!arxivInput && kind?.kind !== "arxiv" && kind) {
      const probe: PaperItem = {
        id: "probe",
        title: kind.value,
        authors: "",
        venue: "",
        year: 0,
        folderId: null,
        status: "unread",
        important: false,
        tags: [],
        source: kind.kind,
        parse: "ready",
        doi: kind.kind === "doi" ? kind.value : undefined,
      };
      const existing = duplicateOf(data.papers, probe);
      if (existing) {
        setPending({ fresh: [], skipped: [existing], folders: data.folders });
        message.info("库中已有，跳过 1 篇");
        return;
      }
    }
    setSearching(true);
    try {
      const item = await lookupIdentifier(identifier);
      if (!isCurrentScope()) return;
      if (item.source === "arxiv") setPendingArxiv(item);
      else stage([item]);
    } catch (error) {
      if (isCurrentScope()) showImportError(error, "没有查到这篇论文");
    } finally {
      if (isCurrentScope()) setSearching(false);
    }
  };

  const commitArxiv = async () => {
    if (disabled || busyRef.current || !pendingArxiv || searching || isDemo) return;
    setImportError(null);
    setSearching(true);
    try {
      const imported = await importArxivPaper(`arXiv:${pendingArxiv.arxiv}${pendingArxiv.arxivVersion || ""}`, datasetId);
      if (!isCurrentScope()) return;
      const current = modernDataRef.current;
      const targetFolder = current.folders.some((folder) => folder.id === folderId) ? folderId : null;
      await persistModern(mergeArxivImports(current, [imported], targetFolder, true));
      if (!isCurrentScope()) return;
      if (imported.processing_status === "failed") throw new Error(imported.processing_error || "PDF 已保存，但正文解析失败");
      message.success(imported.status === "already_present" ? "已复用该版本 PDF" : imported.processing_status === "parsing" ? "文献与 PDF 已保存，正文解析中" : "文献与 PDF 已保存");
      clearImportResult();
      setImportMethod(null);
    } catch (error) {
      if (isCurrentScope()) showImportError(error, "arXiv 导入失败，请重试");
    } finally {
      if (isCurrentScope()) {
        setSyncRevision((value) => value + 1);
        setSearching(false);
      }
    }
  };

  const importFile = async (file: File) => {
    if (disabled || busyRef.current || searching) return;
    clearImportResult();
    const name = file.name.toLowerCase();
    setSearching(true);
    try {
      if (name.endsWith(".pdf")) {
        const item = await paperFromPdf(file);
        if (!isCurrentScope()) return;
        const current = asLegacyLibrary(modernDataRef.current);
        const duplicate = duplicateOf(current.papers, item);
        if (duplicate) setPending({ fresh: [], skipped: [], folders: current.folders, baseFolderIds: current.folders.map((folder) => folder.id), attachTo: duplicate });
        else stage([item]);
        setPendingPdf({ paperId: duplicate?.id || item.id, file });
        setPendingAttachments([createAttachment({ paperId: duplicate?.id || item.id, name: file.name, kind: "pdf", mimeType: "application/pdf", size: file.size, source: "local", isPrimary: true })]);
        if (item.incomplete) message.warning("题录不完整，已标成待补全");
        return;
      }
      const text = await file.text();
      if (!isCurrentScope()) return;
      if (name.endsWith(".ris")) stage(parseRIS(text, "local"));
      else if (name.endsWith(".csv")) {
        const parsed = parseCSV(text);
        stage(parsed.papers, parsed.folderNames);
      } else if (name.endsWith(".bib")) stage(parseBibTeX(text, "local"));
      else throw new Error("请选择 PDF、BibTeX、RIS 或 CSV 文件");
    } catch (error) {
      if (isCurrentScope()) {
        setPending(null);
        setPendingAttachments([]);
        showImportError(error, "文件没有读出来");
      }
    } finally {
      if (isCurrentScope()) setSearching(false);
    }
  };

  const openDocument = (documentId: string) => window.open(`/lib/knowledge/knowledge/${encodeURIComponent(datasetId)}/${encodeURIComponent(documentId)}`, "_blank", "noopener");
  const paperReadingUrl = (paperId: string): string | undefined => {
    const paper = modernDataRef.current.papers.find((item) => item.id === paperId);
    const attachments = modernDataRef.current.attachments.filter((item) => item.paperId === paperId && paper?.attachmentIds.includes(item.id) && isReadablePdfAttachment(item));
    const attachment = attachments.find((item) => item.isPrimary) || attachments[0];
    if (attachment?.contentRef?.trim()) return `/lib/knowledge/knowledge/${encodeURIComponent(datasetId)}/${encodeURIComponent(attachment.contentRef.trim())}`;
    return attachment?.sourceUrl;
  };

  return (
    <DndContext sensors={dragSensors} collisionDetection={pointerWithin}
      accessibility={{
        screenReaderInstructions: { draggable: "拖到左侧文件夹以移动文献，也可按回车选择目标文件夹。" },
        announcements: {
          onDragStart: () => "已开始拖动文献，请移到左侧文件夹。",
          onDragOver: ({ over }: DragOverEvent) => over ? `目标文件夹：${data.folders.find((folder) => folder.id === over.id)?.name || ""}` : "请移到左侧文件夹。",
          onDragEnd: () => "拖动结束。",
          onDragCancel: () => "已取消拖动。",
        },
      }}
      onDragStart={({ active }: DragStartEvent) => {
        if (disabled || busyRef.current) return;
        const item = data.papers.find((paper) => paper.id === active.id);
        if (!item) return;
        const ids = selected.includes(item.id) ? selectedPapers.map((paper) => paper.id) : [item.id];
        paperDrag.current = { datasetId, ids };
        setDraggedIds(ids);
      }}
      onDragEnd={({ over }: DragEndEvent) => {
        const drag = paperDrag.current;
        if (drag?.datasetId === datasetId && over && data.folders.some((folder) => folder.id === over.id)) placePapers(drag.ids, String(over.id));
        endPaperDrag();
      }}
      onDragCancel={endPaperDrag}>
    <section className="paper-library">
      <header className="paper-library-head">
        <div className="paper-library-heading">
          {onBack && <Tooltip title="返回知识库"><Button className="paper-library-back" type="text" icon={<ArrowLeftOutlined />} aria-label="返回知识库" onClick={onBack} /></Tooltip>}
          <h1 title={title}>{title}</h1>
        </div>
        <div className="paper-library-tools" role="toolbar" aria-label="文献工具">
          <PaperSearch open={activeTool === "search"} value={keyword} onChange={setKeyword}
            onOpenChange={setSearchOpen} />
          <Popover trigger="click" placement="bottomRight" align={{ overflow: { adjustX: true, adjustY: true, shiftX: true, shiftY: true } }} arrow={false} destroyOnHidden overlayClassName="paper-library-tool-popover"
            open={activeTool === "filter"} onOpenChange={(open: boolean) => setActiveTool((current) => open ? "filter" : current === "filter" ? null : current)}
            content={<div role="dialog" aria-label="筛选文献" onKeyDown={(event) => { if (event.key === "Escape") setActiveTool(null); }}>
              <PaperFilterPanel papers={data.papers} value={filters} onChange={setFilters} />
            </div>}>
            <Button type="text" className={`paper-library-tool ${hasFilters ? "is-active" : ""}`} icon={<FilterOutlined />} aria-label="筛选" aria-expanded={activeTool === "filter"}>筛选{hasFilters && <span className="paper-library-tool-dot" aria-hidden="true" />}</Button>
          </Popover>
          <Dropdown trigger={["click"]} placement="bottomRight" onOpenChange={(open: boolean) => { if (open) setActiveTool(null); }} menu={{ items: [{
            type: "group", label: `${selectedPapers.length ? "已选文献" : "当前结果"} · ${exportPapers.length} 篇`,
            children: (["bib", "ris", "csv"] as const).map((exportFormat) => ({ key: exportFormat, label: exportFormat === "bib" ? "BibTeX" : exportFormat.toUpperCase(),
              onClick: () => downloadPaperCitations({ papers: exportPapers, folders: data.folders }, exportFormat, title),
            })),
          }] }}>
            <Button type="text" className="paper-library-tool" icon={<ExportOutlined />} aria-label="导出" disabled={!exportPapers.length}>导出</Button>
          </Dropdown>
        </div>
      </header>
      {loading && <p role="status">正在加载已保存的文献…</p>}
      {persistenceError && <Alert className="paper-library-sync-error" type="error" showIcon message="文献保存或加载失败" description={persistenceError} action={<Button size="small" disabled={loading || saving} onClick={() => void reload()}>重新加载</Button>} />}
      {!loading && !canWrite && !persistenceError && <Alert type="info" showIcon message="当前文献库为只读，无法编辑" />}
      {syncError && <Alert className="paper-library-sync-error" type="error" showIcon message="文献同步失败" description={syncError} action={<Button size="small" disabled={loading || saving} onClick={() => setSyncRevision((value) => value + 1)}>重新同步</Button>} />}
      <div className="paper-library-layout">
        <nav className="paper-library-rail" aria-label="文献浏览">
          <Dropdown trigger={["click"]} placement="bottomLeft" menu={{ items: [
            { key: "upload", icon: <UploadOutlined />, label: "直接上传", onClick: () => openImport("upload") },
            { key: "identifier", icon: <LinkOutlined />, label: "arXiv / DOI 链接", onClick: () => openImport("identifier") },
            { key: "bibliography", icon: <FileTextOutlined />, label: "BibTeX / RIS / CSV", onClick: () => openImport("bibliography") },
            { key: "zotero", icon: <FolderOutlined />, label: "从 Zotero 导入", disabled: isDemo, onClick: () => setZoteroOpen(true) },
            { type: "divider" },
            { key: "folder", icon: <PlusOutlined />, label: "新建文件夹", onClick: () => openFolderDialog() },
          ] }}>
            <Button className="paper-library-import-button" aria-label="新建文献或文件夹" disabled={disabled} type="primary" icon={<PlusOutlined />}>新建<DownOutlined /></Button>
          </Dropdown>
          <div className="paper-library-browse-head">
            <span className="paper-library-rail-label">浏览</span>
            <div className="paper-library-folder-tools">
              <Tooltip title="新建文件夹"><Button type="text" size="small" icon={<PlusOutlined />} aria-label="新建文件夹" disabled={disabled} onClick={() => openFolderDialog()} /></Tooltip>
              <Tooltip title={currentFolder ? "删除当前文件夹" : "选择文件夹后删除"}><span><Button type="text" size="small" icon={<DeleteOutlined />} aria-label="删除文件夹" disabled={disabled || !currentFolder} onClick={() => deleteFolder()} /></span></Tooltip>
            </div>
          </div>
          <FolderButton icon={<FileTextOutlined />} name="全部" count={data.papers.length} current={!folderId && libraryView === "all"} onClick={() => selectView(null)} />
          <FolderButton icon={<StarOutlined />} name="收藏" count={stats.important} current={libraryView === "important"} onClick={() => selectView(null, "important")} />
          <div className="paper-library-folder-tree" aria-label="文件夹">
          {folderRows.map(({ folder, depth, hasChildren }) => {
            const count = papersInView(modernData, folder.id).length;
            return (
              <FolderButton
                key={folder.id}
                name={folder.name}
                path={folderPath(data.folders, folder.id)}
                depth={depth}
                hasChildren={hasChildren}
                expanded={!collapsedFolders.has(folder.id)}
                onToggle={() => setCollapsedFolders((current) => { const next = new Set(current); if (next.has(folder.id)) next.delete(folder.id); else next.add(folder.id); return next; })}
                actions={[
                  { key: "child", label: "新建子文件夹", icon: <PlusOutlined />, disabled, onClick: () => openFolderDialog(undefined, folder.id) },
                  { key: "edit", label: "重命名 / 移动", icon: <EditOutlined />, disabled, onClick: () => openFolderDialog(folder) },
                  { type: "divider" },
                  { key: "delete", label: "删除文件夹", icon: <DeleteOutlined />, disabled, danger: true, onClick: () => deleteFolder(folder) },
                ]}
                icon={<FolderOutlined />}
                count={count}
                current={folderId === folder.id}
                onClick={() => selectView(folder.id)}
                dropId={folder.id}
                acceptsDrop={!disabled && modernData.papers.some((item) => draggedIds.includes(item.id) && (item.folderId !== folder.id || item.collectionIds.some((id) => id !== folder.id)))}
                dropTarget={dropFolderId === folder.id}
                onDragOver={(event) => {
                  if (!canDropPapers(event, folder.id)) return;
                  event.preventDefault();
                  event.dataTransfer.dropEffect = "move";
                  setDropFolderId(folder.id);
                }}
                onDragLeave={(event) => {
                  if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropFolderId(null);
                }}
                onDrop={(event) => receivePapers(event, folder.id)}
              />
            );
          })}
          </div>
          {!data.folders.length && <p className="paper-library-folder-hint">用文件夹整理你的文献</p>}
          {!!draggedIds.length && <p className="paper-library-drag-hint" role="status">移动 {draggedIds.length} 篇文献<br />拖到目标文件夹</p>}
        </nav>
        <div className="paper-library-board">
          {selectedPapers.length > 0 && (
        <div className="paper-library-selection-bar" role="region" aria-label="已选论文">
          <div className="paper-library-selection-summary"><Button type="text" size="small" aria-label="取消选择" icon={<CloseOutlined />} onClick={() => setSelected([])} /><span>已选 <strong>{selectedPapers.length}</strong> 篇</span></div>
          <Dropdown trigger={["click"]} menu={{ items: folderOptions.map((folder) => ({ key: folder.value, label: folder.label, onClick: () => placePapers(selected, folder.value) })) }}><Button type="text" size="small" disabled={disabled} icon={<FolderOutlined />}>移动到<DownOutlined /></Button></Dropdown>
          <Button type="text" size="small" disabled={disabled} danger icon={<DeleteOutlined />} onClick={() => Modal.confirm({ title: `移除已选的 ${selectedPapers.length} 篇题录？`, okText: "移除", cancelText: "取消", onOk: () => dropPapers(selected) })}>移除</Button>
          <Dropdown menu={{ items: [
            { key: "export-selected", label: "导出已选", children: (["bib", "ris", "csv"] as const).map((exportFormat) => ({
              key: `export-selected-${exportFormat}`,
              label: exportFormat === "bib" ? "BibTeX" : exportFormat.toUpperCase(),
              onClick: () => downloadPaperCitations({ papers: selectedPapers, folders: data.folders }, exportFormat, `${title}-已选文献`),
            })) },
            { key: "gbt", label: "复制国标", onClick: () => copyText(selectedPapers.map((item) => formatCitation(item, "gbt")).join("\n"), "国标引用") },
            { key: "review", label: `生成综述（${readyCount}）`, disabled: readyCount < 2, onClick: () => setSheet("review") },
            { key: "expand", label: `自动拓展（${readyCount}）`, disabled: readyCount < 1, onClick: () => message.info("自动拓展会带着这些种子论文进入研究流程") },
          ] }}>
            <Button type="text" size="small">更多<DownOutlined /></Button>
          </Dropdown>
          </div>
          )}
          <div className="paper-library-table-scroll" tabIndex={0} aria-label="论文列表，可横向滚动">
          <table className="paper-library-table" aria-label="论文列表">
            <colgroup><col className="paper-library-controls-col" /><col /><col className="paper-library-year-col" /><col className="paper-library-venue-col" /><col className="paper-library-citations-col" /><col className="paper-library-actions-col" /></colgroup>
            <thead>
              <tr>
                <th scope="col" className={`paper-library-selection-head ${selectedVisibleCount ? "has-selection" : ""}`}>
                  <input type="checkbox" className="paper-library-checkbox" aria-label="选择当前列表全部论文" disabled={!visible.length}
                    checked={visible.length > 0 && selectedVisibleCount === visible.length}
                    ref={(element) => { if (element) element.indeterminate = selectedVisibleCount > 0 && selectedVisibleCount < visible.length; }}
                    onChange={(event) => setSelected(event.target.checked ? [...new Set([...selected, ...visible.map((item) => item.id)])] : selected.filter((id) => !visible.some((item) => item.id === id)))} />
                </th>
                <th scope="col">标题</th><th scope="col">年份</th><th scope="col">刊物</th><th scope="col" className="paper-library-citations">引用量</th><th scope="col"><span className="paper-library-visually-hidden">文献操作</span></th>
              </tr>
            </thead>
            <tbody>
          {!visible.length && <tr><td colSpan={6}><div className="paper-library-empty">没有符合条件的论文。可以导入 DOI、arXiv、BibTeX、RIS、CSV，或上传 PDF。</div></td></tr>}
          {visible.map((item) => (
            <tr className={`paper-library-row ${selected.includes(item.id) ? "selected" : ""} ${draggedIds.includes(item.id) ? "is-dragging" : ""}`} key={item.id}>
              <td className="paper-library-controls-cell"><div className="paper-library-row-controls">
              <input
                type="checkbox"
                className="paper-library-checkbox"
                aria-label={`选择 ${item.title}`}
                checked={selected.includes(item.id)}
                onChange={(event: ChangeEvent<HTMLInputElement>) => setSelected(event.target.checked ? [...selected, item.id] : selected.filter((id) => id !== item.id))}
              />
              <PaperDragHandle disabled={disabled} item={item} folders={folderOptions} onMove={(folder) => placePapers(selected.includes(item.id) ? selected : [item.id], folder)} onDragStart={(event) => startPaperDrag(event, item)} onDragEnd={endPaperDrag} />
              </div></td>
              <td className="paper-library-main">
                  <div className="paper-library-title-line"><h2 className="paper-library-title"><a className="paper-library-title-button" href={paperReadingUrl(item.id) || `#paper-${item.id}`} onClick={(event) => { if (!paperReadingUrl(item.id)) { event.preventDefault(); setDetailPaperId(item.id); } }}>{item.title}</a></h2>
                    <button
                      type="button"
                      className={`paper-library-star ${item.important ? "on" : ""}`}
                      disabled={disabled}
                      aria-pressed={item.important}
                      aria-label={item.important ? "取消收藏" : "收藏文献"}
                      onClick={() => void updatePaper(item.id, { important: !item.important })}
                    >{item.important ? <StarFilled /> : <StarOutlined />}</button>
                  </div>
                <p className="paper-library-cite">{item.authors || "作者待补全"}</p>
                <div className="paper-library-meta">
                  {item.tags.map((tag) => (
                    <span key={tag} className="paper-library-tag">{tag}</span>
                  ))}
                  <span className="paper-library-source">{sourceLabel[item.source]}</span>
                  {item.incomplete && <span className="paper-library-warn">待补全</span>}
                  {item.parse === "failed" && <Tooltip title={item.processingError || "正文解析失败，请检查导入任务"}><span className="paper-library-warn">解析失败</span></Tooltip>}
                  {item.parse === "parsing" && <span className="paper-library-source">解析中</span>}
                  {!!materialByPaper.get(item.id)?.attachments && <span className="paper-library-material">{materialByPaper.get(item.id)?.attachments} 个附件</span>}
                  {!!materialByPaper.get(item.id)?.notes && <span className="paper-library-material">{materialByPaper.get(item.id)?.notes} 条笔记</span>}
                </div>
              </td>
              <td className="paper-library-year">{item.year || "—"}</td>
              <td className="paper-library-venue"><span title={venueName(item)}>{item.venue?.trim() ? venueName(item) : "—"}</span></td>
              <td className="paper-library-citations" title={item.citationCount == null ? "暂无引用量数据" : undefined}>{typeof item.citationCount === "number" && Number.isFinite(item.citationCount) && item.citationCount >= 0 ? item.citationCount.toLocaleString("en-US") : "—"}</td>
              <td><div className="paper-library-row-actions">
                    <Select
                      className="paper-library-status"
                      size="small"
                      value={item.status}
                      disabled={disabled}
                      aria-label={`${item.title} 的阅读状态`}
                      options={(Object.keys(statusLabel) as ReadingStatus[]).map((key) => ({ value: key, label: statusLabel[key] }))}
                      onChange={(value: ReadingStatus) => void updatePaper(item.id, { status: value })}
                    />
                    <Tooltip title="文献详情"><button type="button" className="paper-library-info-action" aria-label={`${item.title} 的文献详情`} onClick={() => setDetailPaperId(item.id)}><InfoCircleOutlined /></button></Tooltip>
                  <Dropdown menu={{ items: [
                    ...(Object.keys(citationLabel) as CitationStyle[]).map((style) => ({
                      key: style,
                      label: citationLabel[style],
                      onClick: () => copyText(formatCitation(item, style), citationLabel[style]),
                    })),
                    { key: "link", label: "原文", disabled: !paperLink(item), onClick: () => { const href = paperLink(item); if (href) window.open(href, "_blank", "noopener"); } },
                    { key: "edit", label: "编辑", disabled, onClick: () => { editingRevisionRef.current = revisionRef.current; setEditError(null); setEditing(item); setSheet("edit"); } },
                    { type: "divider" },
                    { key: "move", label: "移动到", disabled, children: folderOptions.map((option) => ({ key: option.value, label: option.label, onClick: () => placePapers([item.id], option.value) })) },
                    { key: "remove", label: "移除", disabled, danger: true, onClick: () => Modal.confirm({ title: "从论文集移除这篇题录？", okText: "移除", cancelText: "取消", onOk: () => dropPapers([item.id]) }) },
                  ] }}>
                    <button type="button" className="paper-library-more" aria-label={`${item.title} 的更多操作`}><EllipsisOutlined /></button>
                  </Dropdown>
              </div></td>
            </tr>
          ))}
            </tbody>
          </table>
          </div>
        </div>
      </div>
      <Modal title={editingFolderId ? "编辑文件夹" : "新建文件夹"} open={folderDialogOpen} onCancel={() => { if (!saving) setFolderDialogOpen(false); }} onOk={saveFolder} okText={editingFolderId ? "保存" : "创建"} cancelText="取消" confirmLoading={saving} okButtonProps={{ disabled: disabled || !folderName.trim() }} width={420} centered destroyOnHidden>
        {folderError && <Alert type="error" showIcon message={folderError} />}
        <label className="paper-library-folder-form">文件夹名称
          <Input autoFocus maxLength={80} value={folderName} placeholder="例如：长上下文研究" onChange={(event: ChangeEvent<HTMLInputElement>) => setFolderName(event.target.value)} onPressEnter={saveFolder} />
        </label>
        <label className="paper-library-folder-form">上级文件夹
          <Select aria-label="上级文件夹" showSearch optionFilterProp="label" value={folderParentId || ""} options={folderParentOptions} onChange={(id: string) => setFolderParentId(id || null)} />
        </label>
      </Modal>
      <ZoteroImportModal open={zoteroOpen} datasetId={datasetId} canWrite={canWrite} initialFolderId={folderId} onCancel={() => setZoteroOpen(false)} onImport={importZotero} />
      <Modal
        title={importMethod ? importTitles[importMethod] : "导入文献"}
        open={!!importMethod}
        onCancel={() => { if (!searching && !saving && !confirmingImport) { setImportMethod(null); clearImportResult(); } }}
        footer={null} width={560} centered destroyOnHidden
        className="paper-library-import-modal"
        closable={!searching && !saving && !confirmingImport} maskClosable={!searching && !saving && !confirmingImport} keyboard={!searching && !saving && !confirmingImport}
      >
        <div className="paper-library-import-content">
          {importError && <Alert type="error" showIcon message="导入未完成" description={importError} />}
          {importMethod === "upload" && <>
            <p>选择 PDF 或文献文件，先识别题录；确认加入论文库后才会上传 PDF。</p>
            <Upload.Dragger accept=".pdf,.bib,.ris,.csv" showUploadList={false} disabled={disabled || searching} beforeUpload={async (file: File) => { await importFile(file); return false; }}>
              <UploadOutlined className="paper-library-drop-icon" />
              <strong>{searching ? "正在识别文献…" : "点击选择，或拖拽文件到这里"}</strong>
              <span>PDF、BibTeX、RIS、CSV</span>
            </Upload.Dragger>
          </>}
          {importMethod === "identifier" && <>
            <p>粘贴 arXiv 链接、编号或 DOI，先确认文献信息再导入。</p>
            <label className="paper-library-import-label" htmlFor="paper-import-identifier">链接或标识符
              <Tooltip title={<div>支持 arXiv 编号、arXiv: 前缀、abs / pdf / html 链接、旧版分类编号，以及 arXiv DOI。系统从输入中提取编号，统一访问官方来源。v3 等版本号会保留；未指定版本时获取最新版。也支持普通 DOI 与 doi.org 链接。</div>}>
                <button type="button" className="paper-library-info" aria-label="支持的 DOI 与 arXiv 格式说明"><InfoCircleOutlined /></button>
              </Tooltip>
            </label>
            <Input id="paper-import-identifier" autoFocus size="large" value={identifier} placeholder="https://arxiv.org/abs/2404.06654" disabled={disabled || searching} onChange={(event: ChangeEvent<HTMLInputElement>) => { setIdentifier(event.target.value); clearImportResult(); }} onPressEnter={() => { if (!searching) void importIdentifier(); }} />
            <div className="paper-library-modal-actions"><Button type="primary" loading={searching} disabled={disabled || !identifier.trim()} onClick={importIdentifier}>查找文献</Button></div>
          </>}
          {importMethod === "bibliography" && <>
            <p>上传 BibTeX、RIS 或 CSV 文件，也可以直接粘贴题录内容。</p>
            <div className="paper-library-bibliography-toolbar">
              <Radio.Group value={format} disabled={disabled || searching} onChange={(event: RadioChangeEvent) => {
                const nextFormat = event.target.value;
                if (nextFormat === "bib" || nextFormat === "ris" || nextFormat === "csv") { setFormat(nextFormat); clearImportResult(); }
              }} optionType="button" buttonStyle="solid" options={[{ label: "BibTeX", value: "bib" }, { label: "RIS", value: "ris" }, { label: "CSV", value: "csv" }]} />
              <Button icon={<UploadOutlined />} disabled={disabled || searching} onClick={() => fileRef.current?.click()}>选择文件</Button>
              <input ref={fileRef} hidden type="file" accept=".bib,.ris,.csv" onChange={(event: ChangeEvent<HTMLInputElement>) => { const file = event.target.files?.[0]; event.target.value = ""; if (file) void importFile(file); }} />
            </div>
            <Input.TextArea aria-label="文献题录内容" rows={7} value={bibliography} disabled={disabled || searching} placeholder={format === "csv" ? "题名,作者,出处,年份,DOI,arXiv,标签,文件夹" : format === "bib" ? "@article{...}\n\n在这里粘贴 BibTeX 题录" : "TY  - JOUR\nTI  - ...\nER  -"} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => { setBibliography(event.target.value); clearImportResult(); }} />
            <div className="paper-library-modal-actions"><Button type="primary" disabled={disabled || searching || !bibliography.trim()} onClick={importBibliography}>识别题录</Button></div>
          </>}
          {pendingArxiv && <section className="paper-library-import-confirm" aria-label="arXiv 导入确认">
            <div className="paper-library-arxiv-source"><span>ARXIV</span><code>{pendingArxiv.arxiv}{pendingArxiv.arxivVersion}</code></div>
            <h3>{pendingArxiv.title}</h3>
            <p className="paper-library-arxiv-authors">{pendingArxiv.authors} · {pendingArxiv.year}</p>
            {pendingArxiv.abstract && <p className="paper-library-arxiv-abstract">{pendingArxiv.abstract}</p>}
            <p className="paper-library-arxiv-hint">保存文献信息并下载该版本 PDF。同一篇文献的不同版本会保留为独立附件。</p>
            {isDemo ? <Alert type="info" showIcon message="当前为演示页面，请进入论文知识库保存文献和 PDF" /> : <div className="paper-library-modal-actions"><Button type="primary" loading={searching || saving} disabled={disabled} onClick={commitArxiv}>{searching ? "正在下载并保存…" : "导入文献与 PDF"}</Button></div>}
          </section>}
          {pending && <section className="paper-library-import-confirm" aria-label="导入确认">
            <h3>{pending.attachTo ? "为已有文献添加 PDF" : pending.fresh.length ? `可导入 ${pending.fresh.length} 篇文献` : "文献已在库中"}</h3>
            <div className="paper-library-import-preview">
              {pending.attachTo && <p>{pending.attachTo.title}</p>}
              {pending.fresh.map((item) => <p key={item.id}>{item.title}{item.incomplete && <small>题录待补全</small>}</p>)}
              {pending.skipped.map((item, index) => <p key={`${item.id}-${index}`} className="paper-library-import-skipped">已跳过重复：{item.title}</p>)}
            </div>
            {(!!pending.fresh.length || pending.attachTo) && <div className="paper-library-modal-actions"><Button type="primary" loading={saving || confirmingImport} disabled={disabled || searching} onClick={commit}>{pending.attachTo ? "添加 PDF" : "加入论文库"}</Button></div>}
          </section>}
        </div>
      </Modal>
      {sheet && (
        <div className="paper-library-mask" onClick={(event) => { if (!saving && event.target === event.currentTarget) setSheet(null); }}>
          <aside className="paper-library-sheet" role="dialog" aria-modal="true" aria-label={sheet === "edit" ? "编辑题录" : "生成综述"}>
            <header className="paper-library-sheet-head">
              <h2>{sheet === "edit" ? "编辑题录" : "生成综述"}</h2>
              <Button type="text" disabled={saving} onClick={() => setSheet(null)}>关闭</Button>
            </header>
            {sheet === "edit" && editError && <Alert type="error" showIcon message="题录尚未保存" description={editError} />}
            {sheet === "edit" && editing && <EditForm key={editing.id} paper={editing} disabled={disabled} onSave={saveEdits} />}
            {sheet === "review" && (
              <div>
                <p>将把 {readyCount} 篇可阅读论文交给学术研究与论文写作。标为重要的论文会加重参考。</p>
                {selectedPapers.filter(usablePaper).map((item) => <p key={item.id}>{item.title}{item.important ? " · 重要" : ""}</p>)}
              </div>
            )}
          </aside>
        </div>
      )}
      {detailPaperId && modernData.papers.some((item) => item.id === detailPaperId) && (
        <PaperDetailDrawer
          paper={modernData.papers.find((item) => item.id === detailPaperId)!}
          data={modernData}
          onClose={() => setDetailPaperId(null)}
          onChange={persistModern}
          disabled={disabled}
          onOpenAttachment={openDocument}
        />
      )}
    </section>
    <DragOverlay dropAnimation={null}>{draggedIds.length > 0 && <div className="paper-library-drag-preview"><HolderOutlined /><span><strong>移动 {draggedIds.length} 篇文献</strong><small>{data.papers.find((paper) => paper.id === draggedIds[0])?.title}</small></span></div>}</DragOverlay>
    </DndContext>
  );
}

function PaperDragHandle({ item, folders, onMove, onDragStart, onDragEnd, disabled }: { disabled: boolean; item: PaperItem; folders: Array<{ value: string; label: string }>; onMove: (folder: string) => void; onDragStart: DragEventHandler<HTMLButtonElement>; onDragEnd: DragEventHandler<HTMLButtonElement> }) {
  const { attributes, listeners, setNodeRef } = useDraggable({ id: item.id, disabled });
  return <Dropdown trigger={["click"]} menu={{ items: folders.map((folder) => ({ key: folder.value, label: folder.label, onClick: () => onMove(folder.value) })) }}>
    <button {...attributes} {...listeners} ref={setNodeRef} type="button" className="paper-library-drag-handle" disabled={disabled} draggable={!disabled} aria-label={`拖动 ${item.title}`} title="拖到左侧文件夹；点击选择目标文件夹" onDragStart={onDragStart} onDragEnd={onDragEnd}><HolderOutlined /></button>
  </Dropdown>;
}

function FolderButton({ name, path, icon, count, current, onClick, dropTarget, dropId, acceptsDrop, onDragOver, onDragLeave, onDrop, depth, hasChildren, expanded, onToggle, actions }: {
  name: string; path?: string; icon: ReactNode; count: number; current: boolean; onClick: () => void;
  dropTarget?: boolean; dropId?: string; acceptsDrop?: boolean;
  onDragOver?: DragEventHandler<HTMLButtonElement>; onDragLeave?: DragEventHandler<HTMLButtonElement>; onDrop?: DragEventHandler<HTMLButtonElement>;
  depth?: number; hasChildren?: boolean; expanded?: boolean; onToggle?: () => void; actions?: MenuProps["items"];
}) {
  const { setNodeRef, isOver } = useDroppable({ id: dropId || `view:${name}`, disabled: !dropId || !acceptsDrop });
  return (
    <div className={`paper-library-folder ${current ? "current" : ""} ${depth !== undefined ? "is-tree" : ""}`} style={{ "--folder-depth": depth || 0 } as CSSProperties}>
      {depth !== undefined && (hasChildren
        ? <button className="paper-library-folder-toggle" type="button" aria-label={expanded ? "折叠子文件夹" : "展开子文件夹"} aria-expanded={expanded} onClick={onToggle}>{expanded ? <DownOutlined /> : <RightOutlined />}</button>
        : <span className="paper-library-folder-toggle-spacer" />)}
      <button ref={setNodeRef} type="button" className={`paper-library-folder-select ${dropTarget || isOver ? "is-drop-target" : ""}`} aria-current={current ? "true" : undefined} onClick={onClick} onDragOver={onDragOver} onDragLeave={onDragLeave} onDrop={onDrop}>
        <span className="paper-library-folder-name">{icon}<span title={path || name}>{name}</span></span><span className="paper-library-folder-count">{count}</span>
      </button>
      {actions && <Dropdown trigger={["click"]} menu={{ items: actions }}><button className="paper-library-folder-menu" type="button" aria-label="文件夹操作"><EllipsisOutlined /></button></Dropdown>}
    </div>
  );
}

function EditForm({ paper, onSave, disabled }: { disabled: boolean; paper: PaperItem; onSave: (edits: PaperEdits) => void }) {
  const [title, setTitle] = useState(paper.title);
  const [authors, setAuthors] = useState(paper.authors);
  const [venue, setVenue] = useState(paper.venue);
  const [year, setYear] = useState(String(paper.year));
  const [doi, setDoi] = useState(paper.doi || "");
  const [arxiv, setArxiv] = useState(paper.arxiv || "");
  const [tags, setTags] = useState(paper.tags.join(";"));
  return (
    <div className="paper-library-import">
      <label>题名<Input disabled={disabled} value={title} onChange={(event: ChangeEvent<HTMLInputElement>) => setTitle(event.target.value)} /></label>
      <label>作者<Input disabled={disabled} value={authors} onChange={(event: ChangeEvent<HTMLInputElement>) => setAuthors(event.target.value)} /></label>
      <label>出处<Input disabled={disabled} value={venue} onChange={(event: ChangeEvent<HTMLInputElement>) => setVenue(event.target.value)} /></label>
      <label>年份<Input disabled={disabled} value={year} onChange={(event: ChangeEvent<HTMLInputElement>) => setYear(event.target.value)} /></label>
      <label>DOI<Input disabled={disabled} value={doi} onChange={(event: ChangeEvent<HTMLInputElement>) => setDoi(event.target.value)} /></label>
      <label>arXiv<Input disabled={disabled} value={arxiv} onChange={(event: ChangeEvent<HTMLInputElement>) => setArxiv(event.target.value)} /></label>
      <label>标签<Input disabled={disabled} value={tags} placeholder="用分号分开" onChange={(event: ChangeEvent<HTMLInputElement>) => setTags(event.target.value)} /></label>
      <Button type="primary" disabled={disabled} onClick={() => onSave({
        title,
        authors,
        venue,
        year: Number(year),
        doi,
        arxiv,
        tags: tags.split(/[;；]/),
      })}>保存题录</Button>
    </div>
  );
}
