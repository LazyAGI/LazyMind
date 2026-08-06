import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor, within } from "@/test/testUtils";
import ScheduleList from "./ScheduleList";
import type { AutomationGroup, Schedule } from "./api";

const listSchedulesMock = vi.hoisted(() => vi.fn());
const listAutomationGroupsMock = vi.hoisted(() => vi.fn());
const createScheduleMock = vi.hoisted(() => vi.fn());
const updateScheduleMock = vi.hoisted(() => vi.fn());
const deleteScheduleMock = vi.hoisted(() => vi.fn());
const cancelScheduleMock = vi.hoisted(() => vi.fn());
const enableScheduleMock = vi.hoisted(() => vi.fn());
const runScheduleNowMock = vi.hoisted(() => vi.fn());
const deleteAutomationGroupMock = vi.hoisted(() => vi.fn());
const moveScheduleMock = vi.hoisted(() => vi.fn());
const batchCreateAutomationGroupMock = vi.hoisted(() => vi.fn());
const listScheduleTasksMock = vi.hoisted(() => vi.fn());

vi.mock("./api", () => ({
  listSchedules: listSchedulesMock,
  listAutomationGroups: listAutomationGroupsMock,
  createSchedule: createScheduleMock,
  updateSchedule: updateScheduleMock,
  deleteSchedule: deleteScheduleMock,
  cancelSchedule: cancelScheduleMock,
  enableSchedule: enableScheduleMock,
  runScheduleNow: runScheduleNowMock,
  deleteAutomationGroup: deleteAutomationGroupMock,
  moveSchedule: moveScheduleMock,
  batchCreateAutomationGroup: batchCreateAutomationGroupMock,
  listScheduleTasks: listScheduleTasksMock,
}));

const datasetServiceListDatasetsMock = vi.hoisted(() => vi.fn());
vi.mock("@/modules/chat/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: datasetServiceListDatasetsMock,
  }),
}));

vi.mock("@/modules/chat/utils/chunkUpload", () => ({
  uploadFileInChunks: vi.fn().mockResolvedValue("uploaded/path"),
}));

const axiosGetMock = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({
  axiosInstance: { get: axiosGetMock },
  BASE_URL: "http://localhost",
  localizeErrorCode: (code: string) => code,
}));

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return { ...actual, useNavigate: () => navigateMock };
});

