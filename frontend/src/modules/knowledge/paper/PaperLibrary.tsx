import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type ChangeEvent, type DragEvent, type DragEventHandler } from "react";
import { Alert, Button, Dropdown, Input, Modal, Popover, Radio, Select, Spin, Tooltip, Upload, message, type RadioChangeEvent } from "antd";
import { DndContext, DragOverlay, PointerSensor, pointerWithin, useDraggable, useDroppable, useSensor, useSensors, type DragEndEvent, type DragStartEvent, type DragOverEvent } from "@dnd-kit/core";
import { ArrowLeftOutlined, DownOutlined, FileTextOutlined, FolderOutlined, InfoCircleOutlined, LinkOutlined, PlusOutlined, DeleteOutlined, StarOutlined, CloseOutlined, UploadOutlined, FilterOutlined, ExportOutlined, HolderOutlined, EllipsisOutlined } from "@ant-design/icons";
import { v4 as uuidv4 } from "uuid";
import {
  applyPaperEdits,
  assignFolders,
  citationLabel,
  classifyIdentifier,
  duplicateOf,
  descendantFolderIds,
  paperFolderPath,
  filterPapers,
  filterLibraryView,
  formatCitation,
  movePapers,
  paperLink,
  venueName,
  parseBibTeX,
  parseCSV,
  parseRIS,
  planImport,
  safePaperUrl,
  statusLabel,
  type PaperEdits,
  type CitationStyle,
  type LibraryView,
  type PaperItem,
  type PaperLibraryData,
  type ReadingStatus,
} from "./model";
import { loadPersistedPaperLibrary, savePersistedPaperLibrary, setLibraryPaperImportance, type LibraryAttachmentContext, type PersistedPaperLibrary } from "./store";
import { asLegacyLibrary, emptyLibraryV3, createAttachment, type Attachment, type PaperLibraryDataV3 } from "./libraryModel";
import PaperDetailDrawer, { type PaperDetailTab } from "./PaperDetailDrawer";
import RestartKnowledgeModal, { type IRestartKnowledgeProps } from "../pages/detail/components/RestartKnowledgeModal";
import type { ProcessingLevel } from "../utils/processingLevel";
import type { ParserConfig } from "@/api/generated/knowledge-client";
import { paperFromPdf } from "./pdfMeta";
import { downloadPaperCitations } from "./exportLibrary";
import { isArxivInput } from "./arxivImport";
import { loadPaperLibrarySources } from './libraryLoader';
import PaperImportStatus from "./PaperImportStatus";
import { previewPaperLink, importPaperLink, retryAcademicImport, listAcademicImportBatches, emptyImportBatchPage, type ImportBatchPage, type ImportPreviewItem, type ImportOperationItem, type LibraryImportContext, type PaperUploadContext, isImportPreviewExpired } from '../api/academicReferences';

import PaperSearch from "./PaperSearch";
import PaperFilterPanel from "./PaperFilterPanel";
import { DEFAULT_PAPER_FILTERS, filterPaperRecords, hasPaperFilters, isReadablePdfAttachment, isStoredPdfAttachment, type PaperFilters } from "./paperFilters";
import PaperTasks from '../components/PaperTasks';
import PaperImportanceButton from '../components/PaperTasks/PaperImportanceButton';
import { updatePaperIdentity, type DocumentAcademic, type PaperTask } from '../api/paperWorkflows';
import { paperDocument, restoreLibraryPaper } from './libraryDocuments';
import PaperDirectoryTree, { PAPER_LIBRARY_ROOT } from './PaperDirectoryTree';
import PdfImportReview from './PdfImportReview';
import { pdfIdentityMatch, pdfTargetCandidates, type PdfPaper } from './pdfAssociation';
import { computeFileSha256 } from '../utils/fileHash';
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

