import { useRef, useState } from 'react';
import { Button, Popover, theme } from 'antd';
import { CheckCircleOutlined, ClockCircleOutlined, FileTextOutlined, InfoCircleOutlined, LoadingOutlined, ExclamationCircleOutlined } from '@ant-design/icons';
import { pdfIRSubmissionMessage } from '../api/pdfIR';
import type { ParseState } from './model';
import './PdfParseStatus.css';

const labels = { not_requested: '尚未解析', queued: '等待解析', parsing: '解析中', ready: '已解析', failed: '解析失败', unknown: '状态未知' };
const icons = { not_requested: FileTextOutlined, queued: ClockCircleOutlined, parsing: LoadingOutlined, ready: CheckCircleOutlined, failed: ExclamationCircleOutlined, unknown: InfoCircleOutlined };
export default function PdfParseStatus({ status = 'unknown', error, available, quotaWaiting, onOpenChange, onRefresh, onParse, disabledReason }: {
  status?: ParseState;
  error?: string;
  available?: boolean;
  quotaWaiting?: boolean;
  onOpenChange?: (open: boolean) => void;
  onRefresh?: () => Promise<void>;
  onParse?: () => Promise<void> | void;
  disabledReason?: string;
}) {
  const { token } = theme.useToken();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const pending = useRef(false);
  const [submitError, setSubmitError] = useState('');
  const state = status in labels ? status : 'unknown';
  const Icon = submitting ? LoadingOutlined : icons[state];
  const active = state === 'queued' || state === 'parsing';
  const label = submitting ? '正在提交' : quotaWaiting ? '等待自动额度' : labels[state];
  const color = state === 'failed' ? token.colorErrorText : state === 'ready' ? token.colorSuccessText : active ? token.colorInfoText : token.colorTextSecondary;
  const background = state === 'failed' ? token.colorErrorBg : state === 'ready' ? token.colorSuccessBg : active ? token.colorInfoBg : token.colorFillTertiary;
  const descriptions = { not_requested: '已保存 PDF 原文，尚未解析结构。', queued: '解析任务已提交，正在等待处理。', parsing: '正在识别正文、公式、图片和表格。', ready: 'PDF 结构已解析完成，论文与知识库共用此结果。', failed: 'PDF 解析未完成，仍可阅读原文。', unknown: '暂未获取到 PDF 结构解析状态，仍可阅读原文。' };
  const action = state === 'not_requested' || quotaWaiting && !available ? '开始解析' : state === 'failed' ? '重试解析' : '重新解析';
  const canShowAction = !!onParse && (!active || quotaWaiting);
  return <Popover trigger="click" title="PDF 解析" onOpenChange={onOpenChange} content={<div className="pdf-parse-details" aria-busy={submitting}>
    <p>{quotaWaiting ? error || '今日自动解析额度已用尽，任务将等待次日额度。' : descriptions[state]}</p>
    {available && state !== 'ready' && <p>已有解析结果仍可使用。</p>}
    {!quotaWaiting && error && <details><summary>查看详情</summary><pre>{error}</pre></details>}
    {refreshError && <p role="alert">状态刷新失败，请稍后重试。</p>}
    {submitError && <p role="alert">{submitError}</p>}
    <div className="pdf-parse-actions">
      {onRefresh && <Button size="small" loading={refreshing} disabled={submitting} onClick={async () => { setRefreshing(true); setRefreshError(false); try { await onRefresh(); } catch { setRefreshError(true); } finally { setRefreshing(false); } }}>刷新状态</Button>}
      {canShowAction && <Button size="small" loading={submitting} disabled={submitting || !!disabledReason} onClick={async () => {
        if (pending.current || disabledReason) return;
        pending.current = true;
        setSubmitting(true);
        setSubmitError('');
        try { await onParse(); }
        catch (error) { setSubmitError(pdfIRSubmissionMessage(error)); }
        finally { pending.current = false; setSubmitting(false); }
      }}>{action}</Button>}
    </div>
    {disabledReason && canShowAction && <p>{disabledReason}</p>}
  </div>}>
    <button type="button" className="pdf-parse-status" style={{ color, background }} aria-label={`PDF 解析：${label}`} onClick={event => event.stopPropagation()}><Icon aria-hidden /><span>{label}</span></button>
  </Popover>;
}
