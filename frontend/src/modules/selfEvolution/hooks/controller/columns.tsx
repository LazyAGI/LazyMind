import type { ColumnsType } from "antd/es/table";
import type { AbComparisonRow } from "../../shared";
import { getStepStatusLabel } from "../../shared/runtimeState";
import type {
  TFunction,
  DatasetCasePreviewRow,
  DatasetStreamingRow,
  EvalStreamingRow,
  AbtestStreamingRow,
  AnalysisStreamingRow,
  PxCaseDetailRow,
  AnalysisCasePreviewRow,
  AnalysisActionableCaseRow,
} from "./types";

export function buildAnalysisStreamingColumns(
  t: TFunction,
): ColumnsType<AnalysisStreamingRow> {
  return [
    { title: "case", dataIndex: "caseId", key: "caseId", width: 116 },
    {
      title: t("selfEvolutionRun.analysisStreamingColTraceSummary"),
      dataIndex: "traceSummaryStatus",
      key: "traceSummaryStatus",
      width: 120,
      render: (status) => (status ? getStepStatusLabel(status) : "-"),
    },
    {
      title: t("selfEvolutionRun.analysisStreamingColClassifyCase"),
      dataIndex: "classifyCaseStatus",
      key: "classifyCaseStatus",
      width: 120,
      render: (status) => (status ? getStepStatusLabel(status) : "-"),
    },
  ];
}

export function buildEvalStreamingColumns(t: TFunction): ColumnsType<EvalStreamingRow> {
  return [
    { title: "case", dataIndex: "caseId", key: "caseId", width: 116 },
    {
      title: t("selfEvolutionRun.evalStreamingColAnswer"),
      dataIndex: "answerStatus",
      key: "answerStatus",
      width: 96,
      render: (status) => (status ? getStepStatusLabel(status) : "-"),
    },
    {
      title: t("selfEvolutionRun.evalStreamingColJudge"),
      dataIndex: "judgeStatus",
      key: "judgeStatus",
      width: 96,
      render: (status) => (status ? getStepStatusLabel(status) : "-"),
    },
  ];
}

export function buildAbtestStreamingColumns(t: TFunction): ColumnsType<AbtestStreamingRow> {
  return [
    { title: "case", dataIndex: "caseId", key: "caseId", width: 116 },
    {
      title: t("selfEvolutionRun.abtestStreamingColAnswer"),
      dataIndex: "answerStatus",
      key: "answerStatus",
      width: 108,
      render: (status) => (status ? getStepStatusLabel(status) : "-"),
    },
    {
      title: t("selfEvolutionRun.abtestStreamingColJudge"),
      dataIndex: "judgeStatus",
      key: "judgeStatus",
      width: 96,
      render: (status) => (status ? getStepStatusLabel(status) : "-"),
    },
  ];
}

export function buildDatasetStreamingColumns(t: TFunction): ColumnsType<DatasetStreamingRow> {
  return [
    { title: "case", dataIndex: "caseId", key: "caseId", width: 116 },
    {
      title: t("selfEvolutionRun.datasetStreamingColPrepare"),
      dataIndex: "prepareStatus",
      key: "prepareStatus",
      width: 96,
      render: (status) => (status ? getStepStatusLabel(status) : "-"),
    },
    {
      title: t("selfEvolutionRun.datasetStreamingColGenerate"),
      dataIndex: "generateStatus",
      key: "generateStatus",
      width: 96,
      render: (status) => (status ? getStepStatusLabel(status) : "-"),
    },
  ];
}

