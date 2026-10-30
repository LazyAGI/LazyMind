import { useEffect, useRef, useState, type MutableRefObject, type Dispatch, type SetStateAction, type ChangeEvent } from 'react';
import { Alert, Button, Input, Modal, Radio, Tooltip, Upload, message, type RadioChangeEvent } from 'antd';
import { InfoCircleOutlined, UploadOutlined } from '@ant-design/icons';
import { v4 as uuidv4 } from 'uuid';
import { assignFolders, classifyIdentifier, parseAuthorInput, parseBibTeX, parseCSV, parseRIS, planImport, paperSourceVersionLabels, type LibraryView, type PaperLibraryData, type PaperRecord } from './model';
import { mergeLibraryChanges, previewLibraryPapers, confirmLibraryPdf, importLibraryPapers, type LibraryChanges } from './store';
import { previewPaperLink, importPaperLink, isImportPreviewExpired, type PaperUploadContext, type ImportPreviewItem, type ImportOperationItem } from '../api/academicReferences';
import { paperFromPdf } from './pdfMeta';
import { computeFileSha256 } from '../utils/fileHash';
import { isArxivInput } from './arxivImport';
import { pdfIdentityMatch, pdfTargetCandidates, type PdfPaper } from './pdfAssociation';
import PdfImportReview from './PdfImportReview';
import type { PaperNotification } from './usePaperNotifications';
type ImportMethod = 'upload' | 'identifier' | 'bibliography';
const importTitles: Record<ImportMethod, string> = { upload: '上传文献', identifier: '通过链接导入', bibliography: '导入文献题录' };
interface ImportOptions {
    datasetId: string;
    dataState: PaperLibraryData;
    dataRef: MutableRefObject<PaperLibraryData>;
    disabled: boolean;
    saving: boolean;
    confirmingImport: boolean;
    busyRef: MutableRefObject<boolean>;
    loadedRef: MutableRefObject<boolean>;
    canWriteRef: MutableRefObject<boolean>;
    syncGeneration: MutableRefObject<number>;
    reloadPromiseRef: MutableRefObject<Promise<void> | null>;
    reload: () => Promise<void> | null;
    isCurrentScope: () => boolean;
    mutate: (operation: () => Promise<LibraryChanges>, notice?: Omit<PaperNotification, "message">) => Promise<void>;
    notify: (notice: PaperNotification) => void;
    resolve: (id: string) => void;
    applySnapshot: (data: PaperLibraryData) => void;
    setConfirmingImport: Dispatch<SetStateAction<boolean>>;
    setSyncRevision: Dispatch<SetStateAction<number>>;
    libraryView: LibraryView;
    folderId: string | null;
    onUploadPdf?: (file: File, context?: PaperUploadContext) => Promise<string | void>;
}
export function usePaperImports(options: ImportOptions) {
    const { datasetId, dataState, dataRef, disabled, saving, confirmingImport, busyRef, loadedRef, canWriteRef, syncGeneration, reloadPromiseRef, reload, isCurrentScope, mutate, applySnapshot, setConfirmingImport, setSyncRevision, libraryView, folderId, onUploadPdf, notify, resolve } = options;
    const [linkPreview, setLinkPreview] = useState<ImportPreviewItem>();
    const pendingImportKey = useRef(uuidv4());
    const bibliographyFile = useRef<string>();
    const linkRequestKey = useRef(uuidv4());
    const [pendingArxiv, setPendingArxiv] = useState<PaperRecord | null>(null);
    const [importMethod, setImportMethod] = useState<ImportMethod | null>(null);
    const [importTargetPaperId, setImportTargetPaperId] = useState<string | null>(null);
    const [identifier, setIdentifier] = useState("");
    const [bibliography, setBibliography] = useState("");
    const bibliographyKind = "local";
    const [format, setFormat] = useState<"bib" | "ris" | "csv">("bib");
    const [searching, setSearching] = useState(false);
    const [selectedFile, setSelectedFile] = useState<File | null>(null);
    const [pending, setPending] = useState<{
        fresh: PaperRecord[];
        skipped: PaperRecord[];
        folders: PaperLibraryData["folders"];
        attachTo?: PaperRecord;
    } | null>(null);
    const [pendingPdf, setPendingPdf] = useState<{
        documentId?: string;
        file?: File;
        metadata: PdfPaper;
        contentHash: string;
    } | null>(null);
    const [pdfConfirmed, setPdfConfirmed] = useState(false);
    const [importError, setImportError] = useState<string | null>(null);
    const fileRef = useRef<HTMLInputElement>(null);
    useEffect(() => { setImportMethod(null); setImportTargetPaperId(null); setSearching(false); setIdentifier(''); setBibliography(''); clearImportResult(); }, [datasetId]);
    const clearImportResult = () => {
        pendingImportKey.current = uuidv4();
        bibliographyFile.current = undefined;
        setImportError(null);
        setSelectedFile(null);
        setPendingArxiv(null);
        setLinkPreview(undefined);
        setPending(null);
        setPendingPdf(null);
        setPdfConfirmed(false);
    };
    const pdfTarget = pending?.attachTo;
    const pdfConflict = !!pendingPdf && !!pdfTarget && pdfIdentityMatch(pdfTarget, pendingPdf.metadata) === 'conflict';
    const pdfRequiresConfirmation = !!pendingPdf && !!pdfTarget && pdfIdentityMatch(pdfTarget, pendingPdf.metadata) !== 'matched';
    const samePdfVersion = !!pendingPdf?.metadata.arxivVersion && !!pdfTarget && dataState.attachments.some(attachment => dataState.papers.find(paper => paper.id === pdfTarget.id)?.attachmentIds.includes(attachment.id) && attachment.sourceVersion === pendingPdf.metadata.arxivVersion && attachment.contentHash !== pendingPdf.contentHash);
    const pdfReviewComplete = !pdfConflict && (!(pdfRequiresConfirmation || samePdfVersion) || pdfConfirmed);
    const selectPdfTarget = (paperId: string | null) => {
        if (!pendingPdf)
            return;
        const current = dataRef.current;
        const target = current.papers.find(paper => paper.id === paperId);
        const candidate = { ...pendingPdf.metadata, folderId: libraryView === 'directory' ? folderId : null };
        setPending({ fresh: target ? [] : [candidate], skipped: [], folders: current.folders, attachTo: target });
        setPendingPdf({ ...pendingPdf });
        setPdfConfirmed(false);
        setImportError(null);
    };
    const showImportError = (error: unknown, fallback: string) => {
        setImportError(error instanceof Error && error.message ? error.message : fallback);
    };
    const stage = async (items: PaperRecord[], folderNames?: Array<string | null>) => {
        if (!items.length) {
            setPending(null);
            throw new Error("没有识别到文献，请检查文件内容或题录格式");
        }
        try {
            items = await previewLibraryPapers(datasetId, items);
            if (isCurrentScope()) resolve('bibliography-preview');
        } catch (error) {
            reportImportError(error, '题录导入预览失败', 'bibliography-preview');
            return;
        }
        if (!isCurrentScope())
            return;
        const current = dataRef.current;
        const targetFolder = current.folders.some((folder) => folder.id === folderId) ? folderId : null;
        const placed = assignFolders(current, items, folderNames || items.map(() => null), targetFolder);
        const planned = planImport(current.papers, placed.papers);
        setPending({ ...planned, folders: placed.folders });
        if (!planned.fresh.length)
            message.info(`库中已有，跳过 ${planned.skipped.length} 篇`);
    };
    const reportImportError = (error: unknown, title: string, id = "import-submit") => {
        if (!isCurrentScope()) return;
        notify({ id, title, message: error instanceof Error ? error.message : title });
        setImportError("导入尚未完成，已保留当前输入；请重试或查看通知。");
    };
    const commit = async () => {
        if (disabled || busyRef.current || searching || !pending || (pendingPdf && !pdfReviewComplete))
            return;
        busyRef.current = true;
        syncGeneration.current += 1;
        setConfirmingImport(true);
        setImportError(null);
        let submitting = false;
        try {
            const current = dataRef.current;
            if (pendingPdf) {
                const target = pending.attachTo ? current.papers.find(p => p.id === pending.attachTo!.id) : pending.fresh[0];
                if (!target)
                    throw new Error('目标文献已被移除，请重新核对后导入');
                if (pending.attachTo && target.revision !== pending.attachTo.revision)
                    throw new Error('目标题录已更新，请重新核对 PDF 关联');
                if (pdfIdentityMatch(target, pendingPdf.metadata) === 'conflict')
                    throw new Error('PDF 与目标论文身份冲突，未添加附件');
                const owner = pendingPdf.contentHash ? current.papers.find(p => p.attachmentIds.some(id => current.attachments.some(a => a.id === id && a.available !== false && a.contentHash === pendingPdf.contentHash))) : undefined;
                if (owner && owner.id !== target.id)
                    throw new Error(`这份 PDF 已关联《${owner.metadata.title}》，请核对归属`);
                let documentId = pendingPdf.documentId;
                if (!documentId) {
                    if (!onUploadPdf || !pendingPdf.file)
                        throw new Error('PDF 文件不可用，请重新选择后重试');
                    submitting = true;
                    const uploaded = await onUploadPdf(pendingPdf.file, { idempotency_key: pendingImportKey.current, library_paper_id: target.id, title: target.metadata.title, paper: target, source_version: pendingPdf.metadata.arxivVersion });
                    if (!isCurrentScope())
                        return;
                    if (typeof uploaded !== 'string' || !uploaded.trim())
                        throw new Error('PDF 上传未返回有效文档 ID');
                    documentId = uploaded.trim();
                    setPendingPdf({ ...pendingPdf, documentId });
                }
                busyRef.current = false;
                submitting = true;
                await mutate(() => confirmLibraryPdf(datasetId, target, documentId!, pendingImportKey.current, pendingPdf.metadata.arxivVersion), { id: "import-submit", title: "PDF 关联保存失败" });
                message.success('PDF 已关联，原有题录与版本均已保留');
            }
            else {
                const folders = pending.folders.filter(f => !current.folders.some(saved => saved.id === f.id));
                const papers = [...pending.fresh, ...pending.skipped];
                busyRef.current = false;
                submitting = true;
                await mutate(() => importLibraryPapers(datasetId, papers, folders, pendingImportKey.current, format, bibliographyFile.current), { id: "import-submit", title: "题录导入保存失败" });
                message.success(`导入已完成，共 ${papers.length} 条；已有论文保留原题录与归档`);
            }
            if (!isCurrentScope())
                return;
            resolve("import-submit");
            clearImportResult();
            setImportMethod(null);
            setSyncRevision(value => value + 1);
        }
        catch (error) {
            if (isCurrentScope()) {
                if (submitting) reportImportError(error, '文献导入保存失败');
                else showImportError(error, '文献保存失败，请重试');
            }
        }
        finally {
            if (isCurrentScope()) {
                busyRef.current = false;
                setConfirmingImport(false);
            }
        }
    };
    const openImport = (method: ImportMethod, targetPaperId: string | null = null) => {
        if (disabled || busyRef.current)
            return;
        clearImportResult();
        setImportMethod(method);
        setImportTargetPaperId(targetPaperId);
    };
    const resumeAssociation = (item: ImportOperationItem) => {
        const documentId = item.document_id || item.current_document?.document_id;
        if (disabled || !documentId || !item.upload_request_key)
            return;
        const snapshot = item.pending_paper;
        if (!snapshot?.id || !snapshot.metadata || !Array.isArray(snapshot.metadata.authors)) {
            notify({ id: `association:${item.item_id}`, title: '上传记录无法恢复关联', message: '上传回执缺少有效题录，请重新选择原文件。' });
            return;
        }
        clearImportResult();
        const current = dataRef.current;
        const existing = current.papers.find(paper => paper.id === item.library_paper_id);
        const metadata: PdfPaper = { ...snapshot, metadata: { ...snapshot.metadata, authors: parseAuthorInput(snapshot.metadata.authors) }, identityVerified: false };
        const restored = { ...metadata, arxivVersion: item.source_version || metadata.arxivVersion, folderId: metadata.folderId && current.folders.some(folder => folder.id === metadata.folderId) ? metadata.folderId : null };
        setPending({ fresh: existing ? [] : [restored], skipped: [], folders: current.folders, attachTo: existing });
        setPendingPdf({ documentId, metadata: restored, contentHash: item.content_sha256 || '' });
        pendingImportKey.current = item.upload_request_key;
        setImportMethod('upload');
        setImportTargetPaperId(existing?.id || null);
    };
    const importBibliography = async () => {
        if (disabled || busyRef.current)
            return;
        clearImportResult();
        try {
            const parsed = format === "ris"
                ? parseRIS(bibliography, bibliographyKind)
                : format === "csv"
                    ? parseCSV(bibliography, bibliographyKind)
                    : { papers: parseBibTeX(bibliography, bibliographyKind), folderNames: [] as Array<string | null> };
            const papers = "papers" in parsed ? parsed.papers : parsed;
            const folderNames = "folderNames" in parsed ? parsed.folderNames : undefined;
            await stage(papers.map(item => ({ ...item, importMethod: "bibliography" })), folderNames);
        }
        catch (error) {
            showImportError(error, "题录解析失败，请检查内容和格式");
        }
    };
    const importIdentifier = async () => {
        if (disabled || busyRef.current || searching)
            return;
        clearImportResult();
        const kind = classifyIdentifier(identifier);
        const arxivInput = isArxivInput(identifier);
        if (!kind && !arxivInput) {
            showImportError(null, "请粘贴有效的 DOI、arXiv 编号或链接");
            return;
        }
        setSearching(true);
        try {
            const result = await previewPaperLink(identifier, datasetId, true);
            if (!isCurrentScope())
                return;
            resolve("import-preview");
            const paper = result.paper;
            const item: PaperRecord = { revision: 0, attachmentIds: [], noteIds: [], id: `link-${result.work_id}`,
                arxivVersion: result.fulltext_candidates[0]?.version,
                source: result.arxiv_id ? 'arxiv' : 'doi', importMethod: 'link',
                sourceUrl: result.fulltext_candidates[0]?.url,
                folderId: null, status: 'unread', important: false, tags: [], parse: 'parsing', metadata: {
                    title: paper?.title || result.title,
                    authors: paper?.authors || [],
                    citationCount: paper?.citation_count,
                    year: paper?.year || 0,
                    venue: paper?.venue || '',
                    abstract: paper?.abstract || undefined,
                    identifiers: { doi: result.doi, arxiv: result.arxiv_id }
                } };
            linkRequestKey.current = uuidv4();
            setLinkPreview(result);
            setPendingArxiv(item);
        }
        catch (error) {
            if (isCurrentScope()) reportImportError(error, '论文链接查询失败', 'import-preview');
        }
        finally {
            if (isCurrentScope())
                setSearching(false);
        }
    };
    const commitArxiv = async () => {
        if (disabled || busyRef.current || !pendingArxiv || !linkPreview || searching)
            return;
        setSearching(true);
        setImportError(null);
        try {
            const result = await importPaperLink(datasetId, linkPreview, linkRequestKey.current, libraryView === 'directory' ? folderId : null, true);
            if (!isCurrentScope())
                return;
            if ('data' in result && result.data)
                applySnapshot(mergeLibraryChanges(dataRef.current, result as unknown as LibraryChanges));
            setSyncRevision(value => value + 1);
            clearImportResult();
            setImportMethod(null);
            resolve('import-link');
            message.success('已提交导入，可在导入记录中查看进度');
        }
        catch (error) {
            if (isImportPreviewExpired(error)) {
                setLinkPreview(undefined);
                setPendingArxiv(null);
                setImportError('导入预览已过期，请重新查询');
            }
            else if (isCurrentScope()) reportImportError(error, '链接导入提交失败', 'import-link');
        }
        finally {
            if (isCurrentScope())
                setSearching(false);
        }
    };
    const importFile = async (file: File) => {
        if (!isCurrentScope() || saving || confirmingImport || searching)
            return;
        clearImportResult();
        setSelectedFile(file);
        const name = file.name.toLowerCase();
        setSearching(true);
        try {
            if (!loadedRef.current) {
                await reload();
                if (!isCurrentScope())
                    return;
            }
            let pdf: [
                PdfPaper,
                string
            ] | undefined;
            let text = '';
            if (name.endsWith('.pdf'))
                pdf = await Promise.all([paperFromPdf(file, datasetId), computeFileSha256(file)]);
            else
                text = await file.text();
            if (!isCurrentScope())
                return;
            // Returning from the native picker can refresh the library before its change event.
            // Keep the file, then match it against the refreshed snapshot and access rights.
            while (reloadPromiseRef.current) {
                await reloadPromiseRef.current;
                if (!isCurrentScope())
                    return;
            }
            if (!loadedRef.current)
                throw new Error('论文库尚未就绪，请重试识别');
            if (!canWriteRef.current)
                throw new Error('当前论文库没有编辑权限，无法导入文献');
            if (pdf) {
                const [metadata, contentHash] = pdf;
                const current = dataRef.current;
                const item = { ...metadata, folderId: libraryView === 'directory' ? folderId : null };
                const candidates = pdfTargetCandidates(current.papers, item);
                const matching = candidates.filter(paper => pdfIdentityMatch(paper, item) === 'matched');
                const duplicate = importTargetPaperId ? current.papers.find(paper => paper.id === importTargetPaperId) : matching.length === 1 ? matching[0] : candidates.length === 1 ? candidates[0] : undefined;
                if (importTargetPaperId && !duplicate)
                    throw new Error('目标论文已移除，请重新打开后添加 PDF');
                setPending({ fresh: duplicate ? [] : [item], skipped: [], folders: current.folders, attachTo: duplicate });
                setPendingPdf({ file, metadata: item, contentHash });
                if (item.incomplete)
                    message.warning("未提取到正式论文题名，当前使用文件名，请补全论文题名");
                return;
            }
            bibliographyFile.current = file.name;
            if (name.endsWith(".ris")) {
                setFormat("ris");
                await stage(parseRIS(text, "local"));
            }
            else if (name.endsWith(".csv")) {
                setFormat("csv");
                const parsed = parseCSV(text);
                await stage(parsed.papers, parsed.folderNames);
            }
            else if (name.endsWith(".bib")) {
                setFormat("bib");
                await stage(parseBibTeX(text, "local"));
            }
            else
                throw new Error("请选择 PDF、BibTeX、RIS 或 CSV 文件");
        }
        catch (error) {
            if (isCurrentScope()) {
                setPending(null);
                showImportError(error, "文件没有读出来");
            }
        }
        finally {
            if (isCurrentScope())
                setSearching(false);
        }
    };
    return { openImport, resumeAssociation, importMethod, setImportMethod, clearImportResult, importError, selectedFile, pending, importFile, importTargetPaperId, searching, identifier, setIdentifier, importIdentifier, format, setFormat, fileRef, bibliography, setBibliography, importBibliography, pendingArxiv, linkPreview, commitArxiv, pendingPdf, pdfConflict, pdfRequiresConfirmation, samePdfVersion, pdfConfirmed, setPdfConfirmed, selectPdfTarget, setImportTargetPaperId, pdfReviewComplete, commit };
}
interface DialogProps {
    imports: ReturnType<typeof usePaperImports>;
    data: PaperLibraryData;
    disabled: boolean;
    loading: boolean;
    saving: boolean;
    confirmingImport: boolean;
    fileImportDisabled: boolean;
}
export function PaperImportDialog({ imports, data, disabled, loading, saving, confirmingImport, fileImportDisabled }: DialogProps) {
    const { importMethod, setImportMethod, clearImportResult, importError, selectedFile, pending, importFile, importTargetPaperId, searching, identifier, setIdentifier, importIdentifier, format, setFormat, fileRef, bibliography, setBibliography, importBibliography, pendingArxiv, linkPreview, commitArxiv, pendingPdf, pdfConflict, pdfRequiresConfirmation, samePdfVersion, pdfConfirmed, setPdfConfirmed, selectPdfTarget, setImportTargetPaperId, pdfReviewComplete, commit } = imports;
    return <Modal title={importMethod ? importTitles[importMethod] : "导入文献"} open={!!importMethod} onCancel={() => {
            if (!searching && !saving && !confirmingImport) {
                setImportMethod(null);
                clearImportResult();
            }
        }} footer={null} width={560} centered destroyOnHidden className="paper-library-import-modal" closable={!searching && !saving && !confirmingImport} maskClosable={!searching && !saving && !confirmingImport} keyboard={!searching && !saving && !confirmingImport}>
        <div className="paper-library-import-content">
          {importError && <Alert type="error" showIcon message="导入未完成" description={importError} action={selectedFile && !pending ? <Button size="small" disabled={loading || saving || confirmingImport || searching} onClick={() => void importFile(selectedFile)}>重试识别</Button> : undefined}/>}
          {importMethod === "upload" && <>
            <p>{importTargetPaperId ? '为当前论文添加 PDF，核对题录和归属后确认上传。' : '选择 PDF 或文献文件，先识别题录；确认加入论文库后才会上传 PDF。'}</p>
            <Upload.Dragger accept={importTargetPaperId ? '.pdf' : '.pdf,.bib,.ris,.csv'} showUploadList={false} disabled={fileImportDisabled || searching} beforeUpload={async (file: File) => { await importFile(file); return false; }}>
              <UploadOutlined className="paper-library-drop-icon"/>
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
            <Input id="paper-import-identifier" autoFocus size="large" value={identifier} placeholder="https://arxiv.org/abs/2404.06654" disabled={disabled || searching} onChange={(event: ChangeEvent<HTMLInputElement>) => { setIdentifier(event.target.value); clearImportResult(); }} onPressEnter={() => {
                if (!searching)
                    void importIdentifier();
            }}/>
            <div className="paper-library-modal-actions"><Button type="primary" loading={searching} disabled={disabled || !identifier.trim()} onClick={importIdentifier}>查找文献</Button></div>
          </>}
          {importMethod === "bibliography" && <>
            <p>上传 BibTeX、RIS 或 CSV 文件，也可以直接粘贴题录内容。</p>
            <div className="paper-library-bibliography-toolbar">
              <Radio.Group value={format} disabled={disabled || searching} onChange={(event: RadioChangeEvent) => {
                const nextFormat = event.target.value;
                if (nextFormat === "bib" || nextFormat === "ris" || nextFormat === "csv") {
                    setFormat(nextFormat);
                    clearImportResult();
                }
            }} optionType="button" buttonStyle="solid" options={[{ label: "BibTeX", value: "bib" }, { label: "RIS", value: "ris" }, { label: "CSV", value: "csv" }]}/>
              <Button icon={<UploadOutlined />} disabled={fileImportDisabled || searching} onClick={() => fileRef.current?.click()}>选择文件</Button>
              <input ref={fileRef} hidden type="file" accept=".bib,.ris,.csv" onChange={(event: ChangeEvent<HTMLInputElement>) => {
                const file = event.target.files?.[0];
                event.target.value = "";
                if (file)
                    void importFile(file);
            }}/>
            </div>
            <Input.TextArea aria-label="文献题录内容" rows={7} value={bibliography} disabled={disabled || searching} placeholder={format === "csv" ? "题名,作者,出处,年份,DOI,arXiv,标签,文件夹" : format === "bib" ? "@article{...}\n\n在这里粘贴 BibTeX 题录" : "TY  - JOUR\nTI  - ...\nER  -"} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => { setBibliography(event.target.value); clearImportResult(); }}/>
            <div className="paper-library-modal-actions"><Button type="primary" disabled={disabled || searching || !bibliography.trim()} onClick={importBibliography}>识别题录</Button></div>
          </>}
          {pendingArxiv && <section className="paper-library-import-confirm" aria-label="链接导入确认">
            <div className="paper-library-arxiv-source"><span>{pendingArxiv.metadata.identifiers.arxiv ? "ARXIV" : "DOI"}</span><code>{pendingArxiv.metadata.identifiers.arxiv || pendingArxiv.metadata.identifiers.doi}{pendingArxiv.metadata.identifiers.arxiv && /^v[1-9][0-9]*$/.test(pendingArxiv.arxivVersion || '') ? pendingArxiv.arxivVersion : ''}</code>{pendingArxiv.arxivVersion && !pendingArxiv.metadata.identifiers.arxiv && <span>{paperSourceVersionLabels[pendingArxiv.arxivVersion] || pendingArxiv.arxivVersion}</span>}</div>
            <h3>{pendingArxiv.metadata.title}</h3>
            <p className="paper-library-arxiv-authors">{pendingArxiv.metadata.authors.join("、")} · {pendingArxiv.metadata.year}</p>
            {pendingArxiv.metadata.abstract && <p className="paper-library-arxiv-abstract">{pendingArxiv.metadata.abstract}</p>}
            {linkPreview?.fulltext_error && <div role="alert"><p>{linkPreview.fulltext_error.message}。{linkPreview.fulltext_error.recovery}</p><Button disabled={disabled || searching} onClick={importIdentifier}>重新查询全文</Button></div>}
            <p className="paper-library-arxiv-hint">{linkPreview?.fulltext_candidates.length ? "确认后提交下载入库任务，同一论文的不同版本保留为独立附件。" : linkPreview?.fulltext_error ? "本次仅保存题录，不会提交 PDF 下载任务。" : "仅找到文献信息，可先保存题录，后续补充 PDF。"}</p>
            <div className="paper-library-modal-actions"><Button type="primary" loading={searching || saving} disabled={disabled} onClick={commitArxiv}>{searching ? "正在提交…" : linkPreview?.fulltext_candidates.length ? "导入文献与 PDF" : linkPreview?.fulltext_error ? "仅保存题录" : "保存题录"}</Button></div>
          </section>}
          {pending && <section className="paper-library-import-confirm" aria-label="导入确认">
            <h3>{pending.attachTo ? "为已有文献添加 PDF" : pending.fresh.length ? `可导入 ${pending.fresh.length} 篇文献` : "文献已在库中"}</h3>
            <div className="paper-library-import-preview">
              {pending.attachTo && <p>{pending.attachTo.metadata.title}</p>}
              {pending.fresh.map((item) => <p key={item.id}>{item.metadata.title}{item.incomplete && <small>题录待补全</small>}</p>)}
              {pending.skipped.map((item, index) => <p key={`${item.id}-${index}`} className="paper-library-import-skipped">已跳过重复：{item.metadata.title}</p>)}
            </div>
            {pendingPdf && <PdfImportReview metadata={pendingPdf.metadata} target={pending.attachTo} papers={data.papers} candidateIds={pdfTargetCandidates(data.papers, pendingPdf.metadata).map(paper => paper.id)} file={pendingPdf.file} fixedTarget={!!importTargetPaperId} conflict={pdfConflict} requiresConfirmation={pdfRequiresConfirmation} sameVersion={samePdfVersion} confirmed={pdfConfirmed} disabled={disabled || searching || !!pendingPdf.documentId} onTargetChange={selectPdfTarget} onConfirmedChange={setPdfConfirmed}/>}
            {pendingPdf && pdfConflict && <Button disabled={disabled || searching} onClick={() => { setImportTargetPaperId(null); selectPdfTarget(null); }}>作为新论文导入</Button>}
            {(!!pending.fresh.length || !!pending.skipped.length || pending.attachTo) && <div className="paper-library-modal-actions"><Button type="primary" loading={saving || confirmingImport} disabled={disabled || searching || (!!pendingPdf && !pdfReviewComplete)} onClick={commit}>{pending.attachTo ? "添加 PDF" : pending.fresh.length ? "加入论文库" : "确认导入"}</Button></div>}
          </section>}
        </div>
      </Modal>;
}
