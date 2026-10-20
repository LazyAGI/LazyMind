import { useMemo, useRef, useState } from "react";
import { Button, Input, Popconfirm, Radio, Select, message } from "antd";
import {
  applyPaperEdits,
  assignFolders,
  citationLabel,
  classifyIdentifier,
  duplicateOf,
  filterPapers,
  formatCitation,
  movePapers,
  paperLink,
  papersInView,
  venueName,
  parseBibTeX,
  parseCSV,
  parseRIS,
  planImport,
  removePapers,
  sortPapers,
  sourceLabel,
  statusLabel,
  toBibTeX,
  toCSV,
  toRIS,
  usablePaper,
  type PaperEdits,
  type YearOrder,
  type CitationStyle,
  type FilterCondition,
  type ImportSource,
  type MatchMode,
  type PaperItem,
  type PaperLibraryData,
  type ReadingStatus,
} from "./model";
import { loadPaperLibrary, savePaperLibrary } from "./store";
import { lookupIdentifier, searchSemanticScholar, searchZotero } from "./scholar";
import { paperFromPdf } from "./pdfMeta";
import "./PaperLibrary.scss";

interface Props {
  datasetId: string;
  preview?: boolean;
  onUploadPdf?: () => void;
}

type Sheet = "import" | "filter" | "review" | "edit" | null;

function download(filename: string, content: string) {
  const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  URL.revokeObjectURL(url);
}

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

