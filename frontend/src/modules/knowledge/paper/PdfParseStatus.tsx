import { useState } from 'react';
import { Button, Popover, theme } from 'antd';
import { CheckCircleOutlined, ClockCircleOutlined, FileTextOutlined, InfoCircleOutlined, LoadingOutlined, ExclamationCircleOutlined } from '@ant-design/icons';
import type { ParseState } from './model';
import './PdfParseStatus.css';
const labels = { not_requested: '尚未解析', queued: '等待解析', parsing: '解析中', ready: '已解析', failed: '解析失败', unknown: '状态未知' };
const icons = { not_requested: FileTextOutlined, queued: ClockCircleOutlined, parsing: LoadingOutlined, ready: CheckCircleOutlined, failed: ExclamationCircleOutlined, unknown: InfoCircleOutlined };
export default function PdfParseStatus({ status = 'unknown', error, onRefresh, onParse, disabledReason }: { status?: ParseState; error?: string; onRefresh?: () => Promise<void>; onParse?: () => void; disabledReason?: string }) {
  const { token } = theme.useToken();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState(false);
  const state = status in labels ? status : 'unknown';
  const Icon = icons[state];
  const color = state === 'failed' ? token.colorErrorText : state === 'ready' ? token.colorSuccessText : ['queued', 'parsing'].includes(state) ? token.colorInfoText : token.colorTextSecondary;
  const background = state === 'failed' ? token.colorErrorBg : state === 'ready' ? token.colorSuccessBg : ['queued', 'parsing'].includes(state) ? token.colorInfoBg : token.colorFillTertiary;
  const descriptions = { not_requested: '已保存 PDF 原文，尚未请求解析。', queued: '解析任务已提交，正在等待处理。', parsing: '正在提取 PDF 正文。', ready: 'PDF 正文已解析完成。', failed: 'PDF 解析未完成，仍可阅读原文。', unknown: '暂未获取到解析状态，仍可阅读原文。' };
  return <Popover trigger="click" title="PDF 解析" content={<div className="pdf-parse-details">
    <p>{descriptions[state]}</p>
    {state === 'failed' && error && <details><summary>查看详情</summary><pre>{error}</pre></details>}
    {refreshError && <p role="alert">状态刷新失败，请稍后重试。</p>}
    <div className="pdf-parse-actions">
      {onRefresh && <Button size="small" loading={refreshing} onClick={async () => { setRefreshing(true); setRefreshError(false); try { await onRefresh(); } catch { setRefreshError(true); } finally { setRefreshing(false); } }}>刷新状态</Button>}
      {onParse && ['failed', 'not_requested'].includes(state) && <Button size="small" disabled={!!disabledReason} onClick={onParse}>{state === 'failed' ? '重试解析' : '开始解析'}</Button>}
    </div>
    {disabledReason && onParse && ['failed', 'not_requested'].includes(state) && <p>{disabledReason}</p>}
  </div>}>
    <button type="button" className="pdf-parse-status" style={{ color, background }} aria-label={`PDF 解析：${labels[state]}`}><Icon aria-hidden /><span>{labels[state]}</span></button>
  </Popover>;
}