function schedule(overrides: Partial<Schedule> = {}): Schedule {
  return {
    id: "s1",
    user_id: "u1",
    name: "Daily digest",
    remark: "",
    cron_expr: "0 9 * * 1,2,3,4,5",
    timezone: "Asia/Shanghai",
    prompt_template: "Summarize today's news",
    group_position: 0,
    enabled: true,
    run_count: 3,
    next_run_at: "2024-06-01T01:00:00Z",
    created_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

function group(overrides: Partial<AutomationGroup> = {}): AutomationGroup {
  return {
    id: "g1",
    name: "Marketing",
    remark: "",
    timezone: "Asia/Shanghai",
    enabled: true,
    task_count: 1,
    created_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("ScheduleList", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    listSchedulesMock.mockReset().mockResolvedValue({ items: [schedule()], total: 1 });
    listAutomationGroupsMock.mockReset().mockResolvedValue({ items: [], total: 0 });
    createScheduleMock.mockReset().mockResolvedValue(schedule({ id: "s2" }));
    updateScheduleMock.mockReset().mockResolvedValue(schedule());
    deleteScheduleMock.mockReset().mockResolvedValue(undefined);
    cancelScheduleMock.mockReset().mockResolvedValue(undefined);
    enableScheduleMock.mockReset().mockResolvedValue(schedule());
    runScheduleNowMock.mockReset().mockResolvedValue({ task_id: "t1", conversation_id: "c1" });
    deleteAutomationGroupMock.mockReset().mockResolvedValue(undefined);
    moveScheduleMock.mockReset().mockResolvedValue(undefined);
    batchCreateAutomationGroupMock.mockReset().mockResolvedValue({ group_id: "g1", schedule_ids: {} });
    listScheduleTasksMock.mockReset().mockResolvedValue({ items: [], total: 0 });
    datasetServiceListDatasetsMock.mockReset().mockResolvedValue({ data: { datasets: [] } });
    axiosGetMock.mockReset().mockResolvedValue({ data: { data: { ready: true } } });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads schedules and groups when active", async () => {
    renderWithProviders(<ScheduleList active />);
    await waitFor(() => expect(listSchedulesMock).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Daily digest")).toBeInTheDocument();
  });

  it("does not load schedules when inactive", async () => {
    renderWithProviders(<ScheduleList active={false} />);
    // Let the unrelated KB-options/embedding-ready effects settle before asserting.
    await waitFor(() => expect(datasetServiceListDatasetsMock).toHaveBeenCalled());
    expect(listSchedulesMock).not.toHaveBeenCalled();
  });

  it("filters the displayed schedules by the search keyword", async () => {
    renderWithProviders(<ScheduleList active />);
    await screen.findByText("Daily digest");

    fireEvent.change(screen.getByPlaceholderText("taskCenter.scheduleSearchPlaceholder"), {
      target: { value: "no such schedule" },
    });

    expect(screen.queryByText("Daily digest")).not.toBeInTheDocument();
  });

  it("disables a schedule via the enabled switch", async () => {
    renderWithProviders(<ScheduleList active />);
    await screen.findByText("Daily digest");

    const switchInput = document.querySelector(".ant-switch") as HTMLElement;
    fireEvent.click(switchInput);

    await waitFor(() => expect(cancelScheduleMock).toHaveBeenCalledWith("s1"));
  });

  it("triggers a run-now request when the run button is clicked", async () => {
    renderWithProviders(<ScheduleList active />);
    await screen.findByText("Daily digest");

    fireEvent.click(screen.getByText("taskCenter.scheduleRunNow"));

    await waitFor(() => expect(runScheduleNowMock).toHaveBeenCalledWith("s1"));
  });

  it("deletes a schedule after confirming the delete modal", async () => {
    renderWithProviders(<ScheduleList active />);
    await screen.findByText("Daily digest");

    fireEvent.click(screen.getByLabelText("taskCenter.scheduleActions"));
    fireEvent.click(await screen.findByText("taskCenter.scheduleDelete"));

    const confirmModal = await screen.findByRole("dialog", {
      name: /taskCenter.scheduleDeleteConfirmTitle/,
    });
    fireEvent.click(within(confirmModal).getByText("taskCenter.scheduleDeleteOk"));

    await waitFor(() => expect(deleteScheduleMock).toHaveBeenCalledWith("s1"));
  });

  it("opens the create-schedule modal and submits a new schedule", async () => {
    renderWithProviders(<ScheduleList active />);
    await screen.findByText("Daily digest");

    fireEvent.click(screen.getByText("新建定时任务"));
    expect(screen.getByText("taskCenter.scheduleDescription")).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("请输入任务名称"), {
      target: { value: "New schedule" },
    });
    fireEvent.change(screen.getByPlaceholderText("taskCenter.scheduleDescriptionPlaceholder"), {
      target: { value: "Do the thing" },
    });

    fireEvent.click(screen.getByText("taskCenter.scheduleCreateBtn"));

    await waitFor(() =>
      expect(createScheduleMock).toHaveBeenCalledWith(
        expect.objectContaining({ name: "New schedule", prompt_template: "Do the thing" }),
      ),
    );
  });

  it("renders groups in the groups workspace view and shows the group card", async () => {
    listAutomationGroupsMock.mockResolvedValue({ items: [group()], total: 1 });
    listSchedulesMock.mockResolvedValue({
      items: [schedule({ group_id: "g1" })],
      total: 1,
    });

    renderWithProviders(<ScheduleList active />);
    await waitFor(() => expect(listAutomationGroupsMock).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Marketing")).toBeInTheDocument();
  });
});
