import { useState, useEffect, type KeyboardEvent } from "react";
import { Alert, Button, Checkbox, Pagination, Popover } from "antd";
import { CloseOutlined, DownOutlined, FileTextOutlined, LeftOutlined, RightOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { type ChatSource, type ChatSourceCollection, getCitationSources, getSourceDedupKey, getSourceEvidenceText, getSourceFaviconUrl, getSourceHref, getSourceLabel, getSourceSubtitle, isExternalSource } from "../../utils/sourceAdapter";
import { SourceImportActions, useSourceImports, findInspectedSource, uniquePapers } from "./index";
import type { InspectedSource } from "./api";
import "../AssistantMessage/index.scss";

const SOURCE_ICON_TONES = 6;

function groupPaperSources(sources: ChatSource[], inspections: InspectedSource[] = []) {
  const groups = new Map<string, { key: string; sources: ChatSource[]; paper?: InspectedSource }>();
  sources.forEach((source, index) => {
    const paper = findInspectedSource(inspections, source);
    // Only server-confirmed identities can merge references. Keep every original
    // source intact so citation IDs, versions and evidence remain available.
    const key = paper?.eligible && paper.paper_key ? `paper:${paper.paper_key}` : `source:${index}`;
    const group = groups.get(key);
    if (group) group.sources.push(source);
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

export function ChatSourcePanel({ sources, onClose, embedded = false }: { sources: ChatSource[]; onClose: () => void; embedded?: boolean }) {
  const { t } = useTranslation();
  const imports = useSourceImports();
  const [selected, setSelected] = useState<ChatSource | null>(null);
  const [multi, setMulti] = useState(false);
  const [page, setPage] = useState(1);
  const [keys, setKeys] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const groups = groupPaperSources(sources, imports?.inspections);
  const currentPage = Math.min(page, Math.max(1, Math.ceil(groups.length / 20)));
  useEffect(() => { setPage(currentPage); }, [currentPage]);
  const rows = groups.slice((currentPage - 1) * 20, currentPage * 20);
  const available = uniquePapers(rows.flatMap(group => group.paper || []));
  const chosen = uniquePapers(groups.flatMap(group => group.paper && keys.has(group.paper.paper_key || "") ? [group.paper] : []));
  const toggle = (key: string, checked: boolean) => setKeys(current => { const next = new Set(current); checked ? next.add(key) : next.delete(key); return next; });
  return <aside className="chat-source-panel" aria-label={t("chat.references")}>
    {(!embedded || selected) && <div className="chat-source-panel-header">
      <div className="chat-source-panel-heading">
        {selected && <Button type="text" icon={<LeftOutlined aria-hidden />} title={t("chat.contextPanel.backToSources")} aria-label={t("chat.contextPanel.backToSources")} onClick={() => setSelected(null)} />}
        {!embedded && <h2 className="chat-source-panel-title">{t("chat.references")}<span className="chat-source-panel-count">{groups.length}</span></h2>}
      </div>
      {!embedded && <Button type="text" icon={<CloseOutlined />} onClick={onClose} aria-label={t("common.close")} />}
    </div>}
    {imports?.inspectError && <Alert type="warning" message="暂时无法检查文献是否可加入，请重新打开来源" />}
    {!selected && imports && <div className="source-selection-bar">
      {multi ? <Checkbox checked={available.length > 0 && available.every(row => keys.has(row.paper_key!))} indeterminate={available.some(row => keys.has(row.paper_key!)) && !available.every(row => keys.has(row.paper_key!))} disabled={!available.length} onChange={(e: { target: { checked: boolean } }) => setKeys(current => { const next = new Set(current); available.forEach(row => e.target.checked ? next.add(row.paper_key!) : next.delete(row.paper_key!)); return next; })}>全选本页</Checkbox> : <span>{groups.length < sources.length ? `${groups.length} 项 · ${sources.length} 个来源` : `参考来源 ${sources.length}`}</span>}
      <div>{imports.items.length > 0 && <Button type="link" onClick={() => imports.setOpened(true)}>入库记录</Button>}<Button type="link" onClick={() => { setMulti(value => !value); setKeys(new Set()); }}>{multi ? "取消多选" : "多选"}</Button></div>
    </div>}
    <div className="chat-source-panel-body">
      {selected ? <div className="chat-source-detail"><h3>{getSourceLabel(selected)}</h3><small>{getSourceSubtitle(selected)}</small><p>{getSourceEvidenceText(selected) || t("chat.contextPanel.noExcerpt")}</p><SourceImportActions source={selected} /></div> : <div className="chat-source-list">
        {rows.map(group => {
          const source = group.sources[0];
          const { paper } = group;
          const multiple = group.sources.length > 1;
          const isExpanded = expanded.has(group.key);
          return <div key={group.key}>
            <div className={multi ? "source-select-row" : ""}>
              {multi && (paper?.eligible && paper.paper_key ? <Checkbox aria-label={`选择 ${getSourceLabel(source)}`} checked={keys.has(paper.paper_key)} onChange={(e: { target: { checked: boolean } }) => toggle(paper.paper_key!, e.target.checked)} /> : <span style={{ width: 16 }} title={paper?.reason || "此来源不可加入知识库"} />)}
              <button type="button" className="chat-source-item" aria-expanded={multiple ? isExpanded : undefined} onClick={() => {
                if (!multiple) { setSelected(source); return; }
                setExpanded(current => {
                  const next = new Set(current);
                  next.has(group.key) ? next.delete(group.key) : next.add(group.key);
                  return next;
                });
              }} title={getSourceLabel(source)}>
                <SourceFavicon source={source} />
                <span className="chat-source-item-copy">
                  <span className="chat-source-item-heading">{getSourceSubtitle(source) || t("chat.references")}</span>
                  <strong className="chat-source-item-title">{getSourceLabel(source)}</strong>
                  {getSourceEvidenceText(source) && <span className="chat-source-item-content">{getSourceEvidenceText(source)}</span>}
                  {multiple && <span className="source-group-toggle">{isExpanded ? "收起" : "展开"} {group.sources.length} 个来源</span>}
                </span>
                {multiple && isExpanded ? <DownOutlined className="chat-source-item-arrow" aria-hidden="true" /> : <RightOutlined className="chat-source-item-arrow" aria-hidden="true" />}
              </button>
            </div>
            {multiple && isExpanded && <ul className="source-group-links" aria-label={`${getSourceLabel(source)} 的来源`}>
              {group.sources.map((entry, index) => <li key={`${getSourceDedupKey(entry, index)}:${index}`}>
                <button type="button" className="source-group-title" title={getSourceLabel(entry)} onClick={() => setSelected(entry)}>{getSourceLabel(entry)}</button>
                <a href={getSourceHref(entry)} target="_blank" rel="noopener noreferrer">{entry.url}</a>
              </li>)}
            </ul>}
          </div>;
        })}
      </div>}
    </div>
    {!selected && groups.length > 20 && <div className="source-selection-footer"><Pagination size="small" current={currentPage} pageSize={20} total={groups.length} showSizeChanger={false} onChange={setPage} /></div>}
    {multi && <div className="source-selection-footer"><span>已选择 {chosen.length} 篇 <Button type="link" size="small" onClick={() => setKeys(new Set())}>清空</Button></span><Button type="primary" disabled={!chosen.length} onClick={() => imports?.choose(chosen)}>批量加入知识库</Button></div>}
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
        open={sourcesOpen}
        onOpenChange={setSourcesOpen}
        destroyOnHidden
        overlayClassName="chat-sources-popover"
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
          aria-label={`${t("chat.references")} (${groups.length})`}
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
          <span className="chat-source-button-count">
            {groups.length}
          </span>
        </Button>
      </Popover>
    );
  }
