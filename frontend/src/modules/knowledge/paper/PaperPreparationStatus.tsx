import { useEffect, useState } from 'react';
import { Button, Tag, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { usePdfIR, type PdfIRTarget } from '../api/pdfIR';
import { getDocumentParseStatus, type DocumentParseResult } from '../api/pdfArtifacts';
import './PaperPreparationStatus.css';
const structureLabels = { not_requested: '尚未解析', queued: '排队中', running: '解析中', ready: '已就绪', failed: '失败', unavailable: '不可用' };
const chatLabels = { not_requested: '尚未准备', parsing: '准备中', parsed: '已就绪', failed: '失败', unknown: '状态未知' };
export default function PaperPreparationStatus({ target, onRefresh }: { target: PdfIRTarget; onRefresh?: () => void }) {
  const ir = usePdfIR(target);
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
        if (result.status === 'parsing') timer = setTimeout(poll, 2000);
      } catch { if (!stale) setError(true); }
    };
    void poll();
    return () => { stale = true; clearTimeout(timer); };
  }, [target.dataset, target.document, revision]);
  const ready = !ir.loading && ir.state.status === 'ready';
  const busy = ['queued', 'running'].includes(ir.state.status);
  const blocks = ir.state.document?.blocks || [];
  return <section className="paper-preparation-status" aria-label="论文解析状态">
    <div className="paper-preparation-heading"><strong>原文与对话</strong><Button type="text" size="small" icon={<ReloadOutlined />} onClick={() => { ir.refresh(); setRevision(v => v + 1); onRefresh?.(); }}>刷新状态</Button></div>
    <div className="paper-preparation-stage"><div><strong>文档结构解析</strong><p>{ready ? `识别到 ${blocks.filter(b => b.type === 'formula').length} 个公式、${blocks.filter(b => b.type === 'image').length} 张图片、${blocks.filter(b => b.type === 'table').length} 个表格` : '识别正文、公式与图表，供原文研读使用'}</p></div><Tag color={ready ? 'success' : busy ? 'processing' : ir.state.status === 'failed' ? 'error' : 'default'}>{ir.loading ? '读取中' : structureLabels[ir.state.status]}</Tag></div>
    {ir.state.error_message && <Typography.Paragraph type="danger">{ir.state.error_message}</Typography.Paragraph>}
    <div className="paper-preparation-stage"><div><strong>文档对话准备</strong><p>准备检索内容，供论文助手引用原文</p></div><Tag color={error || chat?.status === 'failed' ? 'error' : chat?.status === 'parsed' ? 'success' : chat?.status === 'parsing' ? 'processing' : 'default'}>{error ? '读取失败' : chat ? chatLabels[chat.status] : '读取中'}</Tag></div>
    {chat?.error?.message && <Typography.Paragraph type="danger">{chat.error.message}</Typography.Paragraph>}
    {error && <Typography.Text type="secondary">暂时无法读取对话准备状态，可点击刷新重试。</Typography.Text>}
  </section>;
}
