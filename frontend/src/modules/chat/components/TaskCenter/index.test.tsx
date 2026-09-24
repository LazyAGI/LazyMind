import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";
import type { SubAgentTask } from "@/modules/chat/store/taskCenter";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import type { WorkflowSessionStep } from "@/modules/chat/store/workflowPanel";
import TaskCenter from "./index";
import type { OrdinaryTaskView } from "@/modules/chat/types/ordinaryTask";
import { ordinary } from "./ordinaryTestFixtures";

vi.mock("@/modules/knowledge/components/FileViewer", () => ({ default: () => <div>File viewer</div> }));

vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-i18next")>()),
  useTranslation: () => ({
    t: ((key: string, values?: Record<string, unknown>) => {
      if (key === "taskCenter.ordinaryTaskLabel") {
        return `Subtask ${values?.index}`;
      }
      if (key === "taskCenter.ordinaryRetryCount") {
        return `${values?.count} retries`;
      }
      if (key === "taskCenter.ordinaryArtifactCount") {
        return `${values?.count} artifacts`;
      }
      if (key === "taskCenter.durationSeconds") {
        return `${values?.seconds}s`;
      }
      if (key === "taskCenter.ordinaryTotalDuration") {
        return `Elapsed ${values?.duration}`;
      }
      if (key === "taskCenter.ordinaryExecutionDuration") {
        return `Subtasks ${values?.duration}`;
      }
      if (key === "taskCenter.ordinaryCompletedSummary") {
        return `Completed ${values?.completed}/${values?.total}`;
      }
      if (key === "taskCenter.ordinaryIncompleteSummary") {
        return `${values?.count} remaining`;
      }
      if (key === "taskCenter.writingSubtaskSummary") {
        return `${values?.total} total · ${values?.completed} completed · ${values?.failed} failed`;
      }
      return key;
    }) as TFunction,
  }),
}));

const start = Date.parse("2026-08-20T06:00:00.000Z");
const iso = (seconds: number) => new Date(start + seconds * 1000).toISOString();

function task(
  id: string,
  seq: number,
  status: SubAgentTask["status"],
): SubAgentTask {
  return {
    task_id: id,
    conversation_id: "conversation-1",
    trigger_history_id: "history-1",
    seq_in_conversation: seq,
    created_at: iso(seq * 10),
    updated_at: iso(seq * 10 + 5),
    title: `image-workflow:${id}`,
    agent_type: "workflow_step",
    mode: "manual",
    status,
    progress_pct: status === "succeeded" ? 100 : 0,
    artifacts: [],
    sources: [],
    artifact_streams: [],
    execution_log: [{ type: "think", content: `raw trace ${id}` }],
  };
}

function step(
  taskId: string,
  stepId: string,
  attempt: number,
  validity: "effective" | "stale",
): WorkflowSessionStep {
  return {
    id: `${taskId}-attempt`,
    session_id: "session-1",
    step_id: stepId,
    attempt,
    task_id: taskId,
    status: validity === "effective" ? "succeeded" : "failed",
    validity,
    created_at: iso(attempt * 10),
    updated_at: iso(attempt * 10 + 5),
  };
}

const tasks = [
  task("analyze", 1, "succeeded"),
  task("collect-1", 2, "failed"),
  task("collect-2", 3, "failed"),
  task("collect-3", 4, "succeeded"),
];

const workflowSteps = [
  step("analyze", "analyze", 1, "effective"),
  step("collect-1", "collect", 1, "stale"),
  step("collect-2", "collect", 2, "stale"),
  step("collect-3", "collect", 3, "effective"),
];