export default function PaperLibrary({ datasetId, onUploadPdf, title = "论文库", onBack, processingLevel = "parsed", parsers, canParse = true }: Props) {
  const dragSensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));
  const datasetScopeRef = useRef({ datasetId, active: true });
  if (datasetScopeRef.current.datasetId !== datasetId) datasetScopeRef.current = { datasetId, active: true };
  const datasetScope = datasetScopeRef.current;
  const isCurrentScope = () => datasetScopeRef.current === datasetScope && datasetScope.active;
  const [modernData, setModernData] = useState<PaperLibraryDataV3>(emptyLibraryV3);
  const [paperImportance, setPaperImportance] = useState<Record<string, boolean>>({});
  const [academicStates, setAcademicStates] = useState<DocumentAcademic[]>([]);
  const [academicStatesReady, setAcademicStatesReady] = useState(false);
  const [paperTask, setPaperTask] = useState<PaperTask>();
  const [taskActionsContainer, setTaskActionsContainer] = useState<HTMLDivElement | null>(null);
  const data = useMemo(() => asLegacyLibrary({ ...modernData, papers: modernData.papers.map(paper => ({ ...paper, important: !!paperImportance[paper.id] })) }), [modernData, paperImportance]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [confirmingImport, setConfirmingImport] = useState(false);
  const [canWrite, setCanWrite] = useState(false);
  const [persistenceError, setPersistenceError] = useState<string | null>(null);
  const revisionRef = useRef(0);
  const loadedRef = useRef(false);
  const canWriteRef = useRef(false);
  const busyRef = useRef(false);
  const activeDatasetRef = useRef(datasetId);
  activeDatasetRef.current = datasetId;
  const disabled = loading || saving || confirmingImport || !canWrite;
  const modernDataRef = useRef(modernData);
  const syncGeneration = useRef(0);
  const [syncRevision, setSyncRevision] = useState(0);
  const [syncError, setSyncError] = useState<string | null>(null);
  const initialLoading = !academicStatesReady && !persistenceError && !syncError;
  const [linkPreview, setLinkPreview] = useState<ImportPreviewItem>();
  const [importPage, setImportPage] = useState<ImportBatchPage>(emptyImportBatchPage);
  const pendingImportKey = useRef(uuidv4());
  const bibliographyFile = useRef<string>();
  const linkRequestKey = useRef(uuidv4());
  const [pendingArxiv, setPendingArxiv] = useState<PaperItem | null>(null);
  const [detailPaperId, setDetailPaperId] = useState<string | null>(null);
  const [detailTab, setDetailTab] = useState<PaperDetailTab>("overview");
  const [detailEditing, setDetailEditing] = useState(false);
  const restartRef = useRef<IRestartKnowledgeProps>(null);
  const openDetail = (id: string, tab: PaperDetailTab = "overview") => { setDetailEditing(false); setDetailTab(tab); setDetailPaperId(id); };
  const [folderId, setFolderId] = useState<string | null>(null);
  const [libraryView, setLibraryView] = useState<LibraryView>("all");
  const [filters, setFilters] = useState<PaperFilters>(DEFAULT_PAPER_FILTERS);
  const [keyword, setKeyword] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [draggedIds, setDraggedIds] = useState<string[]>([]);
  const [dropFolderId, setDropFolderId] = useState<string | null>(null);
  const paperDrag = useRef<{ datasetId: string; ids: string[] } | null>(null);
  const [activeTool, setActiveTool] = useState<"search" | "filter" | "imports" | null>(null);
  const setSearchOpen = useCallback((open: boolean) => setActiveTool((current) => open ? "search" : current === "search" ? null : current), []);
  const [importMethod, setImportMethod] = useState<ImportMethod | null>(null);
  const [importTargetPaperId, setImportTargetPaperId] = useState<string | null>(null);
  const [folderDialogOpen, setFolderDialogOpen] = useState(false);
  const [editing, setEditing] = useState<PaperItem | null>(null);
  const editingRevisionRef = useRef<number | null>(null);
  const [editError, setEditError] = useState<string | null>(null);
  const [folderName, setFolderName] = useState("");
  const [identifier, setIdentifier] = useState("");
  const [bibliography, setBibliography] = useState("");
  const bibliographyKind = "local";
  const [format, setFormat] = useState<"bib" | "ris" | "csv">("bib");
  const [searching, setSearching] = useState(false);
  const [pending, setPending] = useState<{ fresh: PaperItem[]; skipped: PaperItem[]; folders: PaperLibraryData["folders"]; baseFolderIds?: string[]; attachTo?: PaperItem } | null>(null);
  const [pendingAttachments, setPendingAttachments] = useState<Attachment[]>([]);
  const [pendingPdf, setPendingPdf] = useState<{ paperId: string; file?: File; metadata: PdfPaper; contentHash: string } | null>(null);
  const [pdfConfirmed, setPdfConfirmed] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const applySnapshot = useCallback((next: PaperLibraryDataV3) => {
    modernDataRef.current = next;
    setModernData(next);

  }, []);

  const applySaved = useCallback((saved: PersistedPaperLibrary) => {
    revisionRef.current = saved.revision;
    canWriteRef.current = saved.canWrite;
    setCanWrite(saved.canWrite);
    setPaperImportance(saved.paperImportance || {});
    applySnapshot(saved.data);
  }, [applySnapshot]);

  const reload = useCallback(async () => {
    if (busyRef.current || datasetScopeRef.current !== datasetScope || !datasetScope.active) return;
    busyRef.current = true;
    const generation = ++syncGeneration.current;
    setLoading(true);
    setPersistenceError(null);
    try {
      const saved = await loadPersistedPaperLibrary(datasetId);
      if (generation !== syncGeneration.current || activeDatasetRef.current !== datasetId) return;
      loadedRef.current = true;
      applySaved(saved);
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
  }, [datasetId, datasetScope, applySaved]);

  useEffect(() => {
    datasetScope.active = true;
    syncGeneration.current += 1;
    busyRef.current = false;
    loadedRef.current = false;
    canWriteRef.current = false;
    setCanWrite(false);
    setSaving(false);
    setConfirmingImport(false);
    setPersistenceError(null);
    setSyncError(null);
    setSelected([]);
    setAcademicStates([]);
    setPaperImportance({});
    setAcademicStatesReady(false);
    setPaperTask(undefined);
    setDetailPaperId(null);
    setFolderId(null);
    setLibraryView("all");
    setImportTargetPaperId(null);
    setImportMethod(null);
    setSearching(false);
    setPending(null);
    setPendingArxiv(null); setLinkPreview(undefined);
    setPendingAttachments([]);
    setPendingPdf(null);
    applySnapshot(emptyLibraryV3());
    void reload();
    const onFocus = () => { void reload(); setSyncRevision(value => value + 1); };
    window.addEventListener("focus", onFocus);
    return () => {
      datasetScope.active = false;
      syncGeneration.current += 1;
      window.removeEventListener("focus", onFocus);
    };
  }, [datasetId, datasetScope, reload, applySnapshot]);

  const persistModern = useCallback(async (next: PaperLibraryDataV3, importContext?: LibraryImportContext, attachmentContext?: LibraryAttachmentContext) => {
    if (datasetScopeRef.current !== datasetScope || !datasetScope.active) throw new Error("文献库已切换，请重新打开后重试");
    if (busyRef.current || !loadedRef.current || !canWriteRef.current) throw new Error("文献库尚未就绪、正在保存或没有编辑权限，请稍后重试");
    busyRef.current = true;
    const generation = ++syncGeneration.current;
    setSaving(true);
    setPersistenceError(null);
    try {
      const saved = await savePersistedPaperLibrary(datasetId, next, revisionRef.current, importContext, attachmentContext);
      if (generation !== syncGeneration.current || activeDatasetRef.current !== datasetId) throw new Error("文献库已切换，请重新加载查看保存结果");
      applySaved(saved);
    } catch (error) {
      if (generation === syncGeneration.current && activeDatasetRef.current === datasetId) setPersistenceError(error instanceof Error ? error.message : "文献保存失败，请重试");
      throw error;
    } finally {
      if (generation === syncGeneration.current && activeDatasetRef.current === datasetId) {
        busyRef.current = false;
        setSaving(false);
      }
    }
  }, [datasetId, datasetScope, applySaved]);

  const persist = async (next: PaperLibraryData, attachments: Attachment[] = [], importContext?: LibraryImportContext, attachmentContext?: LibraryAttachmentContext) => {
    const current = modernDataRef.current;
    await persistModern({
      ...current,
      folders: next.folders,
      attachments: [...current.attachments.filter((item) => !attachments.some((attachment) => attachment.id === item.id)), ...attachments],
      papers: next.papers.map((item) => {
        const previous = current.papers.find((paper) => paper.id === item.id);
        const attachmentIds = attachments.filter((attachment) => attachment.paperId === item.id).map((attachment) => attachment.id);
        const fields = { ...item };
        if (previous && previous.abstract === undefined && item.abstract === previous.metadata.abstract) delete fields.abstract;
        return previous ? { ...previous, ...fields, important: previous.important, attachmentIds: [...new Set([...previous.attachmentIds, ...attachmentIds])], metadata: { ...previous.metadata, abstract: item.abstract, identifiers: { ...previous.metadata.identifiers, doi: item.doi, arxiv: item.arxiv } } } : { ...item, metadata: { abstract: item.abstract, identifiers: { doi: item.doi, arxiv: item.arxiv }, confidence: {} }, attachmentIds, noteIds: [], annotationIds: [], collectionIds: [], relatedPaperIds: [] };
      }),
    }, importContext, attachmentContext);
  };

  const updatePaper = async (id: string, changes: Partial<PaperItem>) => {
    try {
      const current = asLegacyLibrary(modernDataRef.current);
      await persist({ ...current, papers: current.papers.map((paper) => paper.id === id ? { ...paper, ...changes } : paper) });
    } catch { /* persistModern keeps the error visible and retains the last saved snapshot. */ }
  };

  useEffect(() => {
    if (!loadedRef.current) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const retry = () => { if (!cancelled) timer = setTimeout(sync, 12000); };
    const sync = async () => {
      if (busyRef.current) { retry(); return; }
      const generation = syncGeneration.current;
      try {
        const [source, queue] = await Promise.all([loadPaperLibrarySources(datasetId, modernDataRef.current), listAcademicImportBatches(datasetId)]);
        if (cancelled) return;
        if (generation !== syncGeneration.current || busyRef.current) { retry(); return; }
        setAcademicStates(source.states);
        setAcademicStatesReady(true);
        setImportPage(queue);
        applySaved(source);
        if (source.active || queue.notification.active > 0) retry();
        if (cancelled) return;
        setSyncError(null);
      } catch (error) {
        if (!cancelled) setSyncError(error instanceof Error ? error.message : "附件或导入任务同步失败，请重试");
      }
    };
    void sync();
    return () => { cancelled = true; if (timer) clearTimeout(timer); };
  }, [datasetId, syncRevision, applySaved]);

  const scoped = useMemo(() => libraryView === 'directory' ? data.papers.filter(paper => paper.folderId === folderId) : data.papers, [data, folderId, libraryView]);
  const viewed = useMemo(() => filterLibraryView(scoped, libraryView), [scoped, libraryView]);
  const visible = useMemo(
    () => filterPaperRecords(filterPapers(viewed, "all", [], "all", keyword), modernData.attachments.filter(attachment => modernData.papers.some(paper => paper.id === attachment.paperId && paper.attachmentIds.includes(attachment.id))), filters),
    [viewed, keyword, modernData.attachments, modernData.papers, filters],
  );
  const selectedPapers = data.papers.filter((item) => selected.includes(item.id));
  const selectedVisibleCount = visible.filter((item) => selected.includes(item.id)).length;
  const exportPapers = selectedPapers.length ? selectedPapers : visible;
  const hasFilters = hasPaperFilters(filters);
  const confirmedDocument = (id: string) => {
    const identity = paperDocument(modernData, id, academicStates);
    const paper = modernData.papers.find(item => item.id === id);
    if (paper?.associationConflict || (paper?.academicWorkId && identity?.state.paper_id && paper.academicWorkId !== identity.state.paper_id)) return undefined;
    return identity?.state.kind === 'paper' && identity.state.paper_id ? identity.state.document_id : undefined;
  };
  const selectedDocuments = selectedPapers.map(item => confirmedDocument(item.id)).filter((id): id is string => !!id);
  const hasStoredPdf = (id: string) => modernData.attachments.some(item => modernData.papers.find(paper => paper.id === id)?.attachmentIds.includes(item.id) && isStoredPdfAttachment(item));
  const selectionError = [
    selectedPapers.some(item => !hasStoredPdf(item.id)) ? '所选文献尚未关联入库 PDF，请先添加 PDF' : '',
    selectedPapers.some(item => hasStoredPdf(item.id) && !confirmedDocument(item.id)) ? '所选文献的论文身份尚未确认，请先识别或手动确认' : '',
  ].filter(Boolean).join('；') || undefined;
  const stats = useMemo(() => ({
    total: data.papers.length,
    important: data.papers.filter((item) => item.important).length,
  }), [data.papers]);
  const materialByPaper = useMemo(() => new Map(modernData.papers.map((paper) => {
    const attachments = modernData.attachments.filter(item => paper.attachmentIds.includes(item.id) && item.available !== false);
    return [paper.id, { pdfs: attachments.filter(isStoredPdfAttachment), links: attachments.filter(item => isReadablePdfAttachment(item) && !item.contentRef), supplements: attachments.filter(item => item.kind !== 'pdf'), attachments: attachments.length, notes: paper.noteIds.length }];
  })), [modernData]);

  const clearImportResult = () => {
    pendingImportKey.current = uuidv4();
    bibliographyFile.current = undefined;
    setImportError(null);
    setPendingArxiv(null); setLinkPreview(undefined);
    setPending(null);
    setPendingAttachments([]);
    setPendingPdf(null);
    setPdfConfirmed(false);
  };

  const pdfTarget = pending?.attachTo;
  const pdfConflict = !!pendingPdf && !!pdfTarget && pdfIdentityMatch(pdfTarget, pendingPdf.metadata) === 'conflict';
  const pdfRequiresConfirmation = !!pendingPdf && !!pdfTarget && pdfIdentityMatch(pdfTarget, pendingPdf.metadata) !== 'matched';
  const samePdfVersion = !!pendingPdf?.metadata.arxivVersion && !!pdfTarget && modernData.attachments.some(attachment => modernData.papers.find(paper => paper.id === pdfTarget.id)?.attachmentIds.includes(attachment.id) && attachment.sourceVersion === pendingPdf.metadata.arxivVersion && attachment.contentHash !== pendingPdf.contentHash);
  const pdfReviewComplete = !pdfConflict && (!(pdfRequiresConfirmation || samePdfVersion) || pdfConfirmed);
  const selectPdfTarget = (paperId: string | null) => {
    if (!pendingPdf) return;
    const current = asLegacyLibrary(modernDataRef.current);
    const target = current.papers.find(paper => paper.id === paperId);
    const candidate = { ...pendingPdf.metadata, folderId: libraryView === 'directory' ? folderId : null };
    setPending({ fresh: target ? [] : [candidate], skipped: [], folders: current.folders, baseFolderIds: current.folders.map(folder => folder.id), attachTo: target });
    setPendingPdf({ ...pendingPdf, paperId: target?.id || candidate.id });
    setPdfConfirmed(false);
    setImportError(null);
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
    if (disabled || busyRef.current || searching || !pending || (!pending.fresh.length && !pending.skipped.length && !pending.attachTo) || (pendingPdf && !pdfReviewComplete)) return;
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
      const incoming = pending.fresh.map((paper) => ({ ...paper, folderId: paper.folderId ? folderIds.get(paper.folderId) || null : null }));
      const planned = pendingPdf ? { fresh: incoming.filter(paper => !current.papers.some(existing => existing.id === paper.id)), skipped: [] as PaperItem[] } : planImport(current.papers, incoming);
      const pdfCandidate = pendingPdf && pending.fresh.find((paper) => paper.id === pendingPdf.paperId);
      const pdfTarget = pendingPdf ? pending.attachTo
        ? current.papers.find((paper) => paper.id === pending.attachTo!.id)
        : pdfCandidate && planned.fresh.find((paper) => paper.id === pdfCandidate.id)
        : undefined;
      if (pendingPdf && !pdfTarget) throw new Error("目标文献已被移除，请重新识别 PDF 后确认导入");
      if (pendingPdf && pdfTarget) {
        if (pdfIdentityMatch(pdfTarget, pendingPdf.metadata) === 'conflict') throw new Error('PDF 与目标论文身份冲突，未添加附件');
        const owner = pendingPdf.contentHash ? modernDataRef.current.papers.find(paper => paper.attachmentIds.some(id => modernDataRef.current.attachments.some(attachment => attachment.id === id && attachment.available !== false && attachment.contentHash === pendingPdf.contentHash))) : undefined;
        if (owner && !pendingAttachments.some(attachment => attachment.contentRef)) {
          if (owner.id !== pdfTarget.id) throw new Error(`这份 PDF 已关联《${owner.title}》，请核对归属`);
          message.info('这份 PDF 已存在，未重复添加');
          clearImportResult(); setImportMethod(null); openDetail(owner.id, 'attachments'); return;
        }
      }
      const importContext: LibraryImportContext | undefined = pendingPdf ? undefined : {
        idempotency_key: pendingImportKey.current, entry_type: 'bibliography', format, file_name: bibliographyFile.current,
        items: [...pending.fresh, ...pending.skipped].map(paper => ({ paper_id: (duplicateOf([...planned.fresh, ...current.papers], paper) || paper).id })),
      };
      if (planned.fresh.length || pdfTarget) {
        let attachments = pendingPdf ? pendingAttachments : pendingAttachments.filter((attachment) => planned.fresh.some((paper) => paper.id === attachment.paperId));
        if (pdfTarget) {
          const existing = modernDataRef.current.papers.find((paper) => paper.id === pdfTarget.id);
          const hasPdf = modernDataRef.current.attachments.some((attachment) => attachment.paperId === pdfTarget.id && existing?.attachmentIds.includes(attachment.id) && isStoredPdfAttachment(attachment));
          attachments = attachments.map((attachment) => {
            const saved = modernDataRef.current.attachments.find((item) => item.id === attachment.id && item.paperId === pdfTarget.id);
            return { ...attachment, paperId: pdfTarget.id, isPrimary: saved?.isPrimary ?? !hasPdf };
          });
        }
        if (pendingPdf && !attachments.some((attachment) => attachment.contentRef)) {
          if (!onUploadPdf) throw new Error("当前论文库无法上传 PDF，请刷新页面后重试");
          if (!pendingPdf.file) throw new Error('已上传的 PDF 不可用，请重新选择文件');
          const documentId = await onUploadPdf(pendingPdf.file, { idempotency_key: pendingImportKey.current, library_paper_id: pdfTarget!.id, title: pdfTarget!.title, paper: pdfTarget || undefined });
          if (!isCurrentScope()) return;
          if (typeof documentId !== "string" || !documentId.trim()) throw new Error("PDF 上传未返回有效文档 ID，请重试");
          attachments = attachments.map((attachment) => ({ ...attachment, contentRef: documentId.trim() }));
          // Keep the accepted upload for retry if the subsequent library save fails.
          setPendingAttachments(attachments);
        }
        busyRef.current = false;
        const attachmentContext: LibraryAttachmentContext | undefined = pendingPdf && pdfTarget && attachments[0]?.contentRef ? { operation: 'attach', paper_id: pdfTarget.id, document_id: attachments[0].contentRef, attachment_id: attachments[0].id, confirmed: true, idempotency_key: pendingImportKey.current } : undefined;
        await persist({ folders, papers: [...planned.fresh, ...current.papers] }, attachments, importContext, attachmentContext);
        setSyncRevision(value => value + 1);
        if (!isCurrentScope()) return;
        message.success(pdfTarget && !planned.fresh.length ? "已为文献添加 PDF" : `已加入 ${planned.fresh.length} 篇，跳过 ${pending.skipped.length + planned.skipped.length} 篇`);
      } else {
        busyRef.current = false;
        await persist({ folders, papers: current.papers }, [], importContext);
        setSyncRevision(value => value + 1);
        message.info(`库中已有，跳过 ${pending.skipped.length + planned.skipped.length} 篇`);
      }
      clearImportResult();
      setImportMethod(null);
    } catch (error) {
      if (isCurrentScope()) showImportError(error, "文献保存失败，请重试");
    } finally {
      if (isCurrentScope()) { busyRef.current = false; setConfirmingImport(false); }
    }
  };

  const addFolder = async () => {
    if (disabled || busyRef.current) return;
    const name = folderName.trim();
    if (!name) return;
    const parentId = libraryView === 'directory' ? folderId : null;
    if (data.folders.some((folder) => folder.name === name && (folder.parentId || null) === parentId)) {
      message.info("已有同名文件夹");
      return;
    }
    const folder = { id: `folder-${uuidv4()}`, name, parentId };
    try {
      await persist({ ...data, folders: [...data.folders, folder] });
      setFolderName("");
      setFolderDialogOpen(false);
      selectView(folder.id);
    } catch { /* Keep the folder draft and the persistence error visible. */ }
  };

  const selectView = (id: string | null, view: LibraryView = id ? 'directory' : 'all') => {
    setFolderId(id);
    setLibraryView(view);
    setSelected([]);
  };

  const currentFolder = data.folders.find((folder) => folder.id === folderId);
  const childFolders = libraryView === 'directory' ? data.folders.filter(folder => (folder.parentId || null) === folderId) : [];
  const descendantIds = folderId ? descendantFolderIds(data.folders, folderId) : new Set(data.folders.map(folder => folder.id));
  const directoryPaperCount = folderId ? data.papers.filter(paper => !!paper.folderId && descendantIds.has(paper.folderId)).length : data.papers.length;
  const deleteFolder = () => {
    if (disabled || busyRef.current || !currentFolder) return;
    Modal.confirm({
      title: `删除文件夹「${currentFolder.name}」？`,
      content: `删除此目录及 ${descendantIds.size - 1} 个子目录，${directoryPaperCount} 篇论文将移至${currentFolder.parentId ? '上级目录' : title}；PDF、笔记和星标保留。`,
      okText: "删除文件夹", cancelText: "取消", okButtonProps: { danger: true },
      onOk: async () => {
        const latest = modernDataRef.current;
        await persistModern({
          ...latest,
          folders: latest.folders.filter((folder) => !descendantIds.has(folder.id)),
          hiddenFolderIds: [...new Set([...(latest.hiddenFolderIds || []), ...descendantIds])],
          collections: latest.collections.filter((collection) => !descendantIds.has(collection.id)),
          papers: latest.papers.map((paper) => ({ ...paper, folderId: paper.folderId && descendantIds.has(paper.folderId) ? currentFolder.parentId || null : paper.folderId, collectionIds: paper.collectionIds.filter((id) => !descendantIds.has(id)) })),
        });
        selectView(currentFolder.parentId || null, 'directory');
        message.success("已删除文件夹，文献已保留");
      },
    });
  };

  const openImport = (method: ImportMethod, targetPaperId: string | null = null) => {
    if (disabled || busyRef.current) return;
    clearImportResult();
    setImportMethod(method);
    setImportTargetPaperId(targetPaperId);
  };

  const resumeAssociation = (item: ImportOperationItem) => {
    const documentId = item.document_id || item.current_document?.document_id;
    if (disabled || !documentId || !item.upload_request_key) return;
    clearImportResult();
    const current = asLegacyLibrary(modernDataRef.current);
    const existing = current.papers.find(paper => paper.id === item.library_paper_id);
    const metadata: PdfPaper = item.pending_paper?.id ? { ...item.pending_paper, identityVerified: false } : {
      id: item.library_paper_id || `document-${documentId}`, title: item.title, authors: '', venue: '', year: 0,
      folderId: null, status: 'unread', important: false, tags: [], source: 'local', importMethod: 'upload', parse: 'parsing', incomplete: true,
    };
    const restored = { ...metadata, folderId: metadata.folderId && current.folders.some(folder => folder.id === metadata.folderId) ? metadata.folderId : null };
    setPending({ fresh: existing ? [] : [restored], skipped: [], folders: current.folders, baseFolderIds: current.folders.map(folder => folder.id), attachTo: existing });
    setPendingPdf({ paperId: existing?.id || metadata.id, metadata, contentHash: item.content_sha256 || '' });
    setPendingAttachments([createAttachment({ paperId: existing?.id || metadata.id, name: `${item.title}.pdf`, kind: 'pdf', source: 'local', importMethod: 'upload', contentRef: documentId, contentHash: item.content_sha256, sourceVersion: item.source_version, isPrimary: true })]);
    pendingImportKey.current = item.upload_request_key;
    setImportMethod('upload');
    setImportTargetPaperId(existing?.id || null);
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
        hiddenDocumentIds: [...new Set([...(current.hiddenDocumentIds || []), ...current.attachments.filter(item => ids.includes(item.paperId)).flatMap(item => item.contentRef ? [item.contentRef] : [])])],
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
    }
  };

  const placePapers = async (ids: string[], folder: string) => {
    if (disabled || busyRef.current) return false;
    const target = folder === "none" ? null : folder;
    if (target && !data.folders.some((item) => item.id === target)) {
      message.error("目标文件夹已不存在，请重新选择");
      return false;
    }
    const moving = data.papers.filter((item) => ids.includes(item.id) && item.folderId !== target).map((item) => item.id);
    if (!moving.length) return false;
    try {
      await persist({ ...data, papers: movePapers(data.papers, moving, target) });
      setSelected((current) => current.filter((id) => !moving.includes(id)));
      message.success(`已将 ${moving.length} 篇文献移至${target ? `「${data.folders.find((item) => item.id === target)?.name}」` : "未分组"}`);
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
  const canDropPapers = (event: DragEvent<HTMLElement>, folder: string) => {
    const drag = paperDrag.current;
    return !disabled && !busyRef.current && drag?.datasetId === datasetId && Array.from(event.dataTransfer.types).includes(PAPER_DRAG_TYPE)
      && (folder === 'none' || data.folders.some((item) => item.id === folder))
      && data.papers.some((item) => drag.ids.includes(item.id) && item.folderId !== (folder === 'none' ? null : folder));
  };
  const receivePapers = (event: DragEvent<HTMLElement>, folder: string) => {
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
    if (disabled || busyRef.current || !editing) return false;
    if (editingRevisionRef.current !== revisionRef.current) {
      setEditError("文献库已更新。为避免覆盖其他修改，请保留当前草稿并重新打开编辑。");
      return false;
    }
    setEditError(null);
    const latest = modernDataRef.current.papers.find((paper) => paper.id === editing.id);
    if (!latest) { setEditError("文献已被移除，请重新加载后编辑"); return false; }
    const next = applyPaperEdits(latest, edits);
    const clash = duplicateOf(data.papers.filter((item) => item.id !== editing.id), next);
    if (clash) {
      message.info(`库中已有：${clash.title}`);
      return false;
    }
    try {
      const document = paperDocument(modernDataRef.current, editing.id, academicStates)?.state.document_id;
      if (document) {
        busyRef.current = true;
        syncGeneration.current += 1;
        setSaving(true);
        try {
          const identity = await updatePaperIdentity(document, { kind: 'paper', paper: {
            title: next.title, authors: next.authors.split(/[、;；]/).map(value => value.trim()).filter(Boolean),
            year: next.year, venue: next.venue, doi: next.doi, arxiv_id: next.arxiv, abstract: next.abstract,
          } });
          if (identity?.paper_id) next.academicWorkId = identity.paper_id;
        } finally {
          busyRef.current = false;
          setSaving(false);
        }
        await persist({ ...data, papers: data.papers.map(item => item.id === editing.id ? next : item) });
      } else {
      await persist({ ...data, papers: data.papers.map((item) => item.id === editing.id ? next : item) });
      }
      setSyncRevision(value => value + 1);
      message.success(next.incomplete ? "论文题名仍待补全" : "题录已保存");
      setEditing(next);
      return true;
    } catch (error) { setEditError(error instanceof Error ? error.message : "文献保存失败，请重试"); return false; }
  };

  const folderOptions = [{ value: "none", label: title }, ...data.folders.map((folder) => ({ value: folder.id, label: paperFolderPath(data.folders, folder.id) }))];

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
      stage(papers.map(item => ({ ...item, importMethod: "bibliography" })), folderNames);
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

    setSearching(true);
    try {
      const result = await previewPaperLink(identifier, datasetId);
      if (!isCurrentScope()) return;
      const paper = result.paper;
      const item: PaperItem = { id: `link-${result.work_id}`, title: paper?.title || result.title,
        authors: paper?.authors?.join('、') || '', year: paper?.year || 0, venue: paper?.venue || '',
        doi: result.doi, arxiv: result.arxiv_id, arxivVersion: result.fulltext_candidates[0]?.version,
        abstract: paper?.abstract || undefined, source: result.arxiv_id ? 'arxiv' : 'doi', importMethod: 'link',
        sourceUrl: result.fulltext_candidates[0]?.url,
        folderId: null, status: 'unread', important: false, tags: [], parse: 'parsing' };
      linkRequestKey.current = uuidv4();
      setLinkPreview(result); setPendingArxiv(item);
    } catch (error) {
      if (isCurrentScope()) showImportError(error, "没有查到这篇论文");
    } finally {
      if (isCurrentScope()) setSearching(false);
    }
  };

  const commitArxiv = async () => {
    if (disabled || busyRef.current || !pendingArxiv || searching) return;
    setImportError(null);
    setSearching(true);
    try {
      if (!linkPreview) throw new Error('请重新查询文献');
      if (linkPreview.fulltext_candidates.length && linkPreview.disposition !== 'already_present') {
        await importPaperLink(datasetId, linkPreview, linkRequestKey.current);
        if (!isCurrentScope()) return;
      }
      const restored = restoreLibraryPaper(modernDataRef.current, linkPreview.work_id);
      const existing = duplicateOf(asLegacyLibrary(restored).papers, pendingArxiv);
      const importContext: LibraryImportContext | undefined = linkPreview.fulltext_candidates.length && linkPreview.disposition !== 'already_present' ? undefined : {
        idempotency_key: linkRequestKey.current, entry_type: 'paper_link', preview_token: linkPreview.preview_token,
        items: [{ paper_id: existing?.id || pendingArxiv.id }],
      };
      if (existing) {
        await persistModern({ ...restored, papers: restored.papers.map(paper => paper.id === existing.id ? { ...paper, academicWorkId: linkPreview.work_id } : paper) }, importContext);
      } else {
        const linked = { ...pendingArxiv, academicWorkId: linkPreview.work_id, folderId };
        await persist({ ...asLegacyLibrary(restored), papers: [...asLegacyLibrary(restored).papers, linked] }, [], importContext);
      }
      message.success(linkPreview.disposition === 'already_present' ? '该版本已在当前论文集' : linkPreview.fulltext_candidates.length ? '已提交导入任务，可在页面查看进度和重试' : '题录已保存，尚无可下载 PDF');
      clearImportResult();
      setImportMethod(null);
    } catch (error) {
      if (isImportPreviewExpired(error)) {
        setLinkPreview(undefined); setPendingArxiv(null);
        setImportError('导入预览已过期，请重新查询并确认文献');
        return;
      }
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
        const [metadata, contentHash] = await Promise.all([paperFromPdf(file, datasetId), computeFileSha256(file)]);
        if (!isCurrentScope()) return;
        const current = asLegacyLibrary(modernDataRef.current);
        const item = { ...metadata, folderId: libraryView === 'directory' ? folderId : null };
        const candidates = pdfTargetCandidates(current.papers, item);
        const matching = candidates.filter(paper => pdfIdentityMatch(paper, item) === 'matched');
        const duplicate = importTargetPaperId ? current.papers.find(paper => paper.id === importTargetPaperId) : matching.length === 1 ? matching[0] : candidates.length === 1 ? candidates[0] : undefined;
        if (importTargetPaperId && !duplicate) throw new Error('目标论文已移除，请重新打开后添加 PDF');
        setPending({ fresh: duplicate ? [] : [item], skipped: [], folders: current.folders, baseFolderIds: current.folders.map(folder => folder.id), attachTo: duplicate });
        setPendingPdf({ paperId: duplicate?.id || item.id, file, metadata: item, contentHash });
        setPendingAttachments([createAttachment({ paperId: duplicate?.id || item.id, name: file.name, kind: "pdf", mimeType: "application/pdf", size: file.size, source: "local", importMethod: "upload", isPrimary: true, sourceVersion: item.arxivVersion, contentHash })]);
        if (item.incomplete) message.warning("未提取到正式论文题名，当前使用文件名，请补全论文题名");
        return;
      }
      bibliographyFile.current = file.name;
      const text = await file.text();
      if (!isCurrentScope()) return;
      if (name.endsWith(".ris")) { setFormat("ris"); stage(parseRIS(text, "local")); }
      else if (name.endsWith(".csv")) {
        setFormat("csv");
        const parsed = parseCSV(text);
        stage(parsed.papers, parsed.folderNames);
      } else if (name.endsWith(".bib")) { setFormat("bib"); stage(parseBibTeX(text, "local")); }
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

  const primaryAttachment = (paperId: string) => {
    const paper = modernData.papers.find(item => item.id === paperId);
    const attachments = modernData.attachments.filter(item => paper?.attachmentIds.includes(item.id) && isStoredPdfAttachment(item));
    return attachments.find(item => item.isPrimary) || attachments[0];
  };
  const primaryDocument = (paperId: string) => primaryAttachment(paperId)?.contentRef;
  const originalUrl = (item: PaperItem) => safePaperUrl(primaryAttachment(item.id)?.sourceUrl) || safePaperUrl(item.sourceUrl) || paperLink(item);
  const reparseDisabledReason = (item: PaperItem) => !canParse ? '文档解析服务当前不可用'
    : !primaryDocument(item.id) ? '请先添加 PDF 附件'
    : processingLevel === 'stored' ? '请先提高知识库处理级别'
    : item.parse === 'parsing' ? '正文正在解析中' : undefined;
  const openReparse = (item: PaperItem) => {
    const document = primaryDocument(item.id);
    if (disabled || reparseDisabledReason(item) || !document) return;
    restartRef.current?.onOpen({ dataset: datasetId, ids: [document], names: [item.title], title: '重新解析', processingLevel });
  };
  const detailPaper = modernData.papers.find(item => item.id === detailPaperId);

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
        if (drag?.datasetId === datasetId && over && (over.id === PAPER_LIBRARY_ROOT || data.folders.some((folder) => folder.id === over.id))) placePapers(drag.ids, over.id === PAPER_LIBRARY_ROOT ? 'none' : String(over.id));
        endPaperDrag();
      }}
      onDragCancel={endPaperDrag}>
    <section className="paper-library">
      <header className="paper-library-head">
        <div className="paper-library-heading">
          {onBack && <Tooltip title="返回知识库"><Button className="paper-library-back" type="text" icon={<ArrowLeftOutlined />} aria-label="返回知识库" onClick={onBack} /></Tooltip>}
          <h1 title={title}>{title}</h1>
          {loading && !initialLoading && <span className="paper-library-refreshing" role="status">正在刷新…</span>}
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
            children: (["bib", "ris", "csv"] as const).map((exportFormat) => ({ key: exportFormat, label: exportFormat === "bib" ? "BibTeX（.bib）· LaTeX 写作" : exportFormat === "ris" ? "RIS（.ris）· 文献管理" : "CSV（.csv）· 表格整理",
              onClick: () => downloadPaperCitations({ papers: exportPapers, folders: data.folders }, exportFormat, title),
            })),
          }] }}>
            <Button type="text" className="paper-library-tool" icon={<ExportOutlined />} aria-label="导出题录" title="仅导出论文题录，不包含 PDF、附件、笔记和批注" disabled={!exportPapers.length}>导出题录</Button>
          </Dropdown>
          <PaperImportStatus key={datasetId} datasetId={datasetId} page={importPage} canWrite={canWrite} onOpenPaper={id => openDetail(id)}
            onResumeAssociation={resumeAssociation}
            open={activeTool === "imports"} onOpenChange={open => setActiveTool(current => open ? "imports" : current === "imports" ? null : current)}
            onRetry={async batch => { await retryAcademicImport(batch); setSyncRevision(value => value + 1); }} />
        </div>
      </header>
      {persistenceError && <Alert className="paper-library-sync-error" type="error" showIcon message="文献保存或加载失败" description={persistenceError} action={<Button size="small" disabled={loading || saving} onClick={() => void reload()}>重新加载</Button>} />}
      {!loading && !canWrite && !persistenceError && <Alert type="info" showIcon message="当前文献库为只读，无法编辑" />}
      {syncError && <Alert className="paper-library-sync-error" type="error" showIcon message="文献同步失败" description={syncError} action={<Button size="small" disabled={loading || saving} onClick={() => setSyncRevision((value) => value + 1)}>重新同步</Button>} />}
      <div className="paper-library-layout">
        <nav className="paper-library-rail" aria-label="文献浏览">
          <Dropdown trigger={["click"]} placement="bottomLeft" menu={{ items: [
            { key: "upload", icon: <UploadOutlined />, label: "直接上传", onClick: () => openImport("upload") },
            { key: "identifier", icon: <LinkOutlined />, label: "arXiv / DOI 链接", onClick: () => openImport("identifier") },
            { key: "bibliography", icon: <FileTextOutlined />, label: "BibTeX / RIS / CSV", onClick: () => openImport("bibliography") },
          ] }}>
            <Button className="paper-library-import-button" disabled={disabled} type="primary" icon={<PlusOutlined />}>导入<DownOutlined /></Button>
          </Dropdown>
          <div className="paper-library-folders">
          <div className="paper-library-browse-head"><span className="paper-library-rail-label">浏览视图</span></div>
          <FolderButton icon={<FileTextOutlined />} name="全部" count={data.papers.length} current={libraryView === "all"} onClick={() => selectView(null, 'all')} />
          <FolderButton icon={<StarOutlined />} name="重点论文" count={stats.important} current={libraryView === "important"} onClick={() => selectView(null, "important")} />
          <div className="paper-library-directory-head paper-library-browse-head">
            <span className="paper-library-rail-label">目录</span>
            <div className="paper-library-folder-tools">
              <Tooltip title="新建文件夹"><Button type="text" size="small" icon={<PlusOutlined />} aria-label="新建文件夹" disabled={disabled} onClick={() => { setFolderName(""); setFolderDialogOpen(true); }} /></Tooltip>
              <Tooltip title={currentFolder ? "删除当前文件夹" : "选择文件夹后删除"}><span><Button type="text" size="small" icon={<DeleteOutlined />} aria-label="删除文件夹" disabled={disabled || !currentFolder} onClick={deleteFolder} /></span></Tooltip>
            </div>
          </div>
          <PaperDirectoryTree key={datasetId} data={data} title={title} active={libraryView === 'directory'} folderId={folderId} detailPaperId={detailPaperId}
            disabled={disabled} draggedIds={draggedIds} dropFolderId={dropFolderId}
            onSelectFolder={id => selectView(id, 'directory')} onOpenPaper={id => openDetail(id)}
            onDragOver={(event, folder) => { if (!canDropPapers(event, folder)) return; event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setDropFolderId(folder); }}
            onDragLeave={() => setDropFolderId(null)} onDrop={receivePapers} />
          {!!draggedIds.length && <p className="paper-library-drag-hint" role="status">移动 {draggedIds.length} 篇文献<br />拖到目标文件夹</p>}
          </div>
          <div ref={setTaskActionsContainer} className="paper-library-task-actions-slot" />
        </nav>
        <div className="paper-library-board">
          <div className="paper-library-view-heading">
            <div><span className="paper-library-view-path">{libraryView === 'directory' ? [title, paperFolderPath(data.folders, folderId)].filter(Boolean).join(' / ') : '浏览视图'}</span><h2>{libraryView === 'all' ? '全部论文' : libraryView === 'important' ? '重点论文' : currentFolder?.name || title}</h2></div>
            <span>{libraryView === 'directory' ? `本层 ${scoped.length} 篇 · 含子目录 ${directoryPaperCount} 篇` : `${viewed.length} 篇论文`}</span>
          </div>
          {childFolders.length > 0 && <div className="paper-library-child-folders" aria-label="子目录">{childFolders.map(folder => {
            const descendants = descendantFolderIds(data.folders, folder.id);
            const count = data.papers.filter(paper => !!paper.folderId && descendants.has(paper.folderId)).length;
            return <Button type="text" key={folder.id} icon={<FolderOutlined />} onClick={() => selectView(folder.id, 'directory')}><span>{folder.name}</span><span className="paper-library-child-count">{count} 篇</span></Button>;
          })}</div>}
          <PaperTasks key={datasetId} dataset={datasetId} actionsContainer={taskActionsContainer} mode={paperTask} setMode={setPaperTask} selected={selectedDocuments} selectedTitles={selectedPapers.map(item => item.title)} selectionError={selectionError} canImport={canWrite} onImportSubmitted={() => setSyncRevision(value => value + 1)} />
          {selectedPapers.length > 0 && (
        <div className="paper-library-selection-bar" role="region" aria-label="已选论文">
          <div className="paper-library-selection-summary"><Button type="text" size="small" aria-label="取消选择" icon={<CloseOutlined />} onClick={() => setSelected([])} /><span>已选 <strong>{selectedPapers.length}</strong> 篇</span></div>
          <Dropdown trigger={["click"]} menu={{ items: folderOptions.map((folder) => ({ key: folder.value, label: folder.label, onClick: () => placePapers(selected, folder.value) })) }}><Button type="text" size="small" disabled={disabled} icon={<FolderOutlined />}>移动到<DownOutlined /></Button></Dropdown>
          <Button type="text" size="small" disabled={disabled} danger icon={<DeleteOutlined />} onClick={() => Modal.confirm({ title: `移除已选的 ${selectedPapers.length} 篇题录？`, okText: "移除", cancelText: "取消", onOk: () => dropPapers(selected) })}>移除</Button>
          </div>
          )}
          <div className="paper-library-table-scroll" tabIndex={0} aria-label="论文列表，可横向滚动" aria-busy={initialLoading || loading}>
          <table className="paper-library-table" aria-label="论文列表">
            <colgroup><col className="paper-library-controls-col" /><col /><col className="paper-library-year-col" /><col className="paper-library-venue-col" /><col className="paper-library-actions-col" /></colgroup>
            <thead>
              <tr>
                <th scope="col" className={`paper-library-selection-head ${selectedVisibleCount ? "has-selection" : ""}`}>
                  <input type="checkbox" className="paper-library-checkbox" aria-label="选择当前列表全部论文" disabled={!visible.length}
                    checked={visible.length > 0 && selectedVisibleCount === visible.length}
                    ref={(element) => { if (element) element.indeterminate = selectedVisibleCount > 0 && selectedVisibleCount < visible.length; }}
                    onChange={(event) => setSelected(event.target.checked ? [...new Set([...selected, ...visible.map((item) => item.id)])] : selected.filter((id) => !visible.some((item) => item.id === id)))} />
                </th>
                <th scope="col">标题</th><th scope="col">年份</th><th scope="col">会议／期刊</th><th scope="col"><span className="paper-library-visually-hidden">文献操作</span></th>
              </tr>
            </thead>
            <tbody>
          {initialLoading && <tr><td colSpan={5}><div className="paper-library-loading" role="status"><Spin size="small" />正在加载文献…</div></td></tr>}
          {!initialLoading && !visible.length && <tr><td colSpan={5}><div className="paper-library-empty">没有符合条件的论文。可以导入 DOI、arXiv、BibTeX、RIS、CSV，或上传 PDF。</div></td></tr>}
          {!initialLoading && visible.map((item) => (
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
                  <div className="paper-library-title-line"><h2 className="paper-library-title"><button type="button" className="paper-library-title-button" onClick={() => openDetail(item.id)}>{item.title}</button></h2>
                    <PaperImportanceButton paperID={item.academicWorkId} important={item.important} onToggle={async important => {
                      if (busyRef.current) return;
                      busyRef.current = true;
                      syncGeneration.current += 1;
                      try { const saved = await setLibraryPaperImportance(datasetId, item.id, revisionRef.current, important); if (isCurrentScope()) applySaved(saved); }
                      finally { if (isCurrentScope()) busyRef.current = false; }
                    }} onChange={async () => { setSyncRevision(value => value + 1); }} />

                  </div>
                <p className="paper-library-cite">{item.authors || "作者待补全"}</p>
                {(() => {
                  const identity = paperDocument(modernData, item.id, academicStates);
                  const status = identity?.state.status;
                  return status && status !== 'succeeded' ? <button type="button" className="paper-library-state" onClick={() => openDetail(item.id)}>{status === 'failed' ? '论文信息识别失败' : '论文信息识别中'}</button> : null;
                })()}
                <div className="paper-library-meta">
                  {item.tags.filter((tag) => tag !== "paper" && !tag.startsWith("arxiv:")).map((tag) => (
                    <span key={tag} className="paper-library-tag">{tag}</span>
                  ))}
                  {item.incomplete && academicStatesReady && <Tooltip title="尚未提取到正式论文题名，当前使用文件名。点击补全题名"><button type="button" className="paper-library-state" disabled={disabled} onClick={() => {
                    openDetail(item.id);
                    setDetailEditing(true);
                    editingRevisionRef.current = revisionRef.current;
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
              <td className="paper-library-year">{item.year || "—"}</td>
              <td className="paper-library-venue"><span title={venueName(item)}>{item.venue?.trim() ? venueName(item) : "—"}</span></td>
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
                  <Dropdown menu={{ items: [
                    { key: "detail", label: "查看详情", onClick: () => openDetail(item.id) },
                    { key: "citation", label: "复制引用", children: (Object.keys(citationLabel) as CitationStyle[]).map(style => ({
                      key: style, label: citationLabel[style], onClick: () => copyText(formatCitation(item, style), `${citationLabel[style]}引用`),
                    })) },
                    { key: "link", label: "打开原文", disabled: !originalUrl(item), onClick: () => { const href = originalUrl(item); if (href) window.open(href, "_blank", "noopener"); } },
                    { key: "reparse", label: "重新解析", disabled: disabled || !!reparseDisabledReason(item),
                      title: reparseDisabledReason(item), onClick: () => openReparse(item) },
                    { type: "divider" },
                    { key: "move", label: "移动到", disabled, children: folderOptions.map((option) => ({ key: option.value, label: option.label, onClick: () => placePapers([item.id], option.value) })) },
                    { key: "remove", label: "从论文集移除", disabled, danger: true, onClick: () => Modal.confirm({ title: "从论文集移除这篇题录？", okText: "移除", cancelText: "取消", onOk: () => dropPapers([item.id]) }) },
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
      <Modal title="新建文件夹" open={folderDialogOpen} onCancel={() => { if (!saving) setFolderDialogOpen(false); }} onOk={addFolder} okText="创建" cancelText="取消" confirmLoading={saving} okButtonProps={{ disabled: disabled || !folderName.trim() }} width={400} centered destroyOnHidden>
        <label className="paper-library-folder-form">文件夹名称
          <Input autoFocus maxLength={80} value={folderName} placeholder="例如：长上下文研究" onChange={(event: ChangeEvent<HTMLInputElement>) => setFolderName(event.target.value)} onPressEnter={addFolder} />
        </label>
      </Modal>
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
            <p>{importTargetPaperId ? '为当前论文添加 PDF，核对题录和归属后确认上传。' : '选择 PDF 或文献文件，先识别题录；确认加入论文库后才会上传 PDF。'}</p>
            <Upload.Dragger accept={importTargetPaperId ? '.pdf' : '.pdf,.bib,.ris,.csv'} showUploadList={false} disabled={disabled || searching} beforeUpload={async (file: File) => { await importFile(file); return false; }}>
              <UploadOutlined className="paper-library-drop-icon" />
              <strong>{searching ? "正在识别文献…" : "点击选择，或拖拽文件到这里"}</strong>
              <span>{importTargetPaperId ? 'PDF' : 'PDF、BibTeX、RIS、CSV'}</span>
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
          {pendingArxiv && <section className="paper-library-import-confirm" aria-label="链接导入确认">
            <div className="paper-library-arxiv-source"><span>{pendingArxiv.arxiv ? "ARXIV" : "DOI"}</span><code>{pendingArxiv.arxiv || pendingArxiv.doi}{pendingArxiv.arxivVersion}</code></div>
            <h3>{pendingArxiv.title}</h3>
            <p className="paper-library-arxiv-authors">{pendingArxiv.authors} · {pendingArxiv.year}</p>
            {pendingArxiv.abstract && <p className="paper-library-arxiv-abstract">{pendingArxiv.abstract}</p>}
            <p className="paper-library-arxiv-hint">{linkPreview?.fulltext_candidates.length ? "确认后提交下载入库任务，同一论文的不同版本保留为独立附件。" : "仅找到文献信息，可先保存题录，后续补充 PDF。"}</p>
            <div className="paper-library-modal-actions"><Button type="primary" loading={searching || saving} disabled={disabled} onClick={commitArxiv}>{searching ? "正在提交…" : linkPreview?.fulltext_candidates.length ? "导入文献与 PDF" : "保存题录"}</Button></div>
          </section>}
          {pending && <section className="paper-library-import-confirm" aria-label="导入确认">
            <h3>{pending.attachTo ? "为已有文献添加 PDF" : pending.fresh.length ? `可导入 ${pending.fresh.length} 篇文献` : "文献已在库中"}</h3>
            <div className="paper-library-import-preview">
              {pending.attachTo && <p>{pending.attachTo.title}</p>}
              {pending.fresh.map((item) => <p key={item.id}>{item.title}{item.incomplete && <small>待补全论文题名</small>}</p>)}
              {pending.skipped.map((item, index) => <p key={`${item.id}-${index}`} className="paper-library-import-skipped">已跳过重复：{item.title}</p>)}
            </div>
            {pendingPdf && <PdfImportReview metadata={pendingPdf.metadata} target={pending.attachTo} papers={data.papers}
              candidateIds={pdfTargetCandidates(data.papers, pendingPdf.metadata).map(paper => paper.id)} file={pendingPdf.file} fixedTarget={!!importTargetPaperId}
              conflict={pdfConflict} requiresConfirmation={pdfRequiresConfirmation} sameVersion={samePdfVersion} confirmed={pdfConfirmed} disabled={disabled || searching}
              onTargetChange={selectPdfTarget} onConfirmedChange={setPdfConfirmed} />}
            {pendingPdf && pdfConflict && <Button disabled={disabled || searching} onClick={() => { setImportTargetPaperId(null); selectPdfTarget(null); }}>作为新论文导入</Button>}
            {(!!pending.fresh.length || !!pending.skipped.length || pending.attachTo) && <div className="paper-library-modal-actions"><Button type="primary" loading={saving || confirmingImport} disabled={disabled || searching || (!!pendingPdf && !pdfReviewComplete)} onClick={commit}>{pending.attachTo ? "添加 PDF" : pending.fresh.length ? "加入论文库" : "确认导入"}</Button></div>}
          </section>}
        </div>
      </Modal>
      {detailPaper && (
        <PaperDetailDrawer
          key={`${detailPaperId}:${detailTab}:${detailEditing}`}
          initialTab={detailTab}
          initialEditing={detailEditing}
          academic={paperDocument(modernData, detailPaper.id, academicStates)}
          editError={editError}
          onEdit={() => { editingRevisionRef.current = revisionRef.current; setEditing(data.papers.find(item => item.id === detailPaperId)!); setEditError(null); }}
          onSave={saveEdits}
          onRetryIdentification={async () => {
            const document = paperDocument(modernDataRef.current, detailPaper.id, academicStates)?.state.document_id;
            if (!document) throw new Error("请先关联 PDF 附件");
            await updatePaperIdentity(document, { reidentify: true });
            if (isCurrentScope()) {
              syncGeneration.current += 1;
              setAcademicStates(states => states.map(item => item.state.document_id === document
                ? { ...item, state: { ...item.state, status: 'pending', manual: false, error: undefined } } : item));
              setSyncRevision(value => value + 1);
            }
          }}
          paper={detailPaper}
          onReparse={() => openReparse(detailPaper)}
          reparseDisabledReason={reparseDisabledReason(detailPaper)}
          data={modernData}
          onClose={() => setDetailPaperId(null)}
          onChange={persistModern}
          disabled={disabled}
          onOpenAttachment={openDocument}
          onAddPdf={() => openImport('upload', detailPaper.id)}
          onDetachAttachment={async attachment => {
            const current = modernDataRef.current;
            await persistModern({ ...current,
              hiddenDocumentIds: attachment.contentRef ? [...new Set([...(current.hiddenDocumentIds || []), attachment.contentRef])] : current.hiddenDocumentIds,
              papers: current.papers.map(paper => paper.id === detailPaper.id ? { ...paper, attachmentIds: paper.attachmentIds.filter(id => id !== attachment.id) } : paper),
            }, undefined, { operation: 'detach', paper_id: detailPaper.id, document_id: attachment.contentRef || '', attachment_id: attachment.id, confirmed: true, idempotency_key: uuidv4() });
            setSyncRevision(value => value + 1);
          }}
        />
      )}
      <RestartKnowledgeModal ref={restartRef} parsers={parsers} onFinish={() => setSyncRevision(value => value + 1)} />
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

function FolderButton({ name, icon, count, current, onClick, dropTarget, dropId, acceptsDrop, onDragOver, onDragLeave, onDrop }: { name: string; icon: ReactNode; count: number; current: boolean; onClick: () => void; dropTarget?: boolean; dropId?: string; acceptsDrop?: boolean; onDragOver?: DragEventHandler<HTMLButtonElement>; onDragLeave?: DragEventHandler<HTMLButtonElement>; onDrop?: DragEventHandler<HTMLButtonElement> }) {
  const { setNodeRef, isOver } = useDroppable({ id: dropId || `view:${name}`, disabled: !dropId || !acceptsDrop });
  return (
    <div className={`paper-library-folder ${current ? "current" : ""}`}>
      <button ref={setNodeRef} type="button" className={dropTarget || isOver ? "is-drop-target" : undefined} aria-current={current ? "true" : undefined} onClick={onClick} onDragOver={onDragOver} onDragLeave={onDragLeave} onDrop={onDrop}>
        <span className="paper-library-folder-name">{icon}<span title={name}>{name}</span></span><span className="paper-library-folder-count">{count}</span>
      </button>
    </div>
  );
}
