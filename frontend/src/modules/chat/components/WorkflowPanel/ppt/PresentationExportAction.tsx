import { embedSlideAssetsForExport } from './slideAssets';
import { useCallback, useEffect, useState } from 'react';
import { Checkbox, Modal } from 'antd';
import { useTranslation } from 'react-i18next';
import { axiosInstance, BASE_URL } from '@/components/request';
import type {
  SlotRevision,
  TabDef,
  WorkflowSession,
  WorkflowTabAction,
} from '@/modules/chat/store/workflowPanel';
import { resolveCoreAssetUrl, resolveMarkdownImageUrlAsync, isExpiredSignedUrl } from '@/modules/knowledge/utils/imageUrl';
import {
  declaredSlideFontFamilies,
  extractHtmlFromArtifact,
  exportHtmlSlidesAsRasterPdf,
  exportHtmlSlidesAsRasterPptx,
} from './exportHtmlToPptx';

type PresentationExportMode = 'editable' | 'raster' | 'pdf';

function getTabSlotRevisions(
  session: WorkflowSession,
  tab: TabDef,
  slotId: string,
): SlotRevision[] {
  const slots = session.slots ?? [];
  if (tab.step_id) {
    return slots.filter((slot) => slot.slot === slotId && slot.step_id === tab.step_id);
  }
  const isStepTab = session.steps?.some((step) => step.step_id === tab.id);
  if (isStepTab) {
    return slots.filter((slot) => slot.slot === slotId && slot.step_id === tab.id);
  }
  return slots.filter((slot) => slot.slot === slotId && slot.selected);
}

function findSlotRevision(
  session: WorkflowSession,
  tab: TabDef,
  slotId: string,
  sortOrder: number,
): SlotRevision | undefined {
  const revisions = getTabSlotRevisions(session, tab, slotId);
  return revisions.find(slot => slot.selected && slot.sort_order === sortOrder)
    ?? revisions.find(slot => slot.sort_order === sortOrder);
}

// Prefer the selected native delivery when it represents the complete visible
// deck. Re-converting its HTML loses native charts and can require another service.
export function selectedNativePresentation(action: WorkflowTabAction, tab: TabDef, session: WorkflowSession, rows: number[]): SlotRevision | undefined {
  const pageSlots = getTabSlotRevisions(session, tab, action.inputs.pages).filter(slot => slot.selected);
  if (pageSlots.length !== rows.length || pageSlots.some(slot => !rows.includes(slot.sort_order ?? -1))) return undefined;
  const declared = action.inputs.native_file ? [action.inputs.native_file] : tab.slots.map(slot => slot.id);
  const candidates = (session.slots ?? []).filter(slot => {
    const value = slot.artifact_value;
    const filename = String(value?.filename ?? value?.name ?? value?.path ?? value?.url ?? '').split('?')[0];
    return slot.selected && declared.includes(slot.slot) && slot.content_type === 'file' && /\.pptx$/i.test(filename);
  });
  if (candidates.length !== 1) return undefined;
  const native = candidates[0];
  const inputs = [...pageSlots, ...getTabSlotRevisions(session, tab, action.inputs.notes ?? '').filter(slot => slot.selected)];
  if (inputs.some(slot => (slot.draft_version ?? 1) > 1 || (!(native.producer_attempt_id && slot.producer_attempt_id === native.producer_attempt_id) && Date.parse(slot.created_at) > Date.parse(native.created_at)))) return undefined;
  return native;
}

async function loadExportText(raw: unknown): Promise<string> {
  if (typeof raw === 'string') return raw;
  if (!raw || typeof raw !== 'object') return '';
  const value = raw as Record<string, unknown>;
  if (typeof value.text === 'string') return value.text;
  // Completed edits from older Connectors uploaded text files in the same
  // envelope as binary artifacts. Decode their bytes, not their metadata.
  if (value.storage === 'inline_base64' && typeof value.content_base64 === 'string') {
    const bytes = Uint8Array.from(atob(value.content_base64), char => char.charCodeAt(0));
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  }
  const path = String(value.path ?? value.url ?? '').trim();
  if (!path) return artifactText(raw);
  const direct = value.url ? resolveCoreAssetUrl(String(value.url)) : '';
  const url = direct && !isExpiredSignedUrl(direct)
    ? direct
    : await resolveMarkdownImageUrlAsync(path);
  const response = await fetch(url);
  if (!response.ok) throw Object.assign(new Error("Failed to load export input"), { response });
  return response.text();
}