describe("TaskCenter display modes", () => {
  beforeEach(() => {
    useTaskCenterStore.setState({
      tasksByConversation: { "conversation-1": tasks },
      _loadingTasks: {},
      _taskLoadErrors: {},
      runsByConversation: {},
    });
  });

  afterEach(() => {
    cleanup();
    useTaskCenterStore.setState({
      tasksByConversation: {},
      _loadingTasks: {},
      _taskLoadErrors: {},
      runsByConversation: {},
    });
  });

  it("shows the designed progress bar and estimated timeline for a completed plan", () => {
    const planned = {
      ...task("planned", 1, "succeeded"), agent_type: "research", progress_pct: 100,
      plan_steps: ["Read sales data", "Compare quarters", "Write report"],
    };
    useTaskCenterStore.setState({ tasksByConversation: { "conversation-1": [planned] } });
    render(<TaskCenter sessionId="conversation-1" developerMode={false} />);
    expect(screen.queryByText("taskCenter.ordinaryNoProcessSteps")).not.toBeInTheDocument();
    expect(screen.getByText("Read sales data")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "taskCenter.ordinaryPlan" })).toHaveAttribute("value", "100");
    expect(screen.getByText("taskCenter.ordinaryPlanDescription")).toBeInTheDocument();
    expect(document.querySelectorAll(".ordinary-plan-item.is-complete")).toHaveLength(3);
    expect(document.querySelectorAll(".ordinary-process-status")).toHaveLength(0);
  });

  it("updates estimated plan progress from public snapshots and keeps failed tasks below completion", () => {
    useTaskCenterStore.setState({ tasksByConversation: {} });
    const initial = ordinary("progress-live", { agent_type: "research", conversation_id: "conversation-1",
      status: "running", progress_pct: 25, plan_steps: ["Read", "Check", "Draft", "Publish"] });
    useTaskCenterStore.getState().applyOrdinarySnapshot("conversation-1", initial);
    render(<TaskCenter sessionId="conversation-1" />);
    expect(screen.getByRole("progressbar")).toHaveAttribute("value", "25");
    expect(screen.getByText("Check").closest("li")).toHaveAttribute("aria-current", "step");
    act(() => useTaskCenterStore.getState().applyOrdinarySnapshot("conversation-1", { ...initial, revision: 2, progress_pct: 75 }));
    expect(screen.getByRole("progressbar")).toHaveAttribute("value", "75");
    expect(screen.getByText("Publish").closest("li")).toHaveAttribute("aria-current", "step");
    act(() => useTaskCenterStore.getState().applyOrdinarySnapshot("conversation-1", { ...initial, revision: 3, status: "failed", progress_pct: 100 }));
    expect(screen.getByRole("progressbar")).toHaveAttribute("value", "99");
    expect(screen.getByText("Publish").closest("li")).toHaveClass("is-failed");
    expect(screen.queryByText("100%")).not.toBeInTheDocument();
  });

  it("shows the current public plan after live updates and drops it on a new execution", () => {
    useTaskCenterStore.setState({ tasksByConversation: {} });
    const initial = ordinary("plan-live", { agent_type: "research", conversation_id: "conversation-1", status: "running" });
    useTaskCenterStore.getState().applyOrdinarySnapshot("conversation-1", initial);
    render(<TaskCenter sessionId="conversation-1" />);
    expect(screen.getByText("taskCenter.ordinaryNoProcessSteps")).toBeInTheDocument();
    act(() => useTaskCenterStore.getState().applyOrdinarySnapshot("conversation-1", {
      ...initial, revision: 2, plan_steps: ["Read requirements", "Check constraints", "Write brief"],
    }));
    expect(screen.getByText("Read requirements")).toBeInTheDocument();
    expect(screen.queryByText("taskCenter.ordinaryNoProcessSteps")).not.toBeInTheDocument();
    expect(document.querySelectorAll(".ordinary-thinking-item.is-complete")).toHaveLength(0);
    expect(screen.getByRole("progressbar")).not.toHaveAttribute("value");
    act(() => useTaskCenterStore.getState().applyOrdinarySnapshot("conversation-1", {
      ...initial, revision: 3, display_key: "task:plan-live:next", execution_id: "next",
    }));
    expect(screen.queryByText("Read requirements")).not.toBeInTheDocument();
    expect(screen.getByText("taskCenter.ordinaryNoProcessSteps")).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("uses individual public step states regardless of the task percentage", () => {
    const process: OrdinaryTaskView = ordinary("real", {
      title: "Prepare report",
      process_state: "available",
      process_steps: [
        { step_id: "check", revision: 2, order: 2, title: "Check citations", status: "failed", started_at: null, finished_at: null, elapsed_ms: null },
        { step_id: "read", revision: 1, order: 1, title: "Read documents", status: "succeeded", started_at: null, finished_at: null, elapsed_ms: null },
      ],
    });
    useTaskCenterStore.setState({ tasksByConversation: { "conversation-1": [{ ...task("real", 1, "failed"), progress_pct: 99, ordinary: process }] } });
    render(<TaskCenter sessionId="conversation-1" />);
    expect(screen.getByText("Read documents").closest("li")).toHaveClass("is-complete");
    expect(screen.getByText("Check citations").closest("li")).toHaveClass("is-failed");
    expect([...document.querySelectorAll(".ordinary-thinking-item strong")].map(el => el.textContent)).toEqual(["Read documents", "Check citations"]);
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("retains the selected parallel task when another task receives updates", () => {
    const a = { ...task("a", 1, "running"), ordinary: ordinary("a", { title: "Task A", status: "running", parallel_group_id: "p" }) };
    const b = { ...task("b", 2, "succeeded"), ordinary: ordinary("b", { title: "Task B", parallel_group_id: "p" }) };
    useTaskCenterStore.setState({ tasksByConversation: { "conversation-1": [a, b] } });
    render(<TaskCenter sessionId="conversation-1" />);
    const tabB = screen.getByRole("tab", { name: /Task B/ });
    fireEvent.click(tabB);
    act(() => useTaskCenterStore.setState({ tasksByConversation: { "conversation-1": [{ ...a, progress_pct: 50 }, b] } }));
    expect(tabB).toHaveAttribute("aria-selected", "true");
  });

  it("preserves cancellation and does not turn it into a completed step", () => {
    const canceled = ordinary("cancel", { status: "canceled", process_state: "available", process_steps: [{ step_id: "s", revision: 1, order: 1, title: "Read source", status: "canceled", started_at: null, finished_at: null, elapsed_ms: null }] });
    useTaskCenterStore.setState({ tasksByConversation: { "conversation-1": [{ ...task("cancel", 1, "canceled"), ordinary: canceled }] } });
    render(<TaskCenter sessionId="conversation-1" />);
    expect(screen.getByText("Read source").closest("li")).toHaveClass("is-canceled");
    expect(screen.getAllByText("taskCenter.statusCanceled").length).toBeGreaterThan(0);
  });

  it("keeps final outputs separate and includes only explicitly bound artifact references", () => {
    const artifact = (id: string) => ({ artifact_id: id, revision: 1, producer_display_key: "task:outputs:1", name: id, content_type: "text/plain", size_bytes: 4, state: "ready" as const, preview_kind: null, capabilities: { preview: false, open: false, download: false }, created_at: iso(0) });
    const view = ordinary("outputs", { stage_artifacts: [artifact("draft"), artifact("final")] });
    useTaskCenterStore.setState({ tasksByConversation: { "conversation-1": [{ ...task("outputs", 1, "succeeded"), ordinary: view }] }, runsByConversation: { "conversation-1": [{ run_id: "run-1", revision: 1, final_output_refs: ["final", "separate"], final_artifacts: [artifact("separate")] }, { run_id: "old-run", revision: 1, final_output_refs: ["old-final"], final_artifacts: [artifact("old-final")] }] } });
    render(<TaskCenter sessionId="conversation-1" />);
    expect(screen.getAllByText("draft")).toHaveLength(1);
    expect(screen.getAllByText("final")).toHaveLength(2);
    expect(screen.getByText("separate")).toBeInTheDocument();
    expect(screen.queryByText("old-final")).not.toBeInTheDocument();
    expect(screen.getByRole("region", { name: /ordinaryFinalArtifacts/ })).toBeInTheDocument();
  });

  it("previews stage artifacts without reload controls and reports metadata pagination failures", async () => {
    const originalReload = useTaskCenterStore.getState().loadOrdinaryTask;
    const reload = vi.fn().mockRejectedValue(new Error("Metadata unavailable"));
    const view = ordinary("preview", { stage_artifacts: [{
      artifact_id: "report", revision: 1, producer_display_key: "task:preview:1", name: "report.md",
      content_type: "text/markdown", size_bytes: null, state: "ready", preview_kind: "text",
      capabilities: { preview: true, open: false, download: false }, inline_content: "Existing public report", created_at: iso(0),
    }] });
    view.pages.sources.next_cursor = "next-sources";
    useTaskCenterStore.setState({ loadOrdinaryTask: reload, tasksByConversation: {
      "conversation-1": [{ ...task("preview", 1, "succeeded"), ordinary: view }],
    } });
    try {
      render(<TaskCenter sessionId="conversation-1" />);
      fireEvent.click(screen.getByRole("button", { name: "report.md" }));
      const dialog = screen.getByRole("dialog");
      expect(within(dialog).queryByRole("button", { name: "taskCenter.ordinaryReload" })).not.toBeInTheDocument();
      expect(reload).not.toHaveBeenCalled();
      expect(within(dialog).getByText("Existing public report")).toBeInTheDocument();
      fireEvent.keyDown(dialog, { key: "Escape" });
      fireEvent.click(screen.getByRole("button", { name: "taskCenter.ordinaryLoadMore" }));
      expect(await screen.findByRole("alert")).toHaveTextContent("taskCenter.ordinaryLoadError");
      expect(reload).toHaveBeenLastCalledWith("conversation-1", "preview", "sources", "next-sources");
    } finally {
      act(() => useTaskCenterStore.setState({ loadOrdinaryTask: originalReload }));
    }
  });

  it("shows a logical step axis without raw traces for ordinary users", () => {
    render(
      <TaskCenter
        sessionId="conversation-1"
        developerMode={false}
        workflowSteps={workflowSteps}
      />,
    );

    expect(document.querySelectorAll(".ordinary-task-card")).toHaveLength(2);
    expect(document.querySelectorAll(".ordinary-step-node")).toHaveLength(2);
    expect(document.querySelector(".ordinary-task-marker")).not.toBeInTheDocument();
    expect(screen.queryByText("2 retries")).not.toBeInTheDocument();
    expect(screen.queryByText("raw trace analyze")).not.toBeInTheDocument();
    expect(screen.queryByText("taskCenter.filterAll")).not.toBeInTheDocument();
    expect(screen.getByRole("region", {
      name: "taskCenter.ordinaryThinking",
    })).toBeInTheDocument();
    expect(document.querySelector(".ordinary-summary-list")).not.toBeInTheDocument();
    expect(screen.queryByText("Elapsed 25s")).not.toBeInTheDocument();
    expect(screen.queryByText("Subtasks 10s")).not.toBeInTheDocument();
  });

  it("does not expose internal workflow identifiers as public task names", () => {
    render(<TaskCenter sessionId="conversation-1" developerMode={false} workflowSteps={workflowSteps} />);
    expect(screen.queryByText("image-workflow:analyze")).not.toBeInTheDocument();
    expect(screen.getByText("Subtask 1")).toBeInTheDocument();
  });

  it("keeps every attempt and the full execution trace in developer mode", () => {
    render(
      <TaskCenter
        sessionId="conversation-1"
        developerMode
        workflowSteps={workflowSteps}
      />,
    );

    expect(document.querySelectorAll(".task-card")).toHaveLength(4);
    expect(screen.getByText("raw trace analyze")).toBeInTheDocument();
    expect(screen.getByText("taskCenter.filterAll")).toBeInTheDocument();
  });

  it("shows only the user query as the run instruction", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [{
          ...task("query-only", 1, "running"),
          query: "继续生成三页 PPT",
          objective: "SYSTEM: expanded workflow prompt that must stay hidden",
        }],
      },
    });

    render(
      <TaskCenter
        sessionId="conversation-1"
        developerMode
        workflowSteps={[]}
      />,
    );

    expect(screen.getByText("继续生成三页 PPT")).toBeInTheDocument();
    expect(screen.queryByText(/expanded workflow prompt/)).not.toBeInTheDocument();
  });

  it("renders explicitly grouped tasks as accessible tabs", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [
          {
            ...task("research-a", 1, "succeeded"),
            agent_type: "research",
            title: "Research A",
            ordinary: ordinary("research-a", { title: "Research A", parallel_group_id: "research" }),
            created_at: iso(0),
            updated_at: iso(20),
          },
          {
            ...task("research-b", 2, "succeeded"),
            agent_type: "research",
            title: "Research B",
            ordinary: ordinary("research-b", { title: "Research B", parallel_group_id: "research" }),
            created_at: iso(2),
            updated_at: iso(18),
          },
        ],
      },
    });

    render(<TaskCenter sessionId="conversation-1" developerMode={false} />);

    const tabs = screen.getAllByRole("tab");
    expect(tabs).toHaveLength(2);
    expect(screen.getByRole("tabpanel")).toBeInTheDocument();
    expect(document.querySelector(".ordinary-parallel-card")).toBeInTheDocument();

    tabs[0].focus();
    fireEvent.keyDown(tabs[0], { key: "ArrowRight" });
    expect(tabs[1]).toHaveAttribute("aria-selected", "true");
    expect(tabs[1]).toHaveFocus();
    fireEvent.keyDown(tabs[1], { key: "Home" });
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(tabs[0]).toHaveFocus();
    fireEvent.keyDown(tabs[0], { key: "End" });
    expect(tabs[1]).toHaveAttribute("aria-selected", "true");
    expect(tabs[1]).toHaveFocus();
  });

  it("expands the active task when tasks arrive after the empty state", () => {
    useTaskCenterStore.setState({ tasksByConversation: {} });
    render(<TaskCenter sessionId="conversation-1" developerMode={false} />);

    expect(screen.getByText("taskCenter.empty")).toBeInTheDocument();

    act(() => {
      useTaskCenterStore.setState({
        tasksByConversation: {
          "conversation-1": [task("late-running", 1, "running")],
        },
      });
    });

    expect(document.querySelector(".ordinary-task-trigger"))
      .toHaveAttribute("aria-expanded", "true");
  });

  it("does not describe pending work as complete", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [task("pending", 1, "pending")],
      },
    });

    render(<TaskCenter sessionId="conversation-1" developerMode={false} />);

    expect(screen.getByText("1 remaining"))
      .toBeInTheDocument();
    expect(screen.queryByText("taskCenter.ordinaryAllComplete"))
      .not.toBeInTheDocument();
    expect(document.querySelector(".ordinary-task-trigger"))
      .toHaveAttribute("aria-expanded", "false");
  });

  it("uses all visible workflow milestones in the completion summary", () => {
    render(
      <TaskCenter
        sessionId="conversation-1"
        developerMode={false}
        workflowSteps={workflowSteps}
        plannedCount={3}
      />,
    );

    expect(document.querySelector(".ordinary-task-count")).toHaveTextContent("3");
    expect(document.querySelectorAll(".ordinary-task-card")).toHaveLength(2);
    expect(screen.getByText("Completed 2/3")).toBeInTheDocument();
    expect(screen.getByText("1 remaining")).toBeInTheDocument();
    expect(screen.queryByText("taskCenter.ordinaryAllComplete"))
      .not.toBeInTheDocument();
  });

  it("keeps hosted workflow attempts visible without exposing fake details", () => {
    useTaskCenterStore.setState({ tasksByConversation: {} });
    render(
      <TaskCenter
        sessionId="conversation-1"
        developerMode={false}
        workflowSteps={[
          {
            ...step("hosted-task", "hosted-step", 1, "effective"),
            status: "running",
          },
        ]}
      />,
    );

    expect(document.querySelectorAll(".ordinary-task-card")).toHaveLength(1);
    expect(document.querySelector(".ordinary-task-trigger")).not.toBeDisabled();
    expect(screen.queryByText("raw trace hosted-task")).not.toBeInTheDocument();
  });

  it("keeps tool parameters and JSON out of the ordinary process timeline", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [{
          ...task("unsafe-summary", 1, "succeeded"),
          current_phase: "KBToolkit_list_knowledge_bases",
          summary: '{"tool_call":"search","params":{"api_key":"secret"}}',
        }],
      },
    });

    render(<TaskCenter sessionId="conversation-1" developerMode={false} />);

    expect(screen.queryByText(/api_key|KBToolkit|secret/)).not.toBeInTheDocument();
    expect(screen.getByText("taskCenter.ordinaryNoProcessSteps"))
      .toBeInTheDocument();
  });

  it("uses the workflow attempt state inside the public process timeline", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [task("authoritative", 1, "failed")],
      },
    });

    render(
      <TaskCenter
        sessionId="conversation-1"
        developerMode={false}
        workflowSteps={[step("authoritative", "authoritative", 1, "effective")]}
      />,
    );

    expect(document.querySelector(".ordinary-task-card")).toHaveClass("is-complete");
    expect(screen.getByText("taskCenter.ordinaryNoProcessSteps"))
      .toBeInTheDocument();
    expect(screen.queryByText("taskCenter.ordinarySummaryFailed"))
      .not.toBeInTheDocument();
  });

  it("renders running details as a static process axis without nested accordions", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [{
          ...task("running-detail", 1, "running"),
          progress_pct: 35,
          artifacts: [{ slot: "report", content_type: "text", seq: 1, value: {} }],
          sources: [{
            source_type: "external",
            title: "Example source",
            url: "https://example.com/article",
            content: "A concise public source description.",
          }],
        }],
      },
    });

    render(<TaskCenter sessionId="conversation-1" developerMode={false} />);

    const panel = document.querySelector(".ordinary-task-panel");
    const trigger = document.querySelector(".ordinary-task-trigger");
    expect(panel).toHaveAttribute("role", "region");
    expect(trigger).toHaveAttribute("aria-controls", panel?.id);
    expect(panel).toHaveAttribute("aria-labelledby", trigger?.id);
    expect(panel?.querySelectorAll("button")).toHaveLength(0);
    expect(panel?.querySelector(".ant-progress")).not.toBeInTheDocument();
    expect(panel?.querySelector(".ordinary-thinking-item.is-running")).not.toBeInTheDocument();
    expect(screen.getByText("taskCenter.ordinaryThinking")).toBeInTheDocument();
    expect(screen.getByText("taskCenter.ordinarySources")).toBeInTheDocument();
  });

  it("does not read private task fields in the ordinary process projection", () => {
    const privateTask = task("private-fields", 1, "succeeded");
    Object.defineProperties(privateTask, {
      current_phase: { get: () => { throw new Error("current_phase read"); } },
      summary: { get: () => { throw new Error("summary read"); } },
      execution_log: { get: () => { throw new Error("execution_log read"); } },
    });
    useTaskCenterStore.setState({
      tasksByConversation: { "conversation-1": [privateTask] },
    });

    expect(() => render(
      <TaskCenter sessionId="conversation-1" developerMode={false} />,
    )).not.toThrow();
    expect(screen.getByText("taskCenter.ordinaryNoProcessSteps"))
      .toBeInTheDocument();
  });

  it("uses native links for public sources and summarizes dependencies", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [{
          ...task("sourced", 1, "succeeded"),
          input_slots: ["prior-result"],
          sources: [{
            source_type: "external",
            title: "Example source",
            url: "https://example.com/article",
          }, {
            source_type: "external",
            title: "Unsafe source",
            url: "javascript:alert(1)",
          }],
        }],
      },
    });

    render(<TaskCenter sessionId="conversation-1" developerMode={false} />);

    expect(screen.getByText("taskCenter.ordinaryDependencyCount"))
      .toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Example source/ }))
      .toHaveAttribute("href", "https://example.com/article");
    expect(screen.getByRole("link", { name: /Example source/ }))
      .toHaveAttribute("rel", "noopener noreferrer");
    expect(screen.getByRole("link", { name: /Example source/ }))
      .toHaveAttribute("target", "_blank");
    expect(screen.queryByRole("link", { name: /Unsafe source/ }))
      .not.toBeInTheDocument();
    expect(screen.getByText("Unsafe source")).toBeInTheDocument();
  });

  it("summarizes writing subtask outcomes and shows the actual search tool", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [{
          ...task("writer", 1, "succeeded"),
          writing_subtasks: [
            {
              subtask_id: "retrieve-1",
              node_id: "section-1",
              question: "Find evidence",
              subtask_type: "retrieve",
              status: "completed",
              retry_count: 0,
              tools_used: ["sciverse_search", "llm"],
            },
            {
              subtask_id: "retrieve-2",
              node_id: "section-2",
              question: "Find another source",
              subtask_type: "retrieve",
              status: "failed",
              retry_count: 1,
              tools_used: ["google_search"],
            },
          ],
        }],
      },
    });

    render(<TaskCenter sessionId="conversation-1" developerMode />);

    expect(screen.getByText(
      "taskCenter.writingSubtasks (2 total · 1 completed · 1 failed)",
    )).toBeInTheDocument();
    expect(screen.getByText(/taskCenter\.writingSubtaskTool_sciverse_search/))
      .toBeInTheDocument();
  });

  it("distinguishes loading and load failures from a true empty state", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {},
      _loadingTasks: { "conversation-1": true },
      _taskLoadErrors: {},
      runsByConversation: {},
    });
    const { rerender } = render(
      <TaskCenter sessionId="conversation-1" developerMode={false} />,
    );

    expect(screen.getByRole("status"))
      .toHaveTextContent("taskCenter.ordinaryLoading");

    act(() => {
      useTaskCenterStore.setState({
        _loadingTasks: {},
        _taskLoadErrors: { "conversation-1": true },
      });
    });
    rerender(<TaskCenter sessionId="conversation-1" developerMode={false} />);

    expect(screen.getByRole("alert"))
      .toHaveTextContent("taskCenter.ordinaryLoadError");
    expect(screen.getByRole("button", { name: "common.retry" }))
      .toBeInTheDocument();

    act(() => {
      useTaskCenterStore.setState({
        tasksByConversation: {
          "conversation-1": [task("cached", 1, "running")],
        },
        _taskLoadErrors: { "conversation-1": true },
      });
    });
    rerender(<TaskCenter sessionId="conversation-1" developerMode={false} />);

    expect(screen.getByRole("alert"))
      .toHaveTextContent("taskCenter.ordinaryStaleData");
    expect(document.querySelectorAll(".ordinary-task-card")).toHaveLength(1);
  });
});
