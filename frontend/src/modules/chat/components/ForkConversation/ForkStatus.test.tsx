import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ForkStatus from "./ForkStatus";
import type { useForkConversation } from "./useForkConversation";

const catalog = vi.hoisted(() => vi.fn());
vi.mock("../ChatModelSelector/api", () => ({ fetchChatModelCatalog: catalog }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
beforeEach(() => { catalog.mockReset(); });
afterEach(cleanup);
const fixture = () => ({ pending: false, phase: "idle", error: "", recoverable: [],
  retry: vi.fn(), resume: vi.fn(), selectModel: vi.fn(), dismissStatus: vi.fn(),
} as unknown as ReturnType<typeof useForkConversation>);

describe("Inline Fork status", () => {
  it("shows creation progress without a dialog or confirmation controls", () => {
    const fork = fixture(); fork.pending = true; fork.phase = "submitting";
    render(<ForkStatus source="source" fork={fork} />);
    expect(screen.getByRole("status")).toHaveTextContent("chat.fork.creating");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "chat.fork.create" })).not.toBeInTheDocument();
  });

  it("recovers the exact operation inline when the result is unknown", () => {
    const fork = fixture(); fork.phase = "unknown";
    fork.recoverable = [{ id: "same-key", source: "source", request: { source_history_id: "h1", expected_prefix_revision: "v1" } }];
    render(<ForkStatus source="source" fork={fork} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.fork.retry" }));
    expect(fork.resume).toHaveBeenCalledWith(fork.recoverable[0]);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("offers retry for a definite failure without a dialog", () => {
    const fork = fixture(); fork.phase = "failed"; fork.error = "SOURCE_CHANGED";
    render(<ForkStatus source="source" fork={fork} />);
    expect(screen.getByRole("alert")).toHaveTextContent("chat.fork.errors.SOURCE_CHANGED");
    fireEvent.click(screen.getByRole("button", { name: "chat.fork.retryCreate" }));
    expect(fork.retry).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("loads replacement models only for the unavailable-model case and reports an empty catalog", async () => {
    catalog.mockResolvedValue({ providers: [] });
    const fork = fixture(); fork.phase = "ready";
    fork.preview = { config_issues: [{ field: "model", reason: "MODEL_UNAVAILABLE" }] } as any;
    render(<ForkStatus source="source" fork={fork} />);
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("chat.fork.noModels"));
    expect(catalog).toHaveBeenCalledWith("source");
    expect(screen.getByRole("button", { name: "chat.fork.create" })).toBeDisabled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.fork.retryRead" }));
    await waitFor(() => expect(catalog).toHaveBeenCalledTimes(2));
  });
});
