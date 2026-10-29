import { CheckCircleFilled, CloseCircleFilled, LoadingOutlined, WarningFilled } from '@ant-design/icons';
import './PaperLinkProgress.scss';

export type PaperLinkPhase = 'metadata' | 'pdf' | 'ready' | 'submitting' | 'failed';
interface Props { phase: PaperLinkPhase; failurePhase: PaperLinkPhase; hasPDF: boolean; warnings: string[]; manualURLs?: string[] }
export default function PaperLinkProgress({ phase, failurePhase, hasPDF, warnings, manualURLs = [] }: Props) {
  const active = phase === 'failed' ? failurePhase : phase;
  const current = active === 'metadata' ? 0 : active === 'pdf' ? 1 : 2;
  const labels = ['发现文献信息', '查找可用 PDF', '确认入库'];
  return <section className="paper-link-progress" aria-label="文献导入进度">
    <ol>{labels.map((label, index) => {
      const failed = phase === 'failed' && index === current;
      const running = index === current && ['metadata', 'pdf', 'submitting'].includes(phase);
      const warning = index === 1 && current > 1 && !hasPDF;
      return <li key={label} className={failed ? 'is-failed' : warning ? 'is-warning' : index < current ? 'is-done' : running ? 'is-active' : ''} aria-current={index === current ? 'step' : undefined}>
        <span className="paper-link-progress-icon">{failed ? <CloseCircleFilled /> : running ? <LoadingOutlined spin /> : warning ? <WarningFilled /> : index < current ? <CheckCircleFilled /> : <span>{index + 1}</span>}</span>
        <span>{label}</span>
      </li>;
    })}</ol>
    <p role="status" aria-live="polite">{phase === 'metadata' ? '正在查询题名、作者与出版信息…' : phase === 'pdf' ? '正在检查开放来源与 Sci-Hub，最多 6 秒…' : phase === 'submitting' ? '正在创建导入任务…' : phase === 'failed' ? '当前步骤失败，请查看错误详情后重试。' : hasPDF ? '已找到可用 PDF，确认后下载入库。' : '未找到可用 PDF；可以明确选择仅保存题录。'}</p>
    {!hasPDF && phase === 'ready' && !!manualURLs.length && <div className="paper-manual-download"><strong>自行下载 PDF</strong><p>打开来源页面，完成验证后下载，再将 PDF 添加到这篇文献。</p><div>{manualURLs.filter(address => { try { const u = new URL(address); return u.protocol === 'https:' && !u.username && !u.password; } catch { return false; } }).map(address => <a key={address} href={address} target="_blank" rel="noopener noreferrer">{new URL(address).hostname === 'doi.org' ? '出版商页面' : new URL(address).hostname}</a>)}</div></div>}
    {!!warnings.length && <details className="paper-link-progress-warnings" open={!hasPDF}><summary>PDF 来源查询提示（{warnings.length}）</summary><ul>{warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul></details>}
  </section>;
}
