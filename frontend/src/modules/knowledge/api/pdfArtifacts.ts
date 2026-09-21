import { axiosInstance, BASE_URL } from "@/components/request";

interface Envelope<T> { data: T }

export type PdfArtifactKind = "SEARCHABLE_PDF" | "TRANSLATION_PDF";
export type PdfJobStatus = "RUNNING" | "WAITING_DEPENDENCY" | "READY" | "FAILED" | "CANCELLED";

export interface PdfArtifact {
  id: string;
  kind: PdfArtifactKind;
  cache_key: string;
  filename: string;
  target_language?: string;
  provider_type?: string;
  provider?: string;
  model?: string;
  warning_count?: number;
  has_layout?: boolean;
  created_at: string;
}

export interface PdfRenderJob {
  id: string;
  kind: PdfArtifactKind;
  cache_key: string;
  status: PdfJobStatus;
  stage: string;
  progress: number;
  artifact_id?: string;
  depends_on_job_id?: string;
  error_message?: string;
  target_language?: string;
  provider_type?: string;
  provider?: string;
  model?: string;
}

export interface PdfCapabilities {
  is_pdf: boolean;
  reader_ready: boolean;
  pdf_kind: "image_only" | "native_text" | "mixed" | "unknown";
  searchable_artifact?: PdfArtifact;
  translations: PdfArtifact[];
  jobs: PdfRenderJob[];
}

export interface CreatePdfJobResult {
  cache_status: "hit" | "running" | "miss";
  artifact?: PdfArtifact;
  job?: PdfRenderJob;
}

export function isActivePdfJob(job?: Pick<PdfRenderJob, "status">): boolean {
  return Boolean(job && ["RUNNING", "WAITING_DEPENDENCY"].includes(job.status));
}

export function latestActiveTranslationJob(jobs: PdfRenderJob[]): PdfRenderJob | undefined {
  return [...jobs].reverse().find((job) => job.kind === "TRANSLATION_PDF" && isActivePdfJob(job));
}

export function latestTranslationJob(jobs: PdfRenderJob[]): PdfRenderJob | undefined {
  return [...jobs].reverse().find((job) => job.kind === "TRANSLATION_PDF");
}

const documentBase = (datasetId: string, documentId: string) =>
  `${BASE_URL}/api/core/datasets/${encodeURIComponent(datasetId)}/documents/${encodeURIComponent(documentId)}`;

export async function getPdfCapabilities(datasetId: string, documentId: string): Promise<PdfCapabilities> {
  const response = await axiosInstance.get<Envelope<PdfCapabilities>>(`${documentBase(datasetId, documentId)}/pdf-capabilities`, { silentError: true } as never);
  return response.data.data;
}

export async function createSearchablePdfJob(datasetId: string, documentId: string, force = false): Promise<CreatePdfJobResult> {
  const response = await axiosInstance.post<Envelope<CreatePdfJobResult>>(`${documentBase(datasetId, documentId)}/pdf-artifacts/searchable`, { force });
  return response.data.data;
}

export async function ensureDocumentParsed(datasetId: string, documentId: string): Promise<{ status: "parsed" | "parsing" | "failed"; task_id?: string }> {
  const response = await axiosInstance.post<Envelope<{ status: "parsed" | "parsing" | "failed"; task_id?: string }>>(
    `${documentBase(datasetId, documentId)}:ensure-parsed`,
    {},
  );
  return response.data.data || response.data as unknown as { status: "parsed" | "parsing" | "failed"; task_id?: string };
}

export async function createTranslationPdfJob(datasetId: string, documentId: string, input: {
  target_language: string;
  provider_type: "api" | "llm";
  provider: string;
  model?: string;
  options_hash?: string;
  force?: boolean;
  source?: Blob;
  source_filename?: string;
  layout_blocks?: PdfLayoutBlock[];
}): Promise<CreatePdfJobResult> {
  if (!input.source || !input.source_filename) {
    const response = await axiosInstance.post<Envelope<CreatePdfJobResult>>(`${documentBase(datasetId, documentId)}/pdf-translations`, input);
    return response.data.data;
  }
  const body = new FormData();
  body.append("target_language", input.target_language);
  body.append("provider_type", input.provider_type);
  body.append("provider", input.provider);
  if (input.model) body.append("model", input.model);
  if (input.options_hash) body.append("options_hash", input.options_hash);
  body.append("force", String(Boolean(input.force)));
  body.append("source", input.source, input.source_filename);
  if (input.layout_blocks) {
    body.append("layout_manifest", new Blob([JSON.stringify({ version: 1, blocks: input.layout_blocks })], { type: "application/json" }), "layout.json");
  }
  const response = await axiosInstance.post<Envelope<CreatePdfJobResult>>(`${documentBase(datasetId, documentId)}/pdf-translations`, body, {
    headers: { "Content-Type": "multipart/form-data" },
    timeout: 5 * 60 * 1000,
  });
  return response.data.data;
}

