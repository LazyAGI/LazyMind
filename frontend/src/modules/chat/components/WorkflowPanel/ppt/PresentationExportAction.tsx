import { useCallback, useState } from 'react';
import { Modal } from 'antd';
import { useNavigate } from 'react-router-dom';
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
  extractHtmlFromArtifact,
  exportHtmlSlidesAsRasterPdf,
  exportHtmlSlidesAsRasterPptx,
} from './exportHtmlToPptx';

type PresentationExportMode = 'editable' | 'raster' | 'pdf';

interface ExporterDependency {
  id: string;
  settings_url?: string;
}

interface ExporterFormatCapability {
  id: string;
  available: boolean;
  dependency?: ExporterDependency;
}

interface ExporterCapabilities {
  provider_id: string;
  formats: ExporterFormatCapability[];
}

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
  return getTabSlotRevisions(session, tab, slotId).find(
    (slot) => slot.sort_order === sortOrder,
  );
}

async function loadHtmlArtifact(raw: unknown): Promise<string> {
  const inline = extractHtmlFromArtifact(raw);
  if (inline) return inline;
  if (!raw || typeof raw !== 'object') return '';
  const value = raw as Record<string, unknown>;
  if (typeof value.text === 'string') return value.text;
  const path = String(value.path ?? value.url ?? '').trim();
  if (!path) return '';
  const direct = value.url ? resolveCoreAssetUrl(String(value.url)) : '';
  const url = direct && !isExpiredSignedUrl(direct)
    ? direct
    : await resolveMarkdownImageUrlAsync(path);
  const response = await fetch(url);
  if (!response.ok) throw new Error(`Failed to load export input (${response.status})`);
  return response.text();
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
  const pages = await Promise.all(rows.map(async (sortOrder) => {
    const pageRevision = findSlotRevision(session, tab, pageSlotId, sortOrder);
    const notesRevision = notesSlotId
      ? findSlotRevision(session, tab, notesSlotId, sortOrder)
      : undefined;
    return {
      html: await loadHtmlArtifact(pageRevision?.artifact_value),
      notes: artifactText(notesRevision?.artifact_value),
    };
  }));
  return pages.filter((page) => page.html.trim());
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
  const navigate = useNavigate();
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [choiceOpen, setChoiceOpen] = useState(false);
  const formatIds = action.formats?.length
    ? action.formats.filter((format) => FORMAT_MODES[format])
    : ['raster-pptx', 'pdf', 'editable-pptx'];

  const showDependencyPrompt = useCallback((dependency?: ExporterDependency) => {
    Modal.confirm({
      title: t('chat.editablePptRequiredTitle'),
      content: t('chat.editablePptRequiredDesc'),
      okText: t('chat.configureEditablePpt'),
      cancelText: t('common.close'),
      onOk: () => navigate(dependency?.settings_url || '/model-providers/tools#editable-ppt-dependency'),
    });
  }, [navigate, t]);

  const runExport = useCallback(async (mode: PresentationExportMode) => {
    if (exporting) return;
    setExporting(true);
    setExportError(null);
    try {
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
        const capabilitiesResponse = await axiosInstance.get(
          `${BASE_URL}/api/core/exporters/${encodeURIComponent(action.provider)}:capabilities`,
        );
        const capabilities = ((capabilitiesResponse.data as { data?: ExporterCapabilities })?.data
          ?? capabilitiesResponse.data) as ExporterCapabilities;
        const editable = capabilities.formats?.find((format) => format.id === 'editable-pptx');
        if (!editable?.available) {
          showDependencyPrompt(editable?.dependency);
          throw new Error(t('chat.editablePptRequiredDesc'));
        }
        const response = await axiosInstance.post(
          `${BASE_URL}/api/core/exporters/${encodeURIComponent(action.provider)}:export`,
          { format: 'editable-pptx', pages, filename },
          { responseType: 'blob', timeout: 20 * 60 * 1000 },
        );
        downloadBlob(response.data as Blob, filename);
      }
    } catch (error) {
      setExportError(await resolveExportError(error, t('chat.workflowExportFailed')));
    } finally {
      setExporting(false);
    }
  }, [action, exporting, rows, session, showDependencyPrompt, t, tab]);

  return (
    <>
      <div className='composite-toolbar'>
        <button
          type='button'
          className='composite-toolbar__export'
          disabled={exporting}
          onClick={() => setChoiceOpen(true)}
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
