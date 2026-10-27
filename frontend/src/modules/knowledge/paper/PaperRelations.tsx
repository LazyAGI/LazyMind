import { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Spin, Typography } from 'antd';
import { ArrowRightOutlined, BranchesOutlined, ExportOutlined, LinkOutlined, ReadOutlined, ReloadOutlined } from '@ant-design/icons';
import { extractAcademicReferences, listAcademicReferences, type AcademicReference } from '../api/academicReferences';
import { listExpansions, type Expansion } from '../api/paperWorkflows';
import type { Attachment, PaperLibraryData, PaperRecord } from './libraryModel';
import { safePaperUrl } from './model';
import { isStoredPdfAttachment } from './paperFilters';
import { findLibraryPaper, paperExpansions, referenceRelations, relatedPaperItem, type RelatedPaper } from './relationModel';
import './PaperRelations.scss';
import type { PaperNotification } from './usePaperNotifications';
interface Props {
    datasetId: string;
    paper: PaperRecord;
    data: PaperLibraryData;
    attachments: Attachment[];
    onOpenPaper?: (id: string) => void;
    onAttachments: () => void;
    onNotification?: (notice: PaperNotification) => void;
    onNotificationResolved?: (id: string) => void;
}
function PaperCard({ item, onOpenPaper }: {
    item: RelatedPaper;
    onOpenPaper?: Props['onOpenPaper'];
}) {
    const title = item.paper && onOpenPaper
        ? <button type="button" className="paper-relation-title" onClick={() => onOpenPaper(item.paper!.id)}>{item.title}<ArrowRightOutlined /></button>
        : item.url ? <a className="paper-relation-title" href={item.url} target="_blank" rel="noopener noreferrer">{item.title}<ExportOutlined /></a> : item.title;
    return <article className="paper-relation-card">
    <div className="paper-relation-card-top"><span>{item.source}</span>{item.paper && <span className="paper-relation-badge">库内文献</span>}</div>
    <Typography.Paragraph className="paper-relation-card-title" ellipsis={{ rows: 3, expandable: true, symbol: '展开' }}>{title}</Typography.Paragraph>
    {item.metadata && <p className="paper-relation-metadata">{item.metadata}</p>}
    {item.detail && <Typography.Paragraph className="paper-relation-detail" ellipsis={{ rows: 2, expandable: true, symbol: '展开' }}>{item.detail}</Typography.Paragraph>}
  </article>;
}
export default function PaperRelations({ datasetId, paper, data, attachments, onOpenPaper, onAttachments, onNotification, onNotificationResolved }: Props) {
    const notifications = useRef({ notify: onNotification, resolve: onNotificationResolved, open: onOpenPaper });
    notifications.current = { notify: onNotification, resolve: onNotificationResolved, open: onOpenPaper };
    const [references, setReferences] = useState<AcademicReference[]>([]);
    const [records, setRecords] = useState<Expansion[]>([]);
    const [referencesLoading, setReferencesLoading] = useState(false);
    const [expansionsLoading, setExpansionsLoading] = useState(false);
    const [referencesError, setReferencesError] = useState('');
    const [expansionsError, setExpansionsError] = useState('');
    const [referenceRevision, setReferenceRevision] = useState(0);
    const [expansionRevision, setExpansionRevision] = useState(0);
    const [allCitations, setAllCitations] = useState(false);
    const documentKey = [...new Set(attachments.filter(isStoredPdfAttachment).map(item => item.contentRef!))].join('\0');
    useEffect(() => {
        let cancelled = false;
        setReferences([]);
        setReferencesError('');
        if (!documentKey) {
            setReferencesLoading(false);
            return;
        }
        setReferencesLoading(true);
        void Promise.allSettled(documentKey.split('\0').map(async (documentId) => {
            const saved = await listAcademicReferences(documentId);
            if (saved.length || paper.parse !== 'ready')
                return saved;
            try {
                return await extractAcademicReferences(documentId, true);
            }
            catch (error) {
                if (error instanceof Error && /no reference entries were detected/i.test(error.message))
                    return [];
                throw error;
            }
        })).then(results => {
            if (cancelled)
                return;
            setReferences(results.flatMap(result => result.status === 'fulfilled' ? result.value : []));
            if (results.some(result => result.status === 'rejected')) {
                setReferencesError('部分 PDF 的参考文献暂时无法加载，请重试。');
                notifications.current.notify?.({ id: `references:${paper.id}`, title: `《${paper.metadata.title}》参考文献加载失败`, message: results.flatMap(result => result.status === 'rejected' ? [result.reason instanceof Error ? result.reason.message : '参考文献加载失败'] : []).join('；'), action: { label: '查看关系', onClick: () => notifications.current.open?.(paper.id) } });
            } else notifications.current.resolve?.(`references:${paper.id}`);
            setReferencesLoading(false);
        });
        return () => { cancelled = true; };
    }, [documentKey, paper.parse, referenceRevision]);
    useEffect(() => {
        let cancelled = false;
        setRecords([]);
        setExpansionsError('');
        setExpansionsLoading(true);
        void listExpansions(datasetId, !!notifications.current.notify).then(items => {
            if (!cancelled) {
                setRecords(items);
                notifications.current.resolve?.(`relations:${paper.id}`);
            }
        }).catch(error => {
            if (!cancelled) {
                setExpansionsError('论文扩展关系暂时无法加载，请重试。');
                notifications.current.notify?.({ id: `relations:${paper.id}`, title: `《${paper.metadata.title}》扩展关系加载失败`, message: error instanceof Error ? error.message : '扩展关系加载失败', action: { label: '查看关系', onClick: () => notifications.current.open?.(paper.id) } });
            }
        }).finally(() => { if (!cancelled)
            setExpansionsLoading(false); });
        return () => { cancelled = true; };
    }, [datasetId, expansionRevision]);
    const citations = useMemo(() => referenceRelations(references, data, paper), [references, data, paper]);
    const expansions = useMemo(() => paperExpansions(records, data, paper), [records, data, paper]);
    const savedRelations = useMemo(() => {
        const related = new Map<string, RelatedPaper>();
        for (const relation of data.relations || []) {
            const outgoing = relation.fromPaperId === paper.id;
            const incoming = relation.toPaperId === paper.id;
            if (!outgoing && !incoming) continue;
            const target = data.papers.find(item => item.id === (outgoing ? relation.toPaperId : relation.fromPaperId));
            if (!target || target.id === paper.id) continue;
            const source = relation.kind === 'related' ? '相关文献' : relation.kind === 'version_of' ? '版本关系'
                : (relation.kind === 'cites') === outgoing ? '引用文献' : '引用本篇';
            related.set(target.id, relatedPaperItem(target, source));
        }
        for (const id of paper.relatedPaperIds || []) {
            const target = data.papers.find(item => item.id === id);
            if (target && !related.has(id)) related.set(id, relatedPaperItem(target, '相关文献'));
        }
        return [...related.values()];
    }, [data.papers, data.relations, paper]);
    const cardList=(items:RelatedPaper[]) => <div className="paper-relation-list">{items.map(item=><PaperCard key={item.id} item={item} onOpenPaper={onOpenPaper}/>)}</div>;
    return <div className="paper-relations">
    <div className="paper-relations-intro"><span className="paper-relations-emblem"><LinkOutlined /></span><div><h3>文献之间的联系</h3><p>沿着引用与研究线索，继续阅读。</p></div></div>
    <div className="paper-relations-stats" aria-label="关系概览">
      {[
            { label: '引用文献', count: referencesLoading ? '…' : referencesError && !citations.length ? '—' : citations.length },
            { label: '扩展记录', count: expansionsLoading ? '…' : expansionsError ? '—' : expansions.length },
        ].map(item => <div key={item.label}><strong>{item.count}</strong><span>{item.label}</span></div>)}
    </div>

    <section className="paper-relation-section" aria-label="引用文献">
      <div className="paper-relation-heading"><h3><ReadOutlined />引用文献<span>{citations.length}</span></h3>{referencesLoading && <Spin size="small"/>}</div>
      <p className="paper-relation-caption">本篇参考文献及已记录的引用关系</p>
      {referencesError && (onNotification ? <p role="status">参考文献未完全加载。<Button type="link" size="small" onClick={() => setReferenceRevision(value => value + 1)}>重试</Button></p> : <Alert type="warning" showIcon message={referencesError} action={<Button size="small" icon={<ReloadOutlined />} onClick={() => setReferenceRevision(value => value + 1)}>重试</Button>}/>)}
      {citations.length ? <>{cardList(allCitations ? citations : citations.slice(0, 5))}{citations.length > 5 && <button type="button" className="paper-relation-show-more" aria-expanded={allCitations} onClick={() => setAllCitations(value => !value)}>{allCitations ? '收起参考文献' : `查看全部 ${citations.length} 条参考文献`}</button>}</> : !referencesLoading && !referencesError && <div className="paper-relation-empty"><ReadOutlined /><div><strong>{documentKey ? '暂无已识别的参考文献' : '还没有可识别的 PDF'}</strong><p>{documentKey ? paper.parse === 'ready' ? '可打开全文，查看原文中的参考文献。' : '全文解析完成后，可在这里查看参考文献。' : '添加并解析 PDF 后，可在这里查看参考文献。'}</p>{!documentKey && <button type="button" className="paper-detail-tab-link" onClick={onAttachments}>前往附件<ArrowRightOutlined /></button>}</div></div>}
    </section>


    {savedRelations.length > 0 && <section className="paper-relation-section" aria-label="已保存关系">
      <div className="paper-relation-heading"><h3><LinkOutlined />已保存关系<span>{savedRelations.length}</span></h3></div>
      {cardList(savedRelations)}
    </section>}

    {(expansionsLoading || expansionsError || expansions.length > 0) && <section className="paper-relation-section" aria-label="论文扩展">
      <div className="paper-relation-heading"><h3><BranchesOutlined />论文扩展<span>{expansions.length}</span></h3>{expansionsLoading && <Spin size="small"/>}</div>
      {expansionsError && (onNotification ? <p role="status">扩展关系暂时无法加载。<Button type="link" size="small" onClick={() => setExpansionRevision(value => value + 1)}>重试</Button></p> : <Alert type="warning" showIcon message={expansionsError} action={<Button size="small" icon={<ReloadOutlined />} onClick={() => setExpansionRevision(value => value + 1)}>重试</Button>}/>)}
      {expansions.map(({ record, current, others }) => <article className="paper-expansion-relation" key={record.id}>
        <div className="paper-relation-card-top"><span>论文扩展 · 第 {record.version} 版</span><span className="paper-relation-badge">{current.is_seed ? '种子文献' : '扩展发现'}</span></div>
        <Typography.Paragraph className="paper-relation-card-title" ellipsis={{ rows: 2, expandable: true, symbol: '展开' }}>{record.title}</Typography.Paragraph>
        <dl>{[
                    ['与种子文献的关系', current.seed_relation],
                    ['与研究方向的关系', current.direction_relation],
                    ['关键贡献', current.key_contribution],
                ].filter(([, content]) => content?.trim()).map(([label, content]) => <div key={label}><dt>{label}</dt><dd><Typography.Paragraph ellipsis={{ rows: 3, expandable: true, symbol: '展开' }}>{content}</Typography.Paragraph></dd></div>)}</dl>
        {others.length > 0 && <details className="paper-expansion-peers"><summary>同次扩展的文献<span>{others.length}</span></summary><div className="paper-relation-list">{others.map(item => {
                        const target = findLibraryPaper(data, item.paper);
                        const url = safePaperUrl(item.paper.paper_url) || safePaperUrl(item.paper.pdf_url);
                        return <PaperCard key={item.paper.paper_id} onOpenPaper={onOpenPaper} item={{ id: item.paper.paper_id, paper: target, title: target?.metadata.title || item.paper.title || '未命名文献', url, source: item.is_seed ? '种子文献' : '扩展发现', detail: item.seed_relation, metadata: [item.paper.authors?.join(', '), item.paper.year].filter(Boolean).join(' · ') }}/>;
                    })}</div></details>}
      </article>)}
    </section>}

  </div>;
}
