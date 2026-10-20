import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import AskCard, { type AskPending } from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

describe("AskCard read-only history", () => {
  it.each([
    { type: "single", value: "old-option", otherText: "" },
    { type: "multiple", value: ["new-option"], otherText: "" },
  ])("discards saved answers incompatible with the current question: $type", async (saved) => {
    const submit = vi.fn().mockResolvedValue(false);
    render(<AskCard askPending={{ ask_id: "new", questions: [{ text: "New?", type: "single", choices: ["new-option"] }] }}
      savedAnswers={{ 0: saved } as any} onSubmit={submit} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.askCardSubmit" }));
    await waitFor(() => expect(submit).toHaveBeenCalled());
    expect(submit.mock.calls[0][0].structured.questions[0].answer).toBeNull();
  });

  const deletion: AskPending = {
    ask_id: "env-delete",
    user_env_delete: { id: "env-test", name: "test_api_key", expected_updated_at: "2026-09-23T00:00:00Z" },
    questions: [{ text: "Delete test_api_key?", type: "boolean", choices: ["__ask_user_yes__", "__ask_user_no__"] }],
  };

  it.each(["yes", "no"])("requires an explicit %s deletion decision", (choice) => {
    const onSubmit = vi.fn().mockResolvedValue(false);
    render(<AskCard askPending={deletion} onSubmit={onSubmit} />);
    const submit = screen.getByRole("button", { name: "chat.askCardSubmit" });
    expect(submit).toBeDisabled();
    fireEvent.click(submit);
    expect(onSubmit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: `common.${choice}` }));
    expect(submit).toBeEnabled();
  });

  it("renders legacy deletion confirmations that have no questions", () => {
    const onSubmit = vi.fn().mockResolvedValue(false);
    render(<AskCard askPending={{
      ask_id: "legacy-env-delete",
      user_env_delete: { id: "env-test", name: "test_api_key", expected_updated_at: "2026-09-23T00:00:00Z" },
      questions: [],
    }} onSubmit={onSubmit} />);
    expect(screen.getByText("settingsPage.envVars.deleteConfirm")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "common.yes" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "common.no" })).toBeInTheDocument();
  });

  it.each(["false", "reject"])("preserves the answer and allows retry after %s", async (failure) => {
    let resolve!: (value: boolean) => void;
    let reject!: (reason: Error) => void;
    const onSubmit = vi.fn().mockImplementationOnce(() => new Promise<boolean>((res, rej) => {
      resolve = res;
      reject = rej;
    })).mockResolvedValue(false);
    render(<AskCard askPending={deletion} onSubmit={onSubmit} />);
    fireEvent.click(screen.getByRole("button", { name: "common.no" }));
    const submit = screen.getByRole("button", { name: "chat.askCardSubmit" });
    fireEvent.click(submit);
    fireEvent.click(submit);
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(submit).toBeDisabled();
    expect(screen.getByRole("button", { name: "common.yes" })).toBeDisabled();
    await act(async () => {
      if (failure === "reject") reject(new Error("request failed"));
      else resolve(false);
    });
    await waitFor(() => expect(submit).toBeEnabled());
    fireEvent.click(submit);
    expect(onSubmit).toHaveBeenCalledTimes(2);
    expect(onSubmit.mock.calls[1][0].structured.questions[0].answer.value).toBe("__ask_user_no__");
    await waitFor(() => expect(submit).toBeEnabled());
  });

  it("still allows ordinary questions to be skipped", async () => {
    const onSubmit = vi.fn();
    render(<AskCard askPending={{ ask_id: "ordinary", questions: [{ text: "Optional?", type: "text" }] }} onSubmit={onSubmit} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.askCardSubmit" }));
    expect(onSubmit.mock.calls[0][0].structured.questions[0].answer).toBeNull();
    await waitFor(() => expect(screen.queryByRole("button", { name: "chat.askCardSubmit" })).toBeNull());
  });

  it.each(["yes", "no"])("localizes boolean %s in the message but preserves its structured token", async (choice) => {
    const onSubmit = vi.fn();
    render(<AskCard askPending={{
      ask_id: "env-delete", title: "Delete variable",
      questions: [{ text: "Delete test_api_key?", type: "boolean", choices: ["__ask_user_yes__", "__ask_user_no__"] }],
    }} onSubmit={onSubmit} />);
    fireEvent.click(screen.getByRole("button", { name: `common.${choice}` }));
    fireEvent.click(screen.getByRole("button", { name: "chat.askCardSubmit" }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      text: `Delete test_api_key?: common.${choice}`,
      structured: expect.objectContaining({ questions: [expect.objectContaining({
        answer: { type: "boolean", value: `__ask_user_${choice}__` },
      })] }),
    }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "chat.askCardSubmit" })).toBeNull());
  });

  it("keeps answers immutable while allowing previous, next, and direct page navigation", () => {
    const onSubmit = vi.fn();
    const onAnswerChange = vi.fn();
    render(
      <AskCard
        askPending={{
          ask_id: "ask-history",
          questions: [
            { text: "第一题", type: "single", choices: ["答案 A", "答案 B"] },
            { text: "第二题", type: "text" },
          ],
        }}
        disabled
        savedAnswers={{
          0: { type: "single", value: "答案 A", otherText: "" },
          1: { type: "text", value: "已保存答案" },
        }}
        onSubmit={onSubmit}
        onAnswerChange={onAnswerChange}
      />,
    );

    expect(screen.getByRole("radio", { name: "答案 A" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /chat\.askCardNext/ }));
    expect(screen.getByText("第二题")).toBeInTheDocument();
    expect(screen.getByDisplayValue("已保存答案")).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Go to question 1" }));
    expect(screen.getByText("第一题")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Go to question 2" }));
    fireEvent.click(screen.getByRole("button", { name: /chat\.askCardPrev/ }));
    expect(screen.getByText("第一题")).toBeInTheDocument();
    expect(onAnswerChange).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});

vi.mock("@/components/request", () => ({ axiosInstance: { post: vi.fn() }, BASE_URL: "" }));

import { axiosInstance } from "@/components/request";

describe("Knowledge management confirmation", () => {
  const pending: AskPending = {
    ask_id: "kb-confirm",
    knowledge_management: { confirmation_id: "kb-confirm", arguments_digest: "args-digest", snapshot: "version-1" },
    questions: [{ text: "Delete Papers?", type: "boolean", choices: ["__ask_user_yes__", "__ask_user_no__"] }],
  };

  it.each([true, false])("shows preview scope and impact with top-level labels %s", (hasLabels) => {
    render(<AskCard askPending={{ ...pending,
      title: hasLabels ? "Delete archive" : undefined,
      description: hasLabels ? "Papers / Archive" : undefined,
      knowledge_management_preview: {
        title: "Delete archive", description: "Papers / Archive",
        resources: [{ resource_ref: { kind: "folder", knowledge_base_id: "kb-1", directory_id: "archive-1" },
          name: "Archive", path: "Papers/Archive" }],
        destination: { resource_ref: { knowledge_base_id: "kb-2" }, name: "Reviewed", path: "Research/Reviewed" },
        document_count: 12, folder_count: 2, total_bytes: 8192,
        counts: { references: 3 }, impact: ["Remove original documents and search entries"],
      },
    }} onSubmit={vi.fn()} />);
    expect(screen.getAllByText("Papers / Archive")).toHaveLength(1);
    expect(screen.getAllByText("Delete archive")).toHaveLength(1);
    const card = screen.getByLabelText("chat.askCardAria");
    expect(card).toHaveTextContent("kb-1");
    expect(card).toHaveTextContent("archive-1");
    expect(card).toHaveTextContent("Papers/Archive");
    expect(card).toHaveTextContent("Research/Reviewed");
    expect(card).not.toHaveTextContent("[object Object]");
    expect(card).toHaveTextContent("12");
    expect(card).toHaveTextContent("8192");
    expect(card).toHaveTextContent("references: 3");
    expect(card).toHaveTextContent("Remove original documents and search entries");
  });

  it.each(["yes", "no"])("submits explicit %s to the bound decision endpoint without a chat message", async (choice) => {
    vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { data: { status: "allowed" } } });
    const onSubmit = vi.fn();
    const onDecisionSubmitted = vi.fn();
    render(<AskCard askPending={pending} onSubmit={onSubmit} onDecisionSubmitted={onDecisionSubmitted} />);
    const submit = screen.getByRole("button", { name: "chat.askCardSubmit" });
    expect(submit).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: `common.${choice}` }));
    fireEvent.click(submit);
    await waitFor(() => expect(axiosInstance.post).toHaveBeenLastCalledWith(
      "/api/core/knowledge-management/confirmations/kb-confirm/decision",
      { decision: choice === "yes" ? "approved" : "rejected", arguments_digest: "args-digest", snapshot: "version-1" },
    ));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(onDecisionSubmitted).toHaveBeenCalledWith(expect.objectContaining({ structured: expect.objectContaining({ ask_id: "kb-confirm" }) }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "chat.askCardSubmit" })).toBeNull());
  });

  it("retains the decision for retry when the business endpoint rejects the request", async () => {
    vi.mocked(axiosInstance.post).mockRejectedValueOnce(new Error("snapshot changed"));
    const onSubmit = vi.fn();
    render(<AskCard askPending={pending} onSubmit={onSubmit} />);
    fireEvent.click(screen.getByRole("button", { name: "common.yes" }));
    const submit = screen.getByRole("button", { name: "chat.askCardSubmit" });
    fireEvent.click(submit);
    await waitFor(() => expect(submit).toBeEnabled());
    expect(screen.getByRole("alert")).toHaveTextContent("chat.knowledgeConfirmationFailed");
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