class ExportPageError extends Error {
  constructor(readonly page: number, readonly cause?: unknown) {
    super('Export page unavailable');
  }
}

function isApplicationShell(html: string): boolean {
  const document = new DOMParser().parseFromString(html, 'text/html');
  const root = document.querySelector('body > #root, body > #app');
  return Boolean(root && !root.children.length && !root.textContent?.trim()
    && document.querySelector('script[type="module"][src]'));
}

function artifactText(raw: unknown): string {
  if (typeof raw === 'string') return raw;
  if (!raw || typeof raw !== 'object') return '';
  const value = raw as Record<string, unknown>;
  return String(value.text ?? value.content ?? value.value ?? '');
}

async function collectPresentationPages(
  action: WorkflowTabAction,
  tab: TabDef,
  session: WorkflowSession,
  rows: number[],
): Promise<Array<{ html: string; notes: string }>> {
  const pageSlotId = action.inputs.pages;
  if (!pageSlotId) return [];
  const notesSlotId = action.inputs.notes;
  return Promise.all(rows.map(async (sortOrder, index) => {
    const pageRevision = findSlotRevision(session, tab, pageSlotId, sortOrder);
    const notesRevision = notesSlotId
      ? findSlotRevision(session, tab, notesSlotId, sortOrder)
      : undefined;
    try {
      const text = await loadExportText(pageRevision?.artifact_value);
      const html = extractHtmlFromArtifact(text);
      // Never silently drop a missing/invalid page or export an SPA fallback
      // returned with HTTP 200 in place of the requested artifact.
      if (!html || isApplicationShell(html)) {
        throw new ExportPageError(index + 1);
      }
      return { html, notes: await loadExportText(notesRevision?.artifact_value) };
    } catch (error) {
      throw error instanceof ExportPageError ? error : new ExportPageError(index + 1, error);
    }
  }));
}

function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

async function resolveExportError(error: unknown, fallback: string): Promise<string> {
  const responseData = (error as { response?: { data?: unknown } })?.response?.data;
  if (responseData instanceof Blob) {
    try {
      const text = await responseData.text();
      const parsed = JSON.parse(text) as { message?: string; detail?: string };
      return parsed.message || parsed.detail || text || fallback;
    } catch {
      return fallback;
    }
  }
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}

const FORMAT_MODES: Record<string, PresentationExportMode> = {
  'raster-pptx': 'raster',
  pdf: 'pdf',
  'editable-pptx': 'editable',
};

