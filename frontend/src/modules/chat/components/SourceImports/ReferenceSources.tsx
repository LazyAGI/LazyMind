import { tr } from "./translations";
import { useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { Alert, Button, Pagination, Popover } from "antd";
import { CloseOutlined, FileTextOutlined, LeftOutlined, RightOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { type ChatSource, type ChatSourceCollection, getCitationSources, getSourceDedupKey, getSourceEvidenceText, getSourceFaviconUrl, getSourceOriginalHref, getSourceLabel, getSourceSubtitle, isExternalSource } from "../../utils/sourceAdapter";
import { SourceImportActions, useSourceImports, findInspectedSource, uniquePapers } from "./index";
import type { InspectedSource } from "./api";
import "../AssistantMessage/index.scss";

const SOURCE_ICON_TONES = 6;
const SOURCES_PER_PAGE = 4;

function groupPaperSources(sources: ChatSource[], inspections: InspectedSource[] = []) {
  const groups = new Map<string, { key: string; sources: ChatSource[]; paper?: InspectedSource }>();
  sources.forEach((source, index) => {
    const paper = findInspectedSource(inspections, source);
    // Only server-confirmed identities can merge references. Keep every original
    // source intact so citation IDs, versions and evidence remain available.
    const key = paper?.eligible && paper.paper_key ? `paper:${paper.paper_key}` : `source:${index}`;
    const group = groups.get(key);
    if (group) {
      group.sources.push(source);
      if (group.paper?.needs_pdf_lookup && !paper?.needs_pdf_lookup) group.paper = paper;
    }
    else groups.set(key, { key, sources: [source], paper });
  });
  return [...groups.values()];
}

function getSourceIconTone(source: ChatSource) {
  const value = getSourceSubtitle(source) || getSourceLabel(source);
  let hash = 0;
  for (let index = 0; index < value.length; index += 1) {
    hash = (hash * 31 + value.charCodeAt(index)) | 0;
  }
  return Math.abs(hash) % SOURCE_ICON_TONES;
}

function getSourceIconInitial(source: ChatSource) {
  const value = (getSourceSubtitle(source) || getSourceLabel(source)).trim();
  return value ? value[0].toLocaleUpperCase() : "S";
}

function SourceFavicon({
  source,
  compact = false,
}: {
  source: ChatSource;
  compact?: boolean;
}) {
  const [hasFaviconError, setHasFaviconError] = useState(false);
  const faviconUrl = getSourceFaviconUrl(source);
  const showFavicon = Boolean(faviconUrl && !hasFaviconError);

  return (
    <span
      className={`chat-source-brand-icon tone-${getSourceIconTone(source)}${compact ? " is-compact" : ""}`}
      aria-hidden="true"
    >
      {showFavicon ? (
        <img
          src={faviconUrl}
          alt=""
          loading="lazy"
          referrerPolicy="no-referrer"
          onError={() => setHasFaviconError(true)}
        />
      ) : isExternalSource(source) ? (
        <span className="chat-source-brand-initial">
          {getSourceIconInitial(source)}
        </span>
      ) : (
        <FileTextOutlined />
      )}
    </span>
  );
}

const paperPriority = (paper?: InspectedSource) => {
  if (!paper?.eligible || !paper.paper_key) return 0;
  return paper.needs_pdf_lookup ? 1 : 2;
};

export function ChatSourcePanel({ sources, onClose, embedded = false }: { sources: ChatSource[]; onClose: () => void; embedded?: boolean }) {
  const { t } = useTranslation();
  const imports = useSourceImports();
  useEffect(() => { void imports?.load(); }, [imports?.load]);
  const [selected, setSelected] = useState<ChatSource | null>(null);
  const [versions, setVersions] = useState<ChatSource[] | null>(null);
  const [page, setPage] = useState(1);
  const inspections = imports?.inspections;
  const groups = useMemo(() => groupPaperSources(sources, inspections)
    .sort((a, b) => paperPriority(b.paper) - paperPriority(a.paper)), [sources, inspections]);
  const currentPage = Math.min(page, Math.max(1, Math.ceil(groups.length / SOURCES_PER_PAGE)));
  const start = (currentPage - 1) * SOURCES_PER_PAGE;
  const showList = !versions && !selected;
  const showPagination = groups.length > SOURCES_PER_PAGE;
  const backLabel = selected && versions ? tr("backToVersions") : t("chat.contextPanel.backToSources");
  const available = uniquePapers(groups.flatMap(group => group.paper || []));

  useEffect(() => {
    if (page !== currentPage) setPage(currentPage);
  }, [page, currentPage]);

  return <aside className="chat-source-panel" aria-label={t("chat.references")}>
    {(!embedded || !showList) && <div className="chat-source-panel-header">
      <div className="chat-source-panel-heading">
        {!showList && <Button type="text" icon={<LeftOutlined aria-hidden />} title={backLabel} aria-label={backLabel} onClick={() => {
          if (selected) setSelected(null);
          else setVersions(null);
        }} />}
        <h2 className="chat-source-panel-title">
          {selected ? tr("sourceDetails") : versions ? tr("sourceVersions") : t("chat.references")}
          {showList && groups.length > 0 && <span className="chat-source-panel-count">{groups.length}</span>}
        </h2>
      </div>
      {!embedded && <Button type="text" icon={<CloseOutlined />} onClick={onClose} aria-label={t("common.close")} />}
    </div>}
    {showList && imports?.inspectError && <Alert type="warning" message={tr("inspectionUnavailable")} />}
    {showList && imports && <div className="source-selection-bar">
      {sources.length > 0 && <span>{groups.length < sources.length ? tr("sourceCount", { v0: groups.length, v1: sources.length }) : tr("reference", { v0: sources.length })}</span>}
      {imports.items.length > 0 && <Button type="link" onClick={() => imports.setOpened(true)}>{tr("history")}</Button>}
    </div>}
    <div className={`chat-source-panel-body${showList ? " chat-source-page-body" : ""}`}>
      {selected ? <div className="chat-source-detail">
        <h3>{getSourceLabel(selected)}</h3>
        <small>{getSourceSubtitle(selected)}</small>
        <p>{getSourceEvidenceText(selected) || t("chat.contextPanel.noExcerpt")}</p>
        <SourceImportActions source={selected} />
      </div> : versions ? <>
        <div className="source-versions-summary">
          <h3>{getSourceLabel(versions[0])}</h3>
          <small>{versions.length}{tr("sourcesSuffix")}</small>
        </div>
        <ul className="source-version-list" aria-label={tr("sourceGroup", { v0: getSourceLabel(versions[0]) })}>
          {versions.map((source, index) => <li key={`${getSourceDedupKey(source, index)}:${index}`}>
            <button type="button" className="chat-source-item source-version-item" onClick={() => setSelected(source)}>
              <SourceFavicon source={source} />
              <span className="chat-source-item-copy">
                <strong>{getSourceLabel(source)}</strong>
                <small>{getSourceOriginalHref(source) || getSourceSubtitle(source)}</small>
              </span>
              <RightOutlined className="chat-source-item-arrow" aria-hidden="true" />
            </button>
          </li>)}
        </ul>
      </> : <div className="chat-source-list">
        {groups.slice(start, start + SOURCES_PER_PAGE).map(({ key, sources: groupSources }) => {
          const source = groupSources[0];
          const multiple = groupSources.length > 1;
          return <button key={key} type="button" className="chat-source-item" title={getSourceLabel(source)} onClick={() => {
            if (multiple) setVersions(groupSources);
            else setSelected(source);
          }}>
            <SourceFavicon source={source} />
            <span className="chat-source-item-copy">
              <span className="chat-source-item-heading">
                <span className="chat-source-item-domain">{getSourceSubtitle(source) || t("chat.references")}</span>
                {multiple && <span className="source-version-count">{groupSources.length}{tr("sourcesSuffix")}</span>}
              </span>
              <strong className="chat-source-item-title">{getSourceLabel(source)}</strong>
              <span className="chat-source-item-content">{getSourceEvidenceText(source)}</span>
            </span>
            <RightOutlined className="chat-source-item-arrow" aria-hidden="true" />
          </button>;
        })}
      </div>}
    </div>
    {showList && (showPagination || available.length > 0) && <div className="source-selection-footer">
      {showPagination && <Pagination size="small" current={currentPage} pageSize={SOURCES_PER_PAGE}
        total={groups.length} showLessItems showSizeChanger={false} onChange={setPage} />}
      {available.length > 0 && <Button className="source-import-footer-action" type="primary" onClick={() => imports?.choose(available)}>{tr("add")}</Button>}
    </div>}
  </aside>;
}

export function ReferenceSourcesButton({ sources }: { sources?: ChatSourceCollection }) {
  const { t } = useTranslation();
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const imports = useSourceImports();
  useEffect(() => { if (imports?.opened) setSourcesOpen(false); }, [imports?.opened]);
    const displaySources = getCitationSources(sources);
    const groups = groupPaperSources(displaySources, imports?.inspections);
    if (!displaySources.length) return null;
    return (
      <Popover
        trigger="click"
        placement="topLeft"
        autoAdjustOverflow={{ shiftY: true }}
        align={{ offset: [0, 0] }}
        arrow={false}
        open={sourcesOpen}
        onOpenChange={(open: boolean) => { if (!imports?.choosing) setSourcesOpen(open); }}
        destroyOnHidden
        overlayClassName="chat-sources-popover chat-reference-popover"
        content={
          <div role="dialog" aria-label={t("chat.references")} onKeyDown={event => {
            if (event.key === "Escape") { event.stopPropagation(); setSourcesOpen(false); }
          }}>
            <ChatSourcePanel sources={displaySources} onClose={() => setSourcesOpen(false)} />
          </div>
        }
      >
        <Button
          className="tool-btn source-btn"
          aria-expanded={sourcesOpen}
          aria-haspopup="dialog"
          onKeyDown={(event: KeyboardEvent<HTMLButtonElement>) => { if (event.key === "Escape") setSourcesOpen(false); }}
          aria-label={groups.length ? `${t("chat.references")} (${groups.length})` : t("chat.references")}
        >
          <span className="chat-source-button-icons" aria-hidden="true">
            {groups.slice(0, 3).map(({ sources: [source] }, sourceIndex) => (
              <SourceFavicon
                source={source}
                compact
                key={`${getSourceDedupKey(source, sourceIndex)}:${sourceIndex}`}
              />
            ))}
          </span>
          <span className="chat-source-button-label">
            {t("chat.references")}
          </span>
          {groups.length > 0 && <span className="chat-source-button-count">
            {groups.length}
          </span>}
        </Button>
      </Popover>
    );
  }