export default function PaperLibrary({ datasetId, preview, onUploadPdf }: Props) {
  const [data, setData] = useState<PaperLibraryData>(() => loadPaperLibrary(datasetId, preview));
  const [folderId, setFolderId] = useState<string | null>(null);
  const [status, setStatus] = useState<ReadingStatus | "all">("all");
  const [conditions, setConditions] = useState<FilterCondition[]>([]);
  const [match, setMatch] = useState<MatchMode>("all");
  const [keyword, setKeyword] = useState("");
  const [yearOrder, setYearOrder] = useState<YearOrder>("library");
  const [selected, setSelected] = useState<string[]>([]);
  const [sheet, setSheet] = useState<Sheet>(null);
  const [editing, setEditing] = useState<PaperItem | null>(null);
  const [bulkFolder, setBulkFolder] = useState<string>("none");
  const [folderName, setFolderName] = useState("");
  const [identifier, setIdentifier] = useState("");
  const [bibliography, setBibliography] = useState("");
  const [bibliographyKind, setBibliographyKind] = useState<ImportSource>("google_scholar");
  const [format, setFormat] = useState<"bib" | "ris" | "csv">("bib");
  const [query, setQuery] = useState("");
  const [zoteroUser, setZoteroUser] = useState("");
  const [zoteroKey, setZoteroKey] = useState("");
  const [hits, setHits] = useState<PaperItem[]>([]);
  const [searching, setSearching] = useState(false);
  const [pending, setPending] = useState<{ fresh: PaperItem[]; skipped: PaperItem[]; folders: PaperLibraryData["folders"] } | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const persist = (next: PaperLibraryData) => {
    setData(next);
    savePaperLibrary(datasetId, next);
  };

  const scoped = useMemo(() => papersInView(data, folderId), [data, folderId]);
  const visible = useMemo(
    () => sortPapers(filterPapers(scoped, status, conditions, match, keyword), yearOrder),
    [scoped, status, conditions, match, keyword, yearOrder],
  );
  const selectedPapers = data.papers.filter((item) => selected.includes(item.id));
  const readyCount = selectedPapers.filter(usablePaper).length;
  const exportItems = selectedPapers.length ? selectedPapers : visible;

  const stage = (items: PaperItem[], folderNames?: Array<string | null>) => {
    const placed = assignFolders(data, items, folderNames || items.map(() => null), folderId);
    const planned = planImport(data.papers, placed.papers);
    setPending({ ...planned, folders: placed.folders });
    if (!planned.fresh.length) message.info(`库中已有，跳过 ${planned.skipped.length} 篇`);
  };

  const commit = () => {
    if (!pending?.fresh.length) return;
    persist({ folders: pending.folders, papers: [...pending.fresh, ...data.papers] });
    message.success(`已加入 ${pending.fresh.length} 篇，跳过 ${pending.skipped.length} 篇`);
    setPending(null);
    setHits([]);
    setSheet(null);
  };

  const addFolder = () => {
    const name = folderName.trim();
    if (!name) return;
    if (data.folders.some((folder) => folder.name === name)) {
      message.info("已有同名文件夹");
      return;
    }
    persist({ ...data, folders: [...data.folders, { id: `folder-${Date.now()}`, name }] });
    setFolderName("");
  };

  const dropPapers = (ids: string[]) => {
    persist({ ...data, papers: removePapers(data.papers, ids) });
    setSelected(selected.filter((id) => !ids.includes(id)));
    if (editing && ids.includes(editing.id)) {
      setEditing(null);
      setSheet(null);
    }
  };

  const placePapers = (ids: string[], folder: string) => {
    persist({ ...data, papers: movePapers(data.papers, ids, folder === "none" ? null : folder) });
  };

  const saveEdits = (edits: PaperEdits) => {
    if (!editing) return;
    const next = applyPaperEdits(editing, edits);
    const clash = duplicateOf(data.papers.filter((item) => item.id !== editing.id), next);
    if (clash) {
      message.info(`库中已有：${clash.title}`);
      return;
    }
    persist({ ...data, papers: data.papers.map((item) => item.id === editing.id ? next : item) });
    message.success(next.incomplete ? "题录仍待补全" : "题录已保存");
    setEditing(next);
    setSheet(null);
  };

  const folderOptions = [{ value: "none", label: "未分组" }, ...data.folders.map((folder) => ({ value: folder.id, label: folder.name }))];

  const importBibliography = () => {
    const parsed = format === "ris"
      ? parseRIS(bibliography, bibliographyKind)
      : format === "csv"
        ? parseCSV(bibliography, bibliographyKind)
        : { papers: parseBibTeX(bibliography, bibliographyKind), folderNames: [] as Array<string | null> };
    const papers = "papers" in parsed ? parsed.papers : parsed;
    const folderNames = "folderNames" in parsed ? parsed.folderNames : undefined;
    if (!papers.length) {
      message.error("没有识别到论文");
      return;
    }
    stage(papers, folderNames);
  };

  const importIdentifier = async () => {
    const kind = classifyIdentifier(identifier);
    if (!kind) {
      message.error("请粘贴 DOI 或 arXiv 编号");
      return;
    }
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
      arxiv: kind.kind === "arxiv" ? kind.value : undefined,
    };
    const existing = duplicateOf(data.papers, probe);
    if (existing) {
      setPending({ fresh: [], skipped: [existing], folders: data.folders });
      message.info("库中已有，跳过 1 篇");
      return;
    }
    setSearching(true);
    try {
      stage([await lookupIdentifier(identifier)]);
    } catch (error) {
      message.error(error instanceof Error ? error.message : "没有查到这篇论文");
    } finally {
      setSearching(false);
    }
  };

  const importFile = async (file: File) => {
    const name = file.name.toLowerCase();
    try {
      if (name.endsWith(".pdf")) {
        const item = await paperFromPdf(file);
        stage([item]);
        if (item.incomplete) message.warning("题录不完整，已标成待补全");
        if (!preview) onUploadPdf?.();
        return;
      }
      const text = await file.text();
      if (name.endsWith(".ris")) stage(parseRIS(text, "local"));
      else if (name.endsWith(".csv")) {
        const parsed = parseCSV(text);
        stage(parsed.papers, parsed.folderNames);
      } else stage(parseBibTeX(text, "local"));
    } catch (error) {
      message.error(error instanceof Error ? error.message : "文件没有读出来");
    }
  };

  return (
    <section className="paper-library">
      <header className="paper-library-head">
        <div>
          <p className="paper-library-lead">{data.papers.length} 篇。阅读状态只属于你。研究方向用单层文件夹分开。</p>
        </div>
        <div className="paper-library-actions">
          <Button type="primary" onClick={() => { setPending(null); setSheet("import"); }}>导入</Button>
          <Button onClick={() => download("papers.bib", toBibTeX(exportItems))}>BibTeX</Button>
          <Button onClick={() => download("papers.ris", toRIS(exportItems))}>RIS</Button>
          <Button onClick={() => download("papers.csv", toCSV(exportItems, data.folders))}>CSV</Button>
        </div>
      </header>
      <div className="paper-library-layout">
        <nav className="paper-library-rail" aria-label="研究方向">
          <FolderButton name="全部" count={data.papers.length} current={!folderId} onClick={() => setFolderId(null)} />
          {data.folders.map((folder) => {
            const count = data.papers.filter((item) => item.folderId === folder.id).length;
            return (
              <FolderButton
                key={folder.id}
                name={folder.name}
                count={count}
                current={folderId === folder.id}
                onClick={() => setFolderId(folder.id)}
                onRemove={count === 0 ? () => {
                  persist({ ...data, folders: data.folders.filter((item) => item.id !== folder.id) });
                  if (folderId === folder.id) setFolderId(null);
                } : undefined}
              />
            );
          })}
          <div className="paper-library-new-folder">
            <Input value={folderName} placeholder="文件夹名称" onChange={(event) => setFolderName(event.target.value)} onPressEnter={addFolder} />
            <Button onClick={addFolder}>新建</Button>
          </div>
        </nav>
        <div className="paper-library-board">
          <div className="paper-library-filters">
            <Input className="paper-library-search" value={keyword} allowClear placeholder="搜索题名、作者、DOI 或 arXiv" onChange={(event) => setKeyword(event.target.value)} />
            <div className="paper-library-seg" role="group" aria-label="阅读状态">
              {(["all", "unread", "reading", "read"] as const).map((key) => {
                const count = key === "all" ? scoped.length : scoped.filter((item) => item.status === key).length;
                return (
                  <button key={key} type="button" aria-pressed={status === key} onClick={() => setStatus(key)}>
                    {key === "all" ? "全部" : statusLabel[key]} {count}
                  </button>
                );
              })}
            </div>
            {conditions.map((condition) => (
              <span className="paper-library-chip" key={condition.label}>
                {condition.label}
                <button type="button" aria-label="移除条件" onClick={() => setConditions(conditions.filter((item) => item !== condition))}>×</button>
              </span>
            ))}
            <div className="paper-library-seg" role="group" aria-label="排序">
              {([["library", "库内顺序"], ["newest", "最新"], ["oldest", "最早"]] as const).map(([key, label]) => (
                <button key={key} type="button" aria-pressed={yearOrder === key} onClick={() => setYearOrder(key)}>{label}</button>
              ))}
            </div>
            <Button type="text" onClick={() => setSheet("filter")}>添加筛选</Button>
          </div>
          {!visible.length && <div className="paper-library-empty">没有符合条件的论文。可以导入 DOI、arXiv、BibTeX、RIS、CSV，或上传 PDF。</div>}
          {visible.map((item) => (
            <article className={`paper-library-row ${selected.includes(item.id) ? "selected" : ""}`} key={item.id}>
              <input
                type="checkbox"
                aria-label={`选择 ${item.title}`}
                checked={selected.includes(item.id)}
                onChange={(event) => setSelected(event.target.checked ? [...selected, item.id] : selected.filter((id) => id !== item.id))}
              />
              <div className="paper-library-main">
                <div className="paper-library-title-line">
                  <h2 className="paper-library-title">{item.title}</h2>
                  <Select
                    className="paper-library-status"
                    size="small"
                    value={item.status}
                    aria-label={`${item.title} 的阅读状态`}
                    options={(Object.keys(statusLabel) as ReadingStatus[]).map((key) => ({ value: key, label: statusLabel[key] }))}
                    onChange={(value) => persist({ ...data, papers: data.papers.map((paper) => paper.id === item.id ? { ...paper, status: value } : paper) })}
                  />
                  <button
                    type="button"
                    className={`paper-library-star ${item.important ? "on" : ""}`}
                    aria-pressed={item.important}
                    aria-label={item.important ? "取消重要" : "标为重要"}
                    onClick={() => persist({ ...data, papers: data.papers.map((paper) => paper.id === item.id ? { ...paper, important: !paper.important } : paper) })}
                  >{item.important ? "★" : "☆"}</button>
                </div>
                <p className="paper-library-cite">{item.authors} · {venueName(item)} {item.year}</p>
                <div className="paper-library-meta">
                  {item.tags.map((tag) => (
                    <button key={tag} type="button" className="paper-library-tag" onClick={() => {
                      if (!conditions.some((condition) => condition.field === "tag" && condition.value === tag)) {
                        setConditions([...conditions, { field: "tag", value: tag, label: `标签包含 ${tag}` }]);
                      }
                    }}>{tag}</button>
                  ))}
                  <span className="paper-library-source">{sourceLabel[item.source]}</span>
                  {item.incomplete && <span className="paper-library-warn">待补全</span>}
                  {item.parse === "failed" && <span className="paper-library-warn">解析失败</span>}
                  {item.parse === "parsing" && <span className="paper-library-source">解析中</span>}
                  {(Object.keys(citationLabel) as CitationStyle[]).map((style) => (
                    <button key={style} type="button" className="paper-library-cite-button" onClick={() => copyText(formatCitation(item, style), citationLabel[style])}>
                      {citationLabel[style]}
                    </button>
                  ))}
                  {paperLink(item) && <a className="paper-library-link" href={paperLink(item)!} target="_blank" rel="noreferrer">原文</a>}
                  <button type="button" className="paper-library-cite-button" onClick={() => { setEditing(item); setSheet("edit"); }}>编辑</button>
                  <Select
                    className="paper-library-move"
                    size="small"
                    value={item.folderId || "none"}
                    aria-label={`${item.title} 的文件夹`}
                    options={folderOptions}
                    onChange={(value) => placePapers([item.id], value)}
                  />
                  <Popconfirm title="从论文集移除这篇题录？" okText="移除" cancelText="取消" onConfirm={() => dropPapers([item.id])}>
                    <button type="button" className="paper-library-cite-button danger">移除</button>
                  </Popconfirm>
                </div>
              </div>
            </article>
          ))}
          {selectedPapers.length > 0 && (
        <div className="paper-library-bar" role="region" aria-label="已选论文">
          <span className="hint">已选 {selectedPapers.length} 篇，可阅读 {readyCount} 篇</span>
          <Button onClick={() => copyText(selectedPapers.map((item) => formatCitation(item, "gbt")).join("\n"), "国标引用")}>复制国标</Button>
          <Select className="paper-library-move" size="small" value={bulkFolder} aria-label="移动到文件夹" options={folderOptions} onChange={setBulkFolder} />
          <Button onClick={() => placePapers(selected, bulkFolder)}>移动</Button>
          <Popconfirm title={`移除已选的 ${selectedPapers.length} 篇题录？`} okText="移除" cancelText="取消" onConfirm={() => dropPapers(selected)}>
            <Button>移除</Button>
          </Popconfirm>
          <Button disabled={readyCount < 2} onClick={() => setSheet("review")}>生成综述（{readyCount}）</Button>
          <Button disabled={readyCount < 1} onClick={() => message.info("自动拓展会带着这些种子论文进入研究流程")}>自动拓展（{readyCount}）</Button>
          </div>
          )}
        </div>
      </div>
      {sheet && (
        <div className="paper-library-mask" onClick={(event) => { if (event.target === event.currentTarget) setSheet(null); }}>
          <aside className="paper-library-sheet" role="dialog" aria-modal="true" aria-label={sheet === "import" ? "导入论文" : sheet === "filter" ? "筛选" : sheet === "edit" ? "编辑题录" : "生成综述"}>
            <header className="paper-library-sheet-head">
              <h2>{sheet === "import" ? "导入论文" : sheet === "filter" ? "筛选" : sheet === "edit" ? "编辑题录" : "生成综述"}</h2>
              <Button type="text" onClick={() => setSheet(null)}>关闭</Button>
            </header>
            {sheet === "import" && (
              <div className="paper-library-import">
                <section>
                  <h3>DOI / arXiv</h3>
                  <p>粘贴 DOI、arXiv 编号或链接。库里已经有的不会再插入。</p>
                  <Input value={identifier} placeholder="10.48550/arXiv.1706.03762 或 1706.03762" onChange={(event) => setIdentifier(event.target.value)} onPressEnter={importIdentifier} />
                  <Button type="primary" loading={searching} onClick={importIdentifier}>查重并导入</Button>
                </section>
                <section>
                  <h3>BibTeX / RIS / CSV</h3>
                  <div className="paper-library-seg" role="group" aria-label="文献格式">
                    {([["bib", "BibTeX"], ["ris", "RIS"], ["csv", "CSV"]] as const).map(([key, label]) => (
                      <button key={key} type="button" aria-pressed={format === key} onClick={() => setFormat(key)}>{label}</button>
                    ))}
                  </div>
                  <div className="paper-library-inline">
                    <Select value={bibliographyKind} options={[
                      { value: "google_scholar", label: "Google Scholar" },
                      { value: "zotero", label: "Zotero" },
                      { value: "local", label: "本地文件" },
                    ]} onChange={setBibliographyKind} />
                  </div>
                  <Input.TextArea rows={6} value={bibliography} placeholder={format === "csv" ? "题名,作者,出处,年份,DOI,arXiv,标签,文件夹" : "粘贴文献记录"} onChange={(event) => setBibliography(event.target.value)} />
                  <Button onClick={importBibliography}>识别</Button>
                </section>
                <section>
                  <h3>PDF</h3>
                  <p>读取文件信息和首页文字。抽到 DOI 会补作者和出处；抽不到则保留文件名，并标成待补全。</p>
                  <input ref={fileRef} hidden type="file" accept=".pdf,.bib,.ris,.csv,application/pdf,text/csv" onChange={(event) => {
                    const file = event.target.files?.[0];
                    event.target.value = "";
                    if (file) void importFile(file);
                  }} />
                  <Button type="primary" onClick={() => fileRef.current?.click()}>选择 PDF 或文献文件</Button>
                </section>
                <section>
                  <h3>外部文库</h3>
                  <Input value={query} placeholder="Semantic Scholar 检索词" onChange={(event) => setQuery(event.target.value)} />
                  <Button loading={searching} onClick={async () => {
                    setSearching(true);
                    try {
                      const found = await searchSemanticScholar(query.trim());
                      setHits(found);
                      if (!found.length) message.info("没有找到可导入的论文");
                    } catch (error) {
                      message.error(error instanceof Error ? error.message : "检索失败");
                    } finally {
                      setSearching(false);
                    }
                  }}>检索 Semantic Scholar</Button>
                  <Input value={zoteroUser} placeholder="Zotero 用户 ID" onChange={(event) => setZoteroUser(event.target.value)} />
                  <Input.Password value={zoteroKey} placeholder="Zotero API Key" onChange={(event) => setZoteroKey(event.target.value)} />
                  <Button loading={searching} onClick={async () => {
                    setSearching(true);
                    try {
                      const found = await searchZotero(zoteroUser.trim(), zoteroKey.trim());
                      setHits(found);
                    } catch (error) {
                      message.error(error instanceof Error ? error.message : "没有读到 Zotero");
                    } finally {
                      setSearching(false);
                    }
                  }}>读取 Zotero</Button>
                  <div className="paper-library-results">
                    {hits.map((item) => (
                      <button key={item.id} type="button" className="paper-library-result" onClick={() => stage([item])}>
                        <b>{item.title}</b>
                        <small>{item.authors} · {item.venue} {item.year}</small>
                      </button>
                    ))}
                  </div>
                </section>
                {pending && (
                  <section className="paper-library-plan">
                    <h3>将加入 {pending.fresh.length} 篇，跳过 {pending.skipped.length} 篇</h3>
                    {pending.skipped.map((item, index) => <p key={`${item.id}-skip-${index}`}>库中已有：{item.title}</p>)}
                    {pending.fresh.map((item) => <p key={item.id}>加入：{item.title}</p>)}
                    <Button type="primary" disabled={!pending.fresh.length} onClick={commit}>加入论文集</Button>
                  </section>
                )}
              </div>
            )}
            {sheet === "filter" && (
              <FilterForm match={match} onMatch={setMatch} onAdd={(condition) => { setConditions([...conditions, condition]); setSheet(null); }} />
            )}
            {sheet === "edit" && editing && <EditForm key={editing.id} paper={editing} onSave={saveEdits} />}
            {sheet === "review" && (
              <div>
                <p>将把 {readyCount} 篇可阅读论文交给学术研究与论文写作。标为重要的论文会加重参考。</p>
                {selectedPapers.filter(usablePaper).map((item) => <p key={item.id}>{item.title}{item.important ? " · 重要" : ""}</p>)}
              </div>
            )}
          </aside>
        </div>
      )}
    </section>
  );
}