export async function deletePdfArtifact(datasetId: string, documentId: string, artifactId: string): Promise<void> {
  await axiosInstance.delete(`${documentBase(datasetId, documentId)}/pdf-artifacts/${encodeURIComponent(artifactId)}`);
}

export async function updatePdfRenderJob(datasetId: string, documentId: string, jobId: string, input: {
  status: PdfJobStatus;
  stage: string;
  progress: number;
  error_message?: string;
}): Promise<PdfRenderJob> {
  const response = await axiosInstance.patch<Envelope<PdfRenderJob>>(`${documentBase(datasetId, documentId)}/pdf-render-jobs/${encodeURIComponent(jobId)}`, input);
  return response.data.data;
}

export async function completePdfRenderJob(datasetId: string, documentId: string, jobId: string, blob: Blob, filename: string, warningCount = 0, layoutBlocks?: PdfLayoutBlock[]): Promise<PdfArtifact> {
  const body = new FormData();
  body.append("file", blob, filename);
  body.append("warning_count", String(warningCount));
  if (layoutBlocks) {
    body.append("layout_manifest", new Blob([JSON.stringify({ version: 1, blocks: layoutBlocks })], { type: "application/json" }), "layout.json");
  }
  const response = await axiosInstance.post<Envelope<PdfArtifact>>(`${documentBase(datasetId, documentId)}/pdf-render-jobs/${encodeURIComponent(jobId)}:complete`, body, {
    headers: { "Content-Type": "multipart/form-data" },
    timeout: 5 * 60 * 1000,
  });
  return response.data.data;
}

export function pdfArtifactContentUrl(datasetId: string, documentId: string, artifactId: string): string {
  return `${documentBase(datasetId, documentId)}/pdf-artifacts/${encodeURIComponent(artifactId)}:content`;
}

export async function getPdfArtifactData(datasetId: string, documentId: string, artifactId: string): Promise<ArrayBuffer> {
  const response = await axiosInstance.get<ArrayBuffer>(pdfArtifactContentUrl(datasetId, documentId, artifactId), { responseType: "arraybuffer" });
  return response.data;
}

export async function getPdfData(url: string): Promise<ArrayBuffer> {
  const response = await axiosInstance.get<ArrayBuffer>(url, { responseType: "arraybuffer" });
  return response.data;
}

export async function getPdfArtifactLayout(datasetId: string, documentId: string, artifactId: string): Promise<PdfLayoutBlock[]> {
  const response = await axiosInstance.get<{ version: number; blocks: PdfLayoutBlock[] }>(
    `${documentBase(datasetId, documentId)}/pdf-artifacts/${encodeURIComponent(artifactId)}:layout`,
  );
  return response.data.blocks || [];
}

export async function translateTextWithLLM(text: string, targetLanguage: string): Promise<string> {
  const id = `pdf-translate-${crypto.randomUUID()}`.slice(0, 36);
  const language = targetLanguage === "en" ? "English" : "简体中文";
  const prompt = `把下面文本准确、自然地翻译成${language}。只输出译文，保留公式、编号、引用标记和专有名词，不要解释。\n\n${text}`;
  const response = await axiosInstance.post<Envelope<{ message: string }>>(`${BASE_URL}/api/core/conversations:chat`, {
    action: "CHAT_CONVERSATIONS_ACTION_CREATE",
    conversation_id: id,
    conversation: { display_name: "PDF 文档翻译", search_config: { dataset_list: [] } },
    models: ["LazyMind"],
    surface: "knowledge_pdf_translation",
    stream: false,
    input: [{ input_type: "text", text: prompt }],
    thinking_depth: "low",
    mode: "auto",
    basic_chat_only: true,
    initial_conversation_settings: { enable_workflow: false, enable_subagent: false, ephemeral: true },
  }, { timeout: 10 * 60 * 1000 });
  return response.data.data.message.trim();
}

