import { useMemo } from "react";
import PaperGuideText from "./PaperGuideText";
import { parseGuideTable } from "./guideTable";
export default function PaperGuideTableText({
  text,
  language,
}: {
  text: string;
  language: string;
}) {
  const table = useMemo(() => parseGuideTable(text), [text]);
  const zh = language === "zh";
  if (!table)
    return (
      <span className="paper-guide-table-fallback">
        <PaperGuideText text={text} />
      </span>
    );
  return (
    <>
      {table.prefix && (
        <div className="paper-guide-table-prose">
          <PaperGuideText text={table.prefix} />
        </div>
      )}
      <div
        className="paper-guide-table-scroll"
        role="region"
        aria-label={zh ? "可横向滚动的导读表格" : "Scrollable guide table"}
        tabIndex={0}
        data-guide-atomic="table"
        data-guide-source-start={table.start}
        data-guide-source-end={table.end}
      >
        <table aria-label={zh ? "导读对照表" : "Guide comparison table"}>
          <thead>
            <tr>
              {table.headers.map((cell, i) => (
                <th key={i} scope="col" style={{ textAlign: table.align[i] }}>
                  <PaperGuideText text={cell} mapSource={false} />
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {table.rows.map((row, index) => (
              <tr key={index}>
                {row.map((cell, i) => (
                  <td key={i} style={{ textAlign: table.align[i] }}>
                    <PaperGuideText text={cell} mapSource={false} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {table.suffix && (
        <div className="paper-guide-table-prose">
          <PaperGuideText text={table.suffix} sourceOffset={table.end} />
        </div>
      )}
    </>
  );
}
