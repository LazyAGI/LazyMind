import { BASE_URL } from "@/components/request";
import { type EvalDataset, type ExtraEvalStrategy, type PxMetricKey, type ThreadEventStage, type WorkflowResultKind, type WorkflowStepId } from "./types";

export const FIXED_EVAL_SET = "__none__";
export const FIXED_EXTRA_EVAL_STRATEGY: ExtraEvalStrategy = "generate";
export const DEFAULT_EVAL_CASE_COUNT = 10;
export const AGENT_API_BASE = `${BASE_URL}/api/core/agent`;
export const EVO_API_BASE = `${BASE_URL}/api/evo/v1/evo`;
export const SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY = "lazymind:self-evolution:last-thread";
export const DEPRECATED_SELF_EVOLUTION_THREAD_HISTORY_STORAGE_KEY = "lazymind:self-evolution:thread-history";

export const pxMetricFieldAliases: Record<PxMetricKey, string[]> = {
  answer_correctness: ["answer_correctness", "answer_correctness_avg", "correct_rate"],
  answer_score: ["answer_score", "answer_score_avg"],
  chunk_recall: ["chunk_recall", "chunk_recall_avg"],
  doc_recall: ["doc_recall", "doc_recall_avg"],
};

export const stageStepMap: Record<ThreadEventStage, WorkflowStepId> = {
  dataset: "dataset",
  eval: "px-report",
  analysis: "analysis",
  repair: "code-optimize",
  abtest: "ab-test",
};

export const stageResultKindMap: Record<ThreadEventStage, WorkflowResultKind> = {
  dataset: "datasets",
  eval: "eval-reports",
  analysis: "analysis-reports",
  repair: "diffs",
  abtest: "abtests",
};

export const stepStageMap: Record<WorkflowStepId, ThreadEventStage> = {
  dataset: "dataset",
  "px-report": "eval",
  analysis: "analysis",
  "code-optimize": "repair",
  "ab-test": "abtest",
};

export const terminalThreadEventTypes = new Set(["done", "thread.done", "thread.stop", "intent.done"]);
export const failedThreadEventTypes = new Set(["error", "thread.error", "intent.error", "USER_ACTIVE_THREAD_EXISTS"]);
export const inactiveTerminalThreadStatuses = new Set(["cancelled", "canceled", "ended", "failed", "error"]);

export const workflowStepOrder: WorkflowStepId[] = ["dataset", "px-report", "analysis", "code-optimize", "ab-test"];

export const evalSetPreviewData: EvalDataset = {
  eval_set_id: "b2e1616d-3d60-4327-9995-3d700e0a6e81",
  eval_name: "string4",
  kb_id: "ds_e030b437e04837ef4dbb952d45e16902",
  task_id: "379cffde-e43b-4f61-8310-d578f3094f6c",
  create_time: "2026-04-18 18:42:46",
  total_nums: 6,
  cases: [
    {
      case_id: "55b6c4b2-0bf7-4abf-8445-7d0e9acc553d",
      reference_doc: ["20384-【沪派江南】乡土行纪  第十四辑：水美林幽·风物万象.pdf"],
      reference_context: [
        "随后，大家来到大石皮村乡村生活馆，领略徐行草编文化的独特魅力。年轻的非遗传承人陈姣为大家讲述了徐行草编的历史渊源，作为江南著名的草编之乡，徐行草编以精湛的工艺和深厚的文化底蕴，于2008年入选第二批国家级非物质文化遗产名录。",
      ],
      is_deleted: false,
      question: "徐行草编于何时入选国家级非物质文化遗产名录？",
      question_type: 1,
      key_point: ["答题关键点"],
      ground_truth: "2008年入选第二批国家级非物质文化遗产名录",
    },
    {
      case_id: "04c504d7-ba7c-4bfb-8b78-5f1b3ca2802b",
      reference_doc: ["20387-【沪派江南】从水库村之变，理解沪派江南.pdf"],
      reference_context: [
        "水庫村採用“三師聯創”機制，保留了水網、疏浚河道、搭建23座橋梁打通水系；引入數字遊民打造全域全場景示範區，利用閒置空間開展100多場活動；設計宅基地安置點時保留菜地尊重傳統生活方式。",
      ],
      is_deleted: false,
      question:
        "水庫村在鄉村振興過程中，如何通過“三師聯創”機制，既保護了江南水鄉的水網風貌，又引入數字遊民實現產業創新，同時保留村民傳統生活方式？",
      question_type: 2,
      key_point: ["答题关键点"],
      ground_truth:
        "水庫村採用“三師聯創”機制，由規劃師、建築師、景觀師聯合設計，首先保留了水網密布的地理特徵，將河道疏浚整治、搭建橋梁打通水系、恢復濕地生態，而非填河為路，既保護了江南田園風貌又兼顧交通。同時引入數字遊民社區，利用村內閒置空間打造工作場景，開展各類活動為鄉村注入青年活力和產業機會，並通過企業會議、項目落地帶動經濟發展。在村民安置方面，設計江南風貌的別墅區並特意保留菜地，讓農戶延續種菜生活方式，避免城市化帶來的“不適應”。這種模式體現了生態保護、產業創新與文化傳承的協調發展。",
    },
  ],
};