export function buildDatasetCaseColumns(t: TFunction): ColumnsType<DatasetCasePreviewRow> {
  return [
    { title: "case", dataIndex: "caseId", key: "caseId", width: 116 },
    { title: t("selfEvolutionRun.colType"), dataIndex: "questionType", key: "questionType", width: 92 },
    { title: t("selfEvolutionRun.colDifficulty"), dataIndex: "difficulty", key: "difficulty", width: 82 },
    {
      title: t("selfEvolutionRun.colQuestion"),
      dataIndex: "question",
      key: "question",
      width: 360,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: t("selfEvolutionRun.colAnswer"),
      dataIndex: "answer",
      key: "answer",
      width: 300,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: t("selfEvolutionRun.colReference"),
      dataIndex: "references",
      key: "references",
      width: 260,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
  ];
}

export function buildPxCaseDetailColumns(t: TFunction): ColumnsType<PxCaseDetailRow> {
  return [
    { title: "Case", dataIndex: "caseId", key: "caseId", width: 126 },
    {
      title: t("selfEvolutionRun.colQuestion"),
      dataIndex: "question",
      key: "question",
      width: 360,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    { title: "Score", dataIndex: "score", key: "score", width: 96 },
    {
      title: t("selfEvolutionRun.colFailureType"),
      dataIndex: "failureType",
      key: "failureType",
      width: 150,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Defect",
      dataIndex: "defect",
      key: "defect",
      width: 260,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Reason",
      dataIndex: "reason",
      key: "reason",
      width: 420,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Trace",
      dataIndex: "traceId",
      key: "traceId",
      width: 170,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
  ];
}

export function buildAnalysisActionableCaseColumns(
  t: TFunction,
): ColumnsType<AnalysisActionableCaseRow> {
  return [
    { title: "case", dataIndex: "caseId", key: "caseId", width: 120 },
    {
      title: t("selfEvolutionRun.colIssueType"),
      dataIndex: "issueType",
      key: "issueType",
      width: 200,
      render: (value: string) => (
        <span className="self-evolution-table-ellipsis" title={value}>
          {value}
        </span>
      ),
    },
    {
      title: t("selfEvolutionRun.colAffectedBlock"),
      dataIndex: "affectedBlock",
      key: "affectedBlock",
      width: 160,
      render: (value: string) => (
        <span className="self-evolution-table-ellipsis" title={value}>
          {value}
        </span>
      ),
    },
    {
      title: t("selfEvolutionRun.colFailureMode"),
      dataIndex: "failureMode",
      key: "failureMode",
      width: 180,
      render: (value: string) => (
        <span className="self-evolution-table-ellipsis" title={value}>
          {value}
        </span>
      ),
    },
    {
      title: t("selfEvolutionRun.colConfidence"),
      dataIndex: "confidence",
      key: "confidence",
      width: 90,
    },
    {
      title: t("selfEvolutionRun.colRootCauseReason"),
      dataIndex: "reason",
      key: "reason",
      width: 260,
      render: (value: string) => (
        <span className="self-evolution-table-ellipsis" title={value}>
          {value}
        </span>
      ),
    },
    {
      title: t("selfEvolutionRun.colClusterId"),
      dataIndex: "clusterId",
      key: "clusterId",
      width: 130,
    },
    {
      title: t("selfEvolutionRun.colOutlierScore"),
      dataIndex: "outlierScore",
      key: "outlierScore",
      width: 110,
    },
  ];
}

export function buildAnalysisCaseColumns(t: TFunction): ColumnsType<AnalysisCasePreviewRow> {
  return [
    { title: "case", dataIndex: "caseId", key: "caseId", width: 130 },
    { title: t("selfEvolutionRun.colCoarseCategory"), dataIndex: "coarseCategory", key: "coarseCategory", width: 180, render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span> },
    { title: t("selfEvolutionRun.colFineCategory"), dataIndex: "fineCategory", key: "fineCategory", width: 190, render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span> },
    { title: t("selfEvolutionRun.colConfidence"), dataIndex: "confidence", key: "confidence", width: 90 },
    { title: "loss", dataIndex: "lossScore", key: "lossScore", width: 90 },
    { title: t("selfEvolutionRun.colQuality"), dataIndex: "quality", key: "quality", width: 100 },
  ];
}

export function buildAbComparisonColumns(t: TFunction): ColumnsType<AbComparisonRow> {
  return [
    { title: t("selfEvolutionRun.colEvalCategory"), dataIndex: "category", key: "category", width: 140 },
    {
      title: t("selfEvolutionRun.colBaselineResult"),
      dataIndex: "baselineSummary",
      key: "baselineSummary",
      width: 320,
      render: (value: string) => (
        <span className="self-evolution-table-ellipsis" title={value}>
          {value}
        </span>
      ),
    },
    {
      title: t("selfEvolutionRun.colOptimizedResult"),
      dataIndex: "experimentSummary",
      key: "experimentSummary",
      width: 320,
      render: (value: string) => (
        <span className="self-evolution-table-ellipsis" title={value}>
          {value}
        </span>
      ),
    },
    {
      title: t("selfEvolutionRun.colChangeSummary"),
      dataIndex: "deltaSummary",
      key: "deltaSummary",
      width: 320,
      render: (value: string) => (
        <span className="self-evolution-table-ellipsis" title={value}>
          {value}
        </span>
      ),
    },
  ];
}