export function splitTranslationText(text: string, maxRunes: number): string[] {
  const runes = Array.from(text);
  const chunks: string[] = [];
  for (let start = 0; start < runes.length; start += maxRunes) {
    chunks.push(runes.slice(start, start + maxRunes).join(""));
  }
  return chunks;
}

export interface PdfLayoutBlock {
  id: string;
  page: number;
  text: string;
  bbox?: [number, number, number, number];
  pageWidth?: number;
  pageHeight?: number;
  type?: string;
}

export function isRasterLayoutBlock(block: Pick<PdfLayoutBlock, "type">): boolean {
  return /image|figure|chart|diagram|formula|equation/i.test(block.type || "");
}

export function validatePdfLayoutBlocks(blocks: PdfLayoutBlock[], pageCount: number): void {
  const positioned = blocks.filter((block) => block.bbox);
  const malformed = positioned.some((block) => {
    const height = block.bbox ? block.bbox[3] - block.bbox[1] : 0;
    return Array.from(block.text).length > 500 && height > 0 && height < 100;
  });
  const coveredPages = new Set(positioned.map((block) => block.page)).size;
  const minimumCoverage = Math.max(1, Math.ceil(pageCount * 0.6));
  if (!positioned.length || malformed || coveredPages < minimumCoverage) {
    throw new Error("Reader 布局结果不完整，无法可靠重建 PDF；请在“重新解析”中选择“全部重新解析（MinerU）”后再试");
  }
}

export async function listPdfLayoutBlocks(datasetId: string, documentId: string): Promise<PdfLayoutBlock[]> {
  const blocks: PdfLayoutBlock[] = [];
  let pageToken = "";
  do {
    const response = await axiosInstance.get<Envelope<{ segments: Array<{ segment_id: string; content?: string; display_content?: string; text?: string; meta?: string; metadata?: Record<string, unknown> }>; next_page_token?: string }>>(
      `${documentBase(datasetId, documentId)}/segments`,
      // Layout reconstruction needs the Reader's line-level DocNodes. The
      // block group can contain multi-page retrieval chunks whose bbox only
      // describes the first line and therefore cannot be used for rendering.
      { params: { page_size: 100, page_token: pageToken || undefined, group: "line" }, silentError: true } as never,
    );
    const data = response.data.data || (response.data as unknown as { segments: []; next_page_token?: string });
    for (const segment of data.segments || []) {
      let meta: Record<string, unknown> = segment.metadata || {};
      if (segment.meta) {
        try { meta = { ...meta, ...JSON.parse(segment.meta) }; } catch { /* keep structured metadata */ }
      }
      const rawBBox = meta.bbox;
      const bbox = Array.isArray(rawBBox) && rawBBox.length === 4 && rawBBox.every((item) => Number.isFinite(Number(item)))
        ? rawBBox.map(Number) as [number, number, number, number]
        : undefined;
      const pageNo = meta.page_no == null ? Number(meta.page ?? 0) + 1 : Number(meta.page_no);
      const segmentRecord = segment as typeof segment & {
        image_key?: string; image_uri?: string; image_keys?: string[]; segment_type?: unknown; display_type?: unknown;
      };
      const hasImage = Boolean(segmentRecord.image_key || segmentRecord.image_uri || segmentRecord.image_keys?.length);
      blocks.push({
        id: segment.segment_id,
        page: Math.max(1, Number.isFinite(pageNo) ? pageNo : 1),
        text: String(segment.display_content || segment.content || segment.text || "").trim(),
        bbox,
        pageWidth: Number(meta.page_width ?? meta.width) || undefined,
        pageHeight: Number(meta.page_height ?? meta.height) || undefined,
        type: String(meta.type || meta.block_type || segmentRecord.display_type || segmentRecord.segment_type || (hasImage ? "image" : "paragraph")),
      });
    }
    pageToken = data.next_page_token || "";
  } while (pageToken);
  return blocks.filter((block) => block.text || isRasterLayoutBlock(block));
}
