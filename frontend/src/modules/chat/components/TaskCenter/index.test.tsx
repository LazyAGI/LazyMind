import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";
import type { SubAgentTask } from "@/modules/chat/store/taskCenter";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import type { WorkflowSessionStep } from "@/modules/chat/store/workflowPanel";
import TaskCenter from "./index";

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
    });
  });

  afterEach(() => {
    cleanup();
    useTaskCenterStore.setState({
      tasksByConversation: {},
      _loadingTasks: {},
      _taskLoadErrors: {},
    });
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
    expect(screen.getByText("2 retries")).toBeInTheDocument();
    expect(screen.queryByText("raw trace analyze")).not.toBeInTheDocument();
    expect(screen.queryByText("taskCenter.filterAll")).not.toBeInTheDocument();
    expect(screen.getByRole("region", {
      name: "taskCenter.ordinaryThinking",
    })).toBeInTheDocument();
    expect(document.querySelector(".ordinary-summary-list")).not.toBeInTheDocument();
    expect(screen.getByText("Elapsed 25s")).toBeInTheDocument();
    expect(screen.getByText("Subtasks 10s")).toBeInTheDocument();
  });

  it("shows persisted workflow task names and reveals the full name on hover", async () => {
    render(
      <TaskCenter
        sessionId="conversation-1"
        developerMode={false}
        workflowSteps={workflowSteps}
      />,
    );

    expect(screen.getByText("image-workflow:analyze")).toBeInTheDocument();
    const taskName = screen.getByText("image-workflow:collect-3");
    fireEvent.mouseEnter(taskName);

    expect(await screen.findByRole("tooltip"))
      .toHaveTextContent("image-workflow:collect-3");
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

  it("renders tasks with overlapping execution intervals as accessible tabs", () => {
    useTaskCenterStore.setState({
      tasksByConversation: {
        "conversation-1": [
          {
            ...task("research-a", 1, "succeeded"),
            agent_type: "research",
            title: "Research A",
            created_at: iso(0),
            updated_at: iso(20),
          },
          {
            ...task("research-b", 2, "succeeded"),
            agent_type: "research",
            title: "Research B",
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
    expect(document.querySelector(".ordinary-task-trigger")).toBeDisabled();
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
    expect(screen.getByText("taskCenter.ordinaryThinkingProcessingComplete"))
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

    expect(document.querySelector(".ordinary-thinking-item:nth-child(2)"))
      .toHaveClass("is-complete");
    expect(screen.getByText("taskCenter.ordinaryThinkingProcessingComplete"))
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
    expect(panel?.querySelector(".ordinary-thinking-item.is-running"))
      .toHaveAttribute("aria-current", "step");
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
    expect(screen.getByText("taskCenter.ordinaryThinkingProcessingComplete"))
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
