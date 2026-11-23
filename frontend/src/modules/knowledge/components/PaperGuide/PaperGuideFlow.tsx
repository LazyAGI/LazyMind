import { Fragment } from "react";
import type { PaperGuideFlow as Flow } from "@/api/generated/core-client";
import "./PaperGuideFlow.scss";

function centers(count: number) {
  return Array.from({ length: count }, (_, index) => (index + 0.5) * 100 / count);
}

function Connector({ from, to }: { from: number; to: number }) {
  const starts = centers(from);
  const ends = centers(to);
  const all = [...starts, ...ends];
  return <div className="guide-flow-connector" aria-hidden="true">
    <svg viewBox="0 0 100 28" preserveAspectRatio="none">
      <path vectorEffect="non-scaling-stroke" d={[
        ...starts.map(x => `M ${x} 0 V 14`),
        `M ${Math.min(...all)} 14 H ${Math.max(...all)}`,
        ...ends.map(x => `M ${x} 14 V 27`),
      ].join(" ")} />
    </svg>
    {ends.map(x => <i key={x} style={{ left: `${x}%` }} />)}
  </div>;
}

export default function PaperGuideFlow({ flow, zh }: { flow: Flow; zh: boolean }) {
  const labels = zh
    ? ["领域", "存在问题", "提出方法", "主要模块", "实验验证"]
    : ["Field", "Problems", "Proposed method", "Main components", "Evaluation"];
  const rows = [[flow.field], flow.problems, [flow.method], flow.modules,
    flow.datasets.length ? flow.datasets : [zh ? "未报告数据集" : "No dataset reported"]];
  const columns = Math.max(...rows.map(row => row.length));
  return <figure className="guide-flow" aria-label={zh ? "论文脉络图" : "Paper at a glance"} tabIndex={0}>
    <dl style={{ minWidth: `calc(6em + ${columns * 6.6}em)` }}>
      {rows.map((row, index) => <Fragment key={labels[index]}>
        <dt>{labels[index]}</dt>
        <dd>
          <ul style={{ gridTemplateColumns: `repeat(${row.length}, minmax(0, 1fr))` }}>
            {row.map((node, i) => <li key={i}>{node}</li>)}
          </ul>
          {index + 1 < rows.length && <Connector from={row.length} to={rows[index + 1].length} />}
        </dd>
      </Fragment>)}
    </dl>
  </figure>;
}
