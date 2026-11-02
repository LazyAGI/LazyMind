import { CheckCircleFilled, CloseCircleFilled, LoadingOutlined, WarningFilled } from '@ant-design/icons';
import './PaperLinkProgress.scss';

export type PaperLinkPhase = 'metadata' | 'pdf' | 'ready' | 'submitting' | 'failed';
interface Props { phase: PaperLinkPhase; failurePhase: PaperLinkPhase; hasPDF: boolean; warnings: string[]; manualURLs?: string[]; doi?: string }
export default function PaperLinkProgress({ phase, failurePhase, hasPDF, warnings, manualURLs = [], doi }: Props) {
  const links = manualURLs.flatMap(address => {
    try {
      const u = new URL(address);
      if (u.protocol !== 'https:' || u.username || u.password) return [];
      return [{ address, label: u.hostname === 'doi.org' ? '出版商页面' : u.hostname }];
    } catch { return []; }
  });
  if (doi) links.unshift(
    { address: `https://openalex.org/works?filter=${encodeURIComponent(`doi:https://doi.org/${doi}`)}`, label: 'OpenAlex' },
    { address: `https://europepmc.org/search?query=${encodeURIComponent(`DOI:${doi}`)}`, label: 'Europe PMC' },
    { address: `https://arxiv.org/search/?query=${encodeURIComponent(doi)}&searchtype=doi`, label: 'arXiv' },
  );
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
    <p role="status" aria-live="polite">{phase === 'metadata' ? '正在查询题名、作者与出版信息…' : phase === 'pdf' ? '正在检查开放来源与 Sci-Hub，最多 6 秒…' : phase === 'submitting' ? '正在创建导入任务…' : phase === 'failed' ? '当前步骤失败，请查看错误详情后重试。' : hasPDF ? '已找到可用 PDF，确认后下载入库。' : '未找到可用 PDF；可以明确选择仅加入文献库。'}</p>
    {!hasPDF && phase === 'ready' && !!links.length && <div className="paper-manual-download"><strong>查找其他 PDF 来源</strong><p>以下为检索入口，并不代表已有可用 PDF。下载后可添加到当前文献。</p><div>{links.map(({address, label}) => <a key={address} href={address} target="_blank" rel="noopener noreferrer">{label}</a>)}</div></div>}
    {!!warnings.length && <details className="paper-link-progress-warnings"><summary>PDF 来源查询提示（{warnings.length}）</summary><ul>{warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul></details>}
  </section>;
}

export function PaperBibliographyProgress({ phase, count }: { phase: 'metadata' | 'ready' | 'submitting' | 'failed'; count: number }) {
  const ready = phase === 'ready' || phase === 'submitting';
  return <section className="paper-link-progress" aria-label="文献导入进度">
    <ol>
      <li className={phase === 'failed' ? 'is-failed' : ready ? 'is-done' : 'is-active'}>
        <span className="paper-link-progress-icon">{phase === 'failed' ? <CloseCircleFilled /> : ready ? <CheckCircleFilled /> : <LoadingOutlined spin />}</span><span>识别文献信息</span>
      </li>
      <li className={ready ? 'is-active' : ''}><span className="paper-link-progress-icon">{phase === 'submitting' ? <LoadingOutlined spin /> : '2'}</span><span>确认入库</span></li>
      <li><span className="paper-link-progress-icon">3</span><span>补全信息与 PDF</span></li>
    </ol>
    <p role="status" aria-live="polite">{phase === 'metadata' ? '正在识别文献并检查重复…' : phase === 'failed' ? '识别未完成，请检查内容或重试。' : phase === 'submitting' ? '正在保存文献信息，无需等待 PDF 下载…' : `已识别 ${count} 篇文献，可立即入库。`}</p>
    <span className="paper-bibliography-followup">入库后后台处理</span><span className="paper-bibliography-followup-detail">自动补全元信息、查找与下载 PDF，进度及错误可在右上角通知中查看。</span>
  </section>;
}
