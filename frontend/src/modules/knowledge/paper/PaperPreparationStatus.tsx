import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { Button, Tag, Typography, theme } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { usePdfIR, type PdfIRTarget } from '../api/pdfIR';
import { getDocumentParseStatus, type DocumentParseResult } from '../api/pdfArtifacts';
import type { ReadingCapabilityStatus, ReadingPreparation } from '../api/readingPreparation';
import './PaperPreparationStatus.css';
const structureLabels = { not_requested: '尚未解析', queued: '排队中', running: '解析中', ready: '已就绪', failed: '失败', unavailable: '不可用' };
const chatLabels = { not_requested: '尚未准备', parsing: '准备中', parsed: '已就绪', failed: '失败', unknown: '状态未知' };
const studyLabels = { not_requested: '尚未开始', pending: '排队中', running: '进行中', partial: '部分完成', succeeded: '已完成', failed: '失败', unavailable: '暂不可用', empty: '本文无此内容' };
const studyScopes = [{ id: 'formula', name: '公式研读' }, { id: 'figure', name: '图片研读' }, { id: 'table', name: '表格研读' }] as const;
export default function PaperPreparationStatus({ target, onRefresh, capabilities, batchStatus }: { target: PdfIRTarget; onRefresh?: () => void; capabilities?: ReadingCapabilityStatus[]; batchStatus?: ReadingPreparation['status'] }) {
  const ir = usePdfIR(target);
  const { token } = theme.useToken();
  const refreshIR = useRef(ir.refresh);
  refreshIR.current = ir.refresh;
  const [chat, setChat] = useState<DocumentParseResult>();
  const [error, setError] = useState(false);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let stale = false;
    let timer: ReturnType<typeof setTimeout>;
    setChat(undefined); setError(false);
    const poll = async () => {
      try {
        const result = await getDocumentParseStatus(target.dataset, target.document);
        if (stale) return;
        setChat(result); setError(false);
        timer = setTimeout(() => { refreshIR.current(); void poll(); }, result.status === 'parsing' ? 2000 : 4000);
      } catch { if (!stale) { setError(true); timer = setTimeout(poll, 4000); } }
    };
    void poll();
    return () => { stale = true; clearTimeout(timer); };
  }, [target.dataset, target.document, revision, batchStatus]);
  const ready = !ir.loading && ir.state.status === 'ready';
  const busy = ['queued', 'running'].includes(ir.state.status);
  const blocks = ir.state.document?.blocks || [];
  return <section className="paper-preparation-status" style={{ "--paper-secondary": token.colorTextSecondary } as CSSProperties} aria-label="论文解析状态">
    <div className="paper-preparation-heading"><strong>本篇准备状态</strong><Button type="text" size="small" icon={<ReloadOutlined />} onClick={() => { ir.refresh(); setRevision(v => v + 1); onRefresh?.(); }}>刷新状态</Button></div>
    <div className="paper-preparation-stage"><div><strong>文档结构解析</strong><p>{ready ? `识别到 ${blocks.filter(b => b.type === 'formula').length} 个公式、${blocks.filter(b => b.type === 'image').length} 张图片、${blocks.filter(b => b.type === 'table').length} 个表格` : '与知识库共用 MinerU 解析结果，识别正文、公式与图表'}</p></div><Tag color={ready ? 'success' : busy ? 'processing' : ir.state.status === 'failed' ? 'error' : 'default'}>{ir.loading ? '读取中' : structureLabels[ir.state.status]}</Tag></div>
    {ir.state.error_message && <Typography.Paragraph type="danger">{ir.state.error_message}</Typography.Paragraph>}
    {studyScopes.map(scope => {
      const capability = capabilities?.find(item => item.id === scope.id);
      const status = capability?.status;
      const color = status === 'succeeded' ? 'success' : status === 'running' || status === 'pending' ? 'processing' : status === 'failed' ? 'error' : status === 'partial' ? 'warning' : 'default';
      return <div className="paper-preparation-study" key={scope.id}>
        <div className="paper-preparation-stage">
          <div><strong>{scope.name}</strong>
            <p>{ready && capability && status !== 'unavailable' ? `已完成 ${capability.completed} / ${capability.total} 项` : '结构就绪后，按本文内容统计研读结果'}</p>
            {!!capability?.running && <p>正在处理 {capability.running} 项</p>}
            {!!capability?.failed && <p>失败 {capability.failed} 项，可重新准备</p>}
          </div>
          <Tag color={color}>{status ? studyLabels[status] : '等待状态'}</Tag>
        </div>
        {status === 'partial' && <Typography.Paragraph type="secondary">已有结果可直接使用；未完成内容可继续准备，生成数量受个人预设限额约束。</Typography.Paragraph>}
        {capability?.error_message && <Typography.Paragraph type="danger">{capability.error_message}</Typography.Paragraph>}
      </div>;
    })}
    <div className="paper-preparation-stage"><div><strong>文档对话准备</strong><p>准备检索内容，供论文助手引用原文</p></div><Tag color={error || chat?.status === 'failed' ? 'error' : chat?.status === 'parsed' ? 'success' : chat?.status === 'parsing' ? 'processing' : 'default'}>{error ? '读取失败' : chat ? chatLabels[chat.status] : '读取中'}</Tag></div>
    {chat?.error?.message && <Typography.Paragraph type="danger">{chat.error.message}</Typography.Paragraph>}
    {error && <Typography.Text type="secondary">暂时无法读取对话准备状态，可点击刷新重试。</Typography.Text>}
  </section>;
}
