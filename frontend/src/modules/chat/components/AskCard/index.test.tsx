import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import AskCard, { type AskPending } from "./index";

vi.mock("react-i18next", () => ({
  initReactI18next: { type: "3rdParty", init: vi.fn() },
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

vi.mock("@/modules/chat/utils/chunkUpload", () => ({
  uploadFileInChunks: vi.fn(async () => "/uploads/template.pptx"),
}));

describe("AskCard read-only history", () => {
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

  it("requires and submits a conditional attachment with the answer", async () => {
    const onSubmit = vi.fn();
    const { container } = render(<AskCard askPending={{
      ask_id: "ppt-template",
      questions: [{
        text: "Use a template?",
        type: "single",
        choices: ["No template", "Upload template"],
        attachment: {
          when_choice: "Upload template",
          material_id: "template_pptx",
          accept: [".pptx"],
          max_files: 1,
          required: true,
        },
      }],
    }} onSubmit={onSubmit} />);

    fireEvent.click(screen.getByRole("radio", { name: "Upload template" }));
    expect(screen.getByRole("button", { name: /chat\.askCardUploadFile/ })).toBeInTheDocument();
    const submit = screen.getByRole("button", { name: "chat.askCardSubmit" });
    expect(submit).toBeDisabled();
    fireEvent.change(container.querySelector('input[type="file"]')!, {
      target: { files: [new File(["pptx"], "brand.pptx", { type: "application/vnd.openxmlformats-officedocument.presentationml.presentation" })] },
    });
    await waitFor(() => expect(submit).toBeEnabled());
    fireEvent.click(submit);
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0].files).toEqual([
      expect.objectContaining({ name: "brand.pptx", uri: "/uploads/template.pptx" }),
    ]);
  });

  it("submits more than three academic papers without a count limit", async () => {
    const onSubmit = vi.fn();
    const { container } = render(<AskCard askPending={{
      ask_id: "papers",
      questions: [{ text: "Research materials", type: "single", choices: ["Upload papers"],
        attachment: { when_choice: "Upload papers", material_id: "uploaded_materials",
          accept: [".pdf"], max_files: -1, required: true } }],
    }} onSubmit={onSubmit} />);
    fireEvent.click(screen.getByRole("radio", { name: "Upload papers" }));
    const submit = screen.getByRole("button", { name: "chat.askCardSubmit" });
    expect(submit).toBeDisabled();
    fireEvent.change(container.querySelector('input[type="file"]')!, {
      target: { files: [1, 2, 3, 4, 5].map((i) => new File([String(i)], `${i}.pdf`, { type: "application/pdf" })) },
    });
    await waitFor(() => expect(submit).toBeEnabled());
    fireEvent.click(submit);
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0].files.map((file: File) => file.name)).toEqual(["1.pdf", "2.pdf", "3.pdf", "4.pdf", "5.pdf"]);
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