export function PresentationExportAction({
  action,
  tab,
  session,
  rows,
}: {
  action: WorkflowTabAction;
  tab: TabDef;
  session: WorkflowSession;
  rows: number[];
}) {
  const { t } = useTranslation();
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [choiceOpen, setChoiceOpen] = useState(false);
  const [embedFonts, setEmbedFonts] = useState(true);
  const [fontFamilies, setFontFamilies] = useState<string[]>([]);
  const formatIds = action.formats?.length
    ? action.formats.filter((format) => FORMAT_MODES[format])
    : ['raster-pptx', 'pdf', 'editable-pptx'];
  const editableOffered = formatIds.includes('editable-pptx');

  useEffect(() => {
    if (!choiceOpen || !editableOffered) return;
    let current = true;
    void collectPresentationPages(action, tab, session, rows)
      .then(pages => declaredSlideFontFamilies(pages.map(page => page.html)))
      .then(families => { if (current) setFontFamilies(families); })
      .catch(() => { if (current) setFontFamilies([]); });
    return () => { current = false; };
  }, [action, choiceOpen, editableOffered, rows, session, tab]);

  const runExport = useCallback(async (mode: PresentationExportMode) => {
    if (exporting) return;
    setExporting(true);
    setExportError(null);
    try {
      const native = mode === 'editable' ? selectedNativePresentation(action, tab, session, rows) : undefined;
      if (native) {
        const value = native.artifact_value as Record<string, unknown>;
        const direct = value.url ? resolveCoreAssetUrl(String(value.url)) : '';
        const url = direct && !isExpiredSignedUrl(direct) ? direct : await resolveMarkdownImageUrlAsync(String(value.path ?? value.url ?? ''));
        const response = await fetch(url);
        if (!response.ok) throw new Error(t('chat.workflowExportFailed'));
        downloadBlob(await response.blob(), String(value.filename ?? value.name ?? 'presentation.pptx'));
        return;
      }
      const pages = await collectPresentationPages(action, tab, session, rows);
      if (!pages.length) throw new Error(t('chat.workflowExportNoHtml'));
      const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
      const filename = `presentation_${stamp}.${mode === 'pdf' ? 'pdf' : 'pptx'}`;
      const slideInputs = pages.map((page, index) => ({ ...page, pageNo: index + 1 }));
      if (mode === 'pdf') {
        await exportHtmlSlidesAsRasterPdf(slideInputs, filename, { sessionId: session.session_id });
      } else if (mode === 'raster') {
        await exportHtmlSlidesAsRasterPptx(slideInputs, filename, { sessionId: session.session_id });
      } else {
        const exportPages = await Promise.all(pages.map(async page => ({
          ...page, html: await embedSlideAssetsForExport(page.html, { embedFonts: false }),
        })));
        const response = await axiosInstance.post(
          `${BASE_URL}/api/core/exporters/${encodeURIComponent(action.provider)}:export`,
          { format: 'editable-pptx', pages: exportPages, filename, embed_fonts: embedFonts },
          { responseType: 'blob', timeout: 20 * 60 * 1000 },
        );
        downloadBlob(response.data as Blob, filename);
      }
    } catch (error) {
      setExportError(error instanceof ExportPageError
        ? t('chat.workflowExportPageFailed', { page: error.page }) + ' ' +
          await resolveExportError(error.cause, t('chat.workflowExportInvalidHtml'))
        : await resolveExportError(error, t('chat.workflowExportFailed')));
    } finally {
      setExporting(false);
    }
  }, [action, embedFonts, exporting, rows, session, t, tab]);

  return (
    <>
      <div className='composite-toolbar'>
        <button
          type='button'
          className='composite-toolbar__export'
          disabled={exporting}
          onClick={() => {
            setEmbedFonts(true);
            setFontFamilies([]);
            setChoiceOpen(true);
          }}
        >
          {exporting ? t('chat.workflowExportingPptx') : (action.label || t('chat.workflowExportPptx'))}
        </button>
        {exportError && <span className='composite-toolbar__error'>{exportError}</span>}
      </div>
      <Modal
        open={choiceOpen}
        onCancel={() => setChoiceOpen(false)}
        footer={null}
        title={t('chat.workflowExportSelectModeTitle')}
      >
        <div className='composite-toolbar__export-mode'>
          <p>{t('chat.workflowExportSelectModeDesc')}</p>
          {editableOffered && (
            <>
              <Checkbox checked={embedFonts} onChange={event => setEmbedFonts(event.target.checked)}>
                {t('chat.workflowExportEmbedFonts')}
              </Checkbox>
              {fontFamilies.length > 0 && (
                <p>{t('chat.workflowExportDeclaredFonts', { fonts: fontFamilies.join(', ') })}</p>
              )}
              <p>{t('chat.workflowExportUnembeddedFontsHelp')}</p>
            </>
          )}
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 12 }}>
            {formatIds.map((formatId) => {
              const mode = FORMAT_MODES[formatId];
              return (
                <button
                  key={formatId}
                  type='button'
                  className={`workflow-panel__action-btn workflow-panel__action-btn--${mode === 'editable' ? 'primary' : 'secondary'}`}
                  onClick={() => {
                    setChoiceOpen(false);
                    void runExport(mode);
                  }}
                >
                  {t(`chat.workflowExportMode${mode[0].toUpperCase()}${mode.slice(1)}`)}
                </button>
              );
            })}
          </div>
        </div>
      </Modal>
    </>
  );
}
