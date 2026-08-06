import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import type { ColumnType } from "antd/es/table";
import {
  buildAbComparisonColumns,
  buildAbtestStreamingColumns,
  buildAnalysisActionableCaseColumns,
  buildAnalysisCaseColumns,
  buildAnalysisStreamingColumns,
  buildDatasetCaseColumns,
  buildDatasetStreamingColumns,
  buildEvalStreamingColumns,
  buildPxCaseDetailColumns,
} from "./columns";

const t = (key: string, options?: Record<string, unknown>) =>
  `${key}${options ? `:${JSON.stringify(options)}` : ""}`;

function renderCell<T>(column: ColumnType<T>, value: unknown, record: T) {
  const rendered = column.render?.(value, record, 0);
  const { container } = render(<>{rendered}</>);
  return container;
}

describe("buildAnalysisStreamingColumns", () => {
  it("renders the status label when a status is present", () => {
    const columns = buildAnalysisStreamingColumns(t);
    const traceCol = columns.find((c) => c.key === "traceSummaryStatus")!;
    const container = renderCell(traceCol, "running", { key: "1", caseId: "c1", traceSummaryStatus: "running" });
    expect(container.textContent).not.toBe("");
    expect(container.textContent).not.toBe("-");
  });

  it("renders a dash when the status is falsy", () => {
    const columns = buildAnalysisStreamingColumns(t);
    const classifyCol = columns.find((c) => c.key === "classifyCaseStatus")!;
    const container = renderCell(classifyCol, undefined, { key: "1", caseId: "c1" });
    expect(container.textContent).toBe("-");
  });
});

describe("buildEvalStreamingColumns", () => {
  it("includes case/answer/judge columns with widths", () => {
    const columns = buildEvalStreamingColumns(t);
    expect(columns.map((c) => c.key)).toEqual(["caseId", "answerStatus", "judgeStatus"]);
    expect(columns.every((c) => typeof c.width === "number")).toBe(true);
  });

  it("renders a dash for a missing judge status", () => {
    const columns = buildEvalStreamingColumns(t);
    const judgeCol = columns.find((c) => c.key === "judgeStatus")!;
    const container = renderCell(judgeCol, undefined, { key: "1", caseId: "c1" });
    expect(container.textContent).toBe("-");
  });
});

describe("buildAbtestStreamingColumns", () => {
  it("renders a status label for a present answer status", () => {
    const columns = buildAbtestStreamingColumns(t);
    const answerCol = columns.find((c) => c.key === "answerStatus")!;
    const container = renderCell(answerCol, "done", { key: "1", caseId: "c1", answerStatus: "done" });
    expect(container.textContent).not.toBe("-");
  });
});

describe("buildDatasetStreamingColumns", () => {
  it("renders dashes for missing prepare/generate statuses", () => {
    const columns = buildDatasetStreamingColumns(t);
    const prepareCol = columns.find((c) => c.key === "prepareStatus")!;
    const generateCol = columns.find((c) => c.key === "generateStatus")!;
    expect(renderCell(prepareCol, undefined, { key: "1", caseId: "c1" }).textContent).toBe("-");
    expect(renderCell(generateCol, undefined, { key: "1", caseId: "c1" }).textContent).toBe("-");
  });
});

describe("buildDatasetCaseColumns", () => {
  it("renders question/answer/reference cells with a title attribute", () => {
    const columns = buildDatasetCaseColumns(t);
    const questionCol = columns.find((c) => c.dataIndex === "question")!;
    const container = renderCell(questionCol, "what is rag?", {
      key: "1",
      caseId: "c1",
      question: "what is rag?",
      answer: "a",
      questionType: "factual",
      difficulty: "easy",
      references: "ref",
    });
    const span = container.querySelector("span");
    expect(span?.textContent).toBe("what is rag?");
    expect(span?.getAttribute("title")).toBe("what is rag?");
  });

  it("includes all expected column keys", () => {
    const columns = buildDatasetCaseColumns(t);
    expect(columns.map((c) => c.dataIndex)).toEqual([
      "caseId",
      "questionType",
      "difficulty",
      "question",
      "answer",
      "references",
    ]);
  });
});

describe("buildPxCaseDetailColumns", () => {
  it("renders the defect and reason columns with ellipsis spans", () => {
    const columns = buildPxCaseDetailColumns(t);
    const defectCol = columns.find((c) => c.dataIndex === "defect")!;
    const container = renderCell(defectCol, "some defect text", {
      key: "1",
      caseId: "c1",
      question: "q",
      score: "0.5",
      failureType: "type",
      defect: "some defect text",
      reason: "reason",
      traceId: "trace-1",
    });
    expect(container.textContent).toBe("some defect text");
  });
});

describe("buildAnalysisActionableCaseColumns", () => {
  it("renders the issue type cell with title", () => {
    const columns = buildAnalysisActionableCaseColumns(t);
    const issueCol = columns.find((c) => c.dataIndex === "issueType")!;
    const container = renderCell(issueCol, "retrieval_miss", {
      key: "1",
      caseId: "c1",
      issueType: "retrieval_miss",
      affectedBlock: "retrieval",
      failureMode: "mode",
      confidence: "0.9",
      reason: "reason",
      clusterId: "cluster-1",
      outlierScore: "0.1",
    });
    expect(container.querySelector("span")?.getAttribute("title")).toBe("retrieval_miss");
  });

  it("has no render function for confidence/clusterId/outlierScore (plain text cells)", () => {
    const columns = buildAnalysisActionableCaseColumns(t);
    const confidenceCol = columns.find((c) => c.dataIndex === "confidence")!;
    expect(confidenceCol.render).toBeUndefined();
  });
});

describe("buildAnalysisCaseColumns", () => {
  it("renders the coarse/fine category columns with ellipsis", () => {
    const columns = buildAnalysisCaseColumns(t);
    const coarseCol = columns.find((c) => c.dataIndex === "coarseCategory")!;
    const container = renderCell(coarseCol, "category A", {
      key: "1",
      caseId: "c1",
      coarseCategory: "category A",
      fineCategory: "fine",
      confidence: "0.8",
      lossScore: "0.1",
      quality: "good",
    });
    expect(container.textContent).toBe("category A");
  });
});

describe("buildAbComparisonColumns", () => {
  it("renders baseline/experiment/delta summaries with title attributes", () => {
    const columns = buildAbComparisonColumns(t);
    const deltaCol = columns.find((c) => c.dataIndex === "deltaSummary")!;
    const container = renderCell(deltaCol, "improved by 5%", {
      key: "1",
      category: "cat",
      baselineSummary: "base",
      experimentSummary: "exp",
      deltaSummary: "improved by 5%",
    });
    expect(container.querySelector("span")?.getAttribute("title")).toBe("improved by 5%");
  });

  it("includes exactly 4 columns", () => {
    const columns = buildAbComparisonColumns(t);
    expect(columns).toHaveLength(4);
  });
});