function FolderButton({ name, count, current, onClick, onRemove }: { name: string; count: number; current: boolean; onClick: () => void; onRemove?: () => void }) {
  return (
    <div className={`paper-library-folder ${current ? "current" : ""}`}>
      <button type="button" aria-current={current ? "true" : undefined} onClick={onClick}>
        <span>{name}</span><span>{count}</span>
      </button>
      {onRemove && <button type="button" aria-label={`删除文件夹 ${name}`} onClick={onRemove}>×</button>}
    </div>
  );
}

function EditForm({ paper, onSave }: { paper: PaperItem; onSave: (edits: PaperEdits) => void }) {
  const [title, setTitle] = useState(paper.title);
  const [authors, setAuthors] = useState(paper.authors);
  const [venue, setVenue] = useState(paper.venue);
  const [year, setYear] = useState(String(paper.year));
  const [doi, setDoi] = useState(paper.doi || "");
  const [arxiv, setArxiv] = useState(paper.arxiv || "");
  const [tags, setTags] = useState(paper.tags.join(";"));
  return (
    <div className="paper-library-import">
      <label>题名<Input value={title} onChange={(event) => setTitle(event.target.value)} /></label>
      <label>作者<Input value={authors} onChange={(event) => setAuthors(event.target.value)} /></label>
      <label>出处<Input value={venue} onChange={(event) => setVenue(event.target.value)} /></label>
      <label>年份<Input value={year} onChange={(event) => setYear(event.target.value)} /></label>
      <label>DOI<Input value={doi} onChange={(event) => setDoi(event.target.value)} /></label>
      <label>arXiv<Input value={arxiv} onChange={(event) => setArxiv(event.target.value)} /></label>
      <label>标签<Input value={tags} placeholder="用分号分开" onChange={(event) => setTags(event.target.value)} /></label>
      <Button type="primary" onClick={() => onSave({
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

function FilterForm({ match, onMatch, onAdd }: { match: MatchMode; onMatch: (value: MatchMode) => void; onAdd: (condition: FilterCondition) => void }) {
  const [value, setValue] = useState("tag:RAG");
  const options = [
    { value: "tag:RAG", label: "标签包含 RAG" },
    { value: "importance:true", label: "重要程度为重要" },
    { value: "status:unread", label: "阅读状态为未读" },
    { value: "parse:failed", label: "解析状态为失败" },
    { value: "source:zotero", label: "来源为 Zotero" },
    { value: "source:doi", label: "来源为 DOI" },
    { value: "source:arxiv", label: "来源为 arXiv" },
  ];
  return (
    <div className="paper-library-import">
      <Radio.Group value={match} onChange={(event) => onMatch(event.target.value)} options={[{ label: "全部满足", value: "all" }, { label: "任一满足", value: "any" }]} />
      <Select value={value} options={options} onChange={setValue} />
      <Button type="primary" onClick={() => {
        const option = options.find((item) => item.value === value)!;
        const [field, fieldValue] = option.value.split(":") as [FilterCondition["field"], string];
        onAdd({ field, value: fieldValue, label: option.label });
      }}>加上这条</Button>
    </div>
  );
}
