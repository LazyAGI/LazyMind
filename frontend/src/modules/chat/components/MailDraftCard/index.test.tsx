import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import MailDraftCard from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

vi.mock("@/modules/chat/store/taskCenter", () => ({
  useTaskCenterStore: (selector: (state: any) => unknown) =>
    selector({
      activeConversationId: "",
      artifactsByConversation: {},
    }),
}));

describe("MailDraftCard", () => {
  const editableDraft = { draft_id: "stable", revision: 1, to: ["team@example.com"],
    cc: [], subject: "Original", body: "Body", attachments: ["existing.pdf"], status: "draft" };

  it("preserves unsaved edits and attachment removal when an equivalent snapshot arrives", () => {
    const { rerender } = render(<MailDraftCard draft={editableDraft} onConfirm={vi.fn()} />);
    fireEvent.change(screen.getByRole("textbox", { name: "chat.mailDraft.subject" }), { target: { value: "My edit" } });
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.removeAttachment" }));
    rerender(<MailDraftCard draft={{ ...editableDraft, to: [...editableDraft.to], cc: [], attachments: ["existing.pdf"] }} onConfirm={vi.fn()} />);
    expect(screen.getByDisplayValue("My edit")).toBeVisible();
    expect(screen.queryByText("existing.pdf")).not.toBeInTheDocument();
  });

  it("prevents duplicate submissions while awaiting acceptance", async () => {
    let resolve!: (started: boolean) => void;
    const onConfirm = vi.fn(() => new Promise<boolean>((done) => { resolve = done; }));
    render(<MailDraftCard draft={editableDraft} onConfirm={onConfirm} />);
    const button = screen.getByRole("button", { name: "chat.mailDraft.confirmSend" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(onConfirm).toHaveBeenCalledOnce();
    resolve(false);
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" })).toBeEnabled());
    expect(screen.getByText("chat.mailDraft.submitFailed")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    expect(onConfirm).toHaveBeenCalledTimes(2);
    resolve(false);
  });

  it("keeps edits and allows retry when submitting throws", async () => {
    const onConfirm = vi.fn().mockRejectedValue(new Error('{"message":"internal"}'));
    render(<MailDraftCard draft={editableDraft} onConfirm={onConfirm} />);
    fireEvent.change(screen.getByRole("textbox", { name: "chat.mailDraft.subject" }), { target: { value: "Keep edit" } });
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    await screen.findByText("chat.mailDraft.submitFailed");
    expect(screen.getByDisplayValue("Keep edit")).toBeVisible();
    expect(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" })).toBeEnabled();
    expect(screen.queryByText('{"message":"internal"}')).not.toBeInTheDocument();
  });

  it.each([
    ["offline", "chat.mailDraft.submitOffline"],
    ["payload_too_large", "chat.mailDraft.submitTooLarge"],
  ] as const)("unlocks with edits and attachment kept when the request is refused (%s)", async (reason, expected) => {
    let refuse!: (reason: "offline" | "payload_too_large") => void;
    const onConfirm = vi.fn(async (_id: string, _revision: number, _patch: unknown, onRefused?: typeof refuse) => {
      refuse = onRefused!;
      return true;
    });
    const { container } = render(<MailDraftCard draft={editableDraft} onConfirm={onConfirm} />);
    fireEvent.change(screen.getByRole("textbox", { name: "chat.mailDraft.subject" }), { target: { value: "Keep edit" } });
    fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files: [new File(["data"], "upload.txt")] } });
    await screen.findByText("upload.txt");
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    await screen.findByText("chat.mailDraft.sending");
    act(() => refuse(reason));
    await screen.findByText(expected);
    expect(screen.queryByText("chat.mailDraft.sending")).not.toBeInTheDocument();
    expect(screen.getByDisplayValue("Keep edit")).toBeVisible();
    expect(screen.getByText("upload.txt")).toBeVisible();
    expect(onConfirm).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    expect(onConfirm).toHaveBeenCalledTimes(2);
    expect(onConfirm.mock.calls[1][2]).toEqual(expect.objectContaining({
      subject: "Keep edit", attachments: [{ filename: "upload.txt", content_base64: "ZGF0YQ==" }],
    }));
  });

  it("stops showing the waiting notice once a newer card carries the result", async () => {
    const onConfirm = vi.fn().mockResolvedValue(true);
    const { rerender } = render(<MailDraftCard draft={editableDraft} onConfirm={onConfirm} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    await screen.findByText("chat.mailDraft.sending");
    rerender(<MailDraftCard draft={editableDraft} disabled superseded onConfirm={onConfirm} />);
    expect(screen.queryByText("chat.mailDraft.sending")).not.toBeInTheDocument();
  });

  it("rejects an upload beyond the realtime-safe total size", async () => {
    const { container } = render(<MailDraftCard draft={editableDraft} onConfirm={vi.fn()} />);
    const big = new File(["x"], "big.bin");
    Object.defineProperty(big, "size", { value: 10 * 1024 * 1024 + 1 });
    fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files: [big] } });
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" })).toBeEnabled());
    expect(screen.queryByText("big.bin")).not.toBeInTheDocument();
  });

  it("allows exactly five uploaded attachments", async () => {
    const { container } = render(<MailDraftCard draft={editableDraft} onConfirm={vi.fn()} />);
    const files = Array.from({ length: 5 }, (_, index) => new File([String(index)], `${index}.txt`));
    fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files } });
    for (const file of files) {
      expect(await screen.findByText(file.name)).toBeVisible();
    }
    expect(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" })).toBeEnabled();
  });

  it.each([
    ['Failed to send the email: {"message":"SMTP rejected"}', "SMTP rejected"],
    [{ ok: false, value: { message: "Mailbox unavailable" } }, "Mailbox unavailable"],
  ])("renders a readable failure from %j", (last_error, expected) => {
    render(<MailDraftCard draft={{ ...editableDraft, status: "failed", last_error: last_error as any }} onConfirm={vi.fn()} />);
    expect(screen.getByRole("alert")).toHaveTextContent(expected);
    expect(screen.getByRole("alert").textContent).not.toMatch(/[{}]/);
  });

  it("localizes a server-rejected recipient instead of exposing the SMTP response", () => {
    render(
      <MailDraftCard
        draft={{
          ...editableDraft,
          status: "failed",
          error_code: "recipient_rejected",
          last_error: "Failed to send the email: (550, b'The recipient may contain a non-existent account')",
        }}
        onConfirm={vi.fn()}
      />,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("chat.mailDraft.recipientRejected");
    expect(screen.queryByText(/non-existent account|Failed to send/)).not.toBeInTheDocument();
  });

  it("keeps delivery-unknown dominant when a retry is rejected", () => {
    render(
      <MailDraftCard
        draft={{
          ...editableDraft,
          status: "delivery_unknown",
          delivery_unknown: true,
          error_code: "recipient_rejected",
          last_error: "Mail delivery is unknown. Failed to send: 550 b'user unknown'",
        }}
        onConfirm={vi.fn()}
      />,
    );

    expect(screen.getAllByRole("alert")).toHaveLength(1);
    expect(screen.getByRole("alert")).toHaveTextContent("chat.mailDraft.deliveryUnknown");
    expect(screen.getByRole("alert")).toHaveTextContent("chat.mailDraft.recipientRejected");
    expect(screen.queryByText(/user unknown|Failed to send/)).not.toBeInTheDocument();
  });

  it("does not permit sending while a sending snapshot is shown", () => {
    render(<MailDraftCard draft={{ ...editableDraft, status: "sending" }} onConfirm={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "chat.mailDraft.confirmSend" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("unlocks a failed receipt arriving before submission acknowledgement", async () => {
    let resolve!: (started: boolean) => void;
    const onConfirm = vi.fn(() => new Promise<boolean>((done) => { resolve = done; }));
    const { rerender } = render(<MailDraftCard draft={editableDraft} onConfirm={onConfirm} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    rerender(<MailDraftCard draft={{ ...editableDraft, status: "failed", last_error: "SMTP rejected" }} onConfirm={onConfirm} />);
    await act(async () => { resolve(true); });
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "chat.mailDraft.resend" })).toBeEnabled());
  });

  it("replaces edited fields with an authoritative receipt and preserves its attachment list", () => {
    const { rerender } = render(<MailDraftCard draft={editableDraft} onConfirm={vi.fn()} />);
    fireEvent.change(screen.getByRole("textbox", { name: "chat.mailDraft.subject" }), { target: { value: "New subject" } });
    rerender(<MailDraftCard draft={{ ...editableDraft, subject: "New subject", status: "sent", attachments: ["existing.pdf", "uploaded.txt"] }} onConfirm={vi.fn()} />);
    expect(screen.getByText("New subject")).toBeVisible();
    expect(screen.getByText("uploaded.txt")).toBeVisible();
    expect(screen.getByText("existing.pdf")).toBeVisible();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("disables send until the attachment has finished loading", async () => {
    const onConfirm = vi.fn();
    const { container } = render(<MailDraftCard draft={editableDraft} onConfirm={onConfirm} />);
    fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files: [new File(["data"], "upload.txt")] } });
    expect(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" })).toBeDisabled();
    await screen.findByText("upload.txt");
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    expect(onConfirm).toHaveBeenCalledWith("stable", 1, expect.objectContaining({ attachments: [{ filename: "upload.txt", content_base64: "ZGF0YQ==" }] }), expect.any(Function));
  });

  it("uses a safe fallback for malformed structured errors", () => {
    render(<MailDraftCard draft={{ ...editableDraft, status: "failed", last_error: 'SMTP error: {"debug": broken' }} onConfirm={vi.fn()} />);
    expect(screen.getByRole("alert")).toHaveTextContent("chat.mailDraft.sendFailed");
    expect(screen.getByRole("alert").textContent).not.toContain("debug");
  });

  it("reuploads an unsaved attachment instead of silently keeping an unresolved filename", async () => {
    const onConfirm = vi.fn();
    const { container } = render(<MailDraftCard draft={{ ...editableDraft, status: "failed",
      attachments: ["unsaved.txt"], attachment_paths: [], pending_attachment_names: ["unsaved.txt"],
    }} onConfirm={onConfirm} />);
    expect(screen.getByRole("button", { name: "chat.mailDraft.resend" })).toBeDisabled();
    fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files: [new File(["retry"], "unsaved.txt")] } });
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "chat.mailDraft.resend" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.resend" }));
    expect(onConfirm).toHaveBeenCalledWith("stable", 1, expect.objectContaining({ attachment_paths: [], attachments: [{ filename: "unsaved.txt", content_base64: "cmV0cnk=" }] }), expect.any(Function));
  });

  it("sends attachment references rather than ambiguous display names", () => {
    const onConfirm = vi.fn();
    render(<MailDraftCard draft={{ ...editableDraft, attachments: ["report.pdf"], attachment_paths: ["/workspace/out/report.pdf"] }} onConfirm={onConfirm} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    expect(onConfirm).toHaveBeenCalledWith("stable", 1, expect.objectContaining({ attachment_paths: ["/workspace/out/report.pdf"] }), expect.any(Function));
  });
  it.each([
    ["draft", "chat.mailDraft.confirmSend"],
    ["failed", "chat.mailDraft.resend"],
    ["delivery_unknown", "chat.mailDraft.resendAnyway"],
  ])("keeps %s sending behind an explicit confirmation", (status, action) => {
    const onConfirm = vi.fn();
    render(<MemoryRouter><MailDraftCard draft={{ draft_id: "draft-state", revision: 3,
      to: ["team@example.com"], subject: "Review", status }} onConfirm={onConfirm} /></MemoryRouter>);
    expect(onConfirm).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole("textbox", { name: "chat.mailDraft.subject" }), { target: { value: "Updated review" } });
    fireEvent.click(screen.getByRole("button", { name: action }));
    expect(onConfirm).toHaveBeenCalledWith("draft-state", 3, expect.objectContaining({ subject: "Updated review" }), expect.any(Function));
  });

  it("renders a sent message as read-only without a send action", () => {
    render(<MemoryRouter><MailDraftCard draft={{ draft_id: "sent", to: ["team@example.com"],
      subject: "Delivered", status: "sent" }} onConfirm={vi.fn()} /></MemoryRouter>);
    expect(screen.getByText("Delivered")).toBeVisible();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "chat.mailDraft.confirmSend" })).not.toBeInTheDocument();
  });
  it("lets the user add and remove attachments before confirm", async () => {
    const onConfirm = vi.fn();
    const { container } = render(
      <MemoryRouter>
        <MailDraftCard
          draft={{
            draft_id: "draft_1",
            revision: 1,
            to: ["a@b.com"],
            subject: "hi",
            body: "body",
            attachments: ["keep.txt"],
            status: "draft",
          }}
          onConfirm={onConfirm}
        />
      </MemoryRouter>,
    );

    expect(screen.getByText("keep.txt")).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "chat.mailDraft.removeAttachment" })[0]);
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["hello"], "new.txt", { type: "text/plain" });
    fireEvent.change(input, { target: { files: [file] } });
    expect(await screen.findByText("new.txt")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    await vi.waitFor(() => expect(onConfirm).toHaveBeenCalled());
    expect(onConfirm.mock.calls[0][2].attachments).toEqual([
      expect.objectContaining({ filename: "new.txt", content_base64: expect.any(String) }),
    ]);
    expect(onConfirm.mock.calls[0][2].attachment_paths).toEqual([]);
  });

  it("can attach a conversation upload without using the chat file picker", async () => {
    const onConfirm = vi.fn();
    render(
      <MemoryRouter>
        <MailDraftCard
          draft={{
            draft_id: "draft_3",
            revision: 1,
            to: ["a@b.com"],
            subject: "hi",
            body: "body",
            attachments: [],
            status: "draft",
          }}
          conversationFiles={[{ name: "report.pdf" }]}
          onConfirm={onConfirm}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: /report.pdf/ }));
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    await vi.waitFor(() => expect(onConfirm).toHaveBeenCalled());
    expect(onConfirm.mock.calls[0][2].attachment_paths).toEqual(["report.pdf"]);
    expect(onConfirm.mock.calls[0][2].attachments).toEqual([]);
  });

  it("accepts string recipients without crashing", () => {
    render(
      <MemoryRouter>
        <MailDraftCard
          draft={{
            draft_id: "draft_str",
            revision: 1,
            to: "a@b.com, c@d.com" as unknown as string[],
            cc: "e@f.com" as unknown as string[],
            subject: "hi",
            body: "body",
            attachments: "notes.txt" as unknown as string[],
            status: "draft",
          }}
          onConfirm={vi.fn()}
        />
      </MemoryRouter>,
    );

    expect(screen.getByText("a@b.com", { selector: ".ant-select-selection-item-content" })).toBeVisible();
    expect(screen.getByText("c@d.com", { selector: ".ant-select-selection-item-content" })).toBeVisible();
    expect(screen.getByText("e@f.com", { selector: ".ant-select-selection-item-content" })).toBeVisible();
    expect(screen.getByText("notes.txt")).toBeInTheDocument();
  });

  it("creates removable recipient tags with Enter", () => {
    const onConfirm = vi.fn();
    const { container } = render(
      <MemoryRouter>
        <MailDraftCard
          draft={{
            draft_id: "draft_to",
            revision: 1,
            to: [],
            cc: [],
            subject: "hi",
            body: "body",
            status: "draft",
          }}
          onConfirm={onConfirm}
        />
      </MemoryRouter>,
    );

    const recipient = screen.getByRole("combobox", { name: "chat.mailDraft.to" });
    fireEvent.change(recipient, { target: { value: "firmach@163.com" } });
    fireEvent.keyDown(recipient, { key: "Enter", code: "Enter" });
    const tag = screen.getByText("firmach@163.com", { selector: ".ant-select-selection-item-content" });
    expect(tag).toBeVisible();
    expect(container.querySelector(".mail-draft-address")).toBeTruthy();

    fireEvent.click(tag.closest(".ant-select-selection-item")!.querySelector(".ant-select-selection-item-remove")!);
    expect(screen.queryByText("firmach@163.com", { selector: ".ant-select-selection-item-content" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" })).toBeDisabled();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("turns semicolon-separated recipients into tags", () => {
    render(<MailDraftCard draft={{ ...editableDraft, to: [] }} onConfirm={vi.fn()} />);
    const recipient = screen.getByRole("combobox", { name: "chat.mailDraft.to" });
    fireEvent.change(recipient, { target: { value: "first@example.com;second@example.org;" } });

    expect(screen.getByText("first@example.com", { selector: ".ant-select-selection-item-content" })).toBeVisible();
    expect(screen.getByText("second@example.org", { selector: ".ant-select-selection-item-content" })).toBeVisible();
  });

  it("shows only the localized frontend error when an empty recipient also has a backend failure", () => {
    render(
      <MailDraftCard
        draft={{
          ...editableDraft,
          to: [],
          status: "failed",
          last_error: "No recipients. Add at least one address in To, then confirm again.",
        }}
        onConfirm={vi.fn()}
      />,
    );

    expect(screen.getAllByRole("alert")).toHaveLength(1);
    expect(screen.getByRole("alert")).toHaveTextContent("chat.mailDraft.recipientRequired");
    expect(screen.queryByText(/No recipients/)).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole("combobox", { name: "chat.mailDraft.to" }), {
      target: { value: "fixed@example.com" },
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("validates To and Cc addresses before submitting", () => {
    const onConfirm = vi.fn();
    render(<MailDraftCard draft={editableDraft} onConfirm={onConfirm} />);

    fireEvent.change(screen.getByRole("combobox", { name: "chat.mailDraft.to" }), {
      target: { value: "valid@example.com, not-an-email" },
    });
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("chat.mailDraft.recipientInvalid");

    fireEvent.change(screen.getByRole("combobox", { name: "chat.mailDraft.to" }), {
      target: { value: "valid@example.com" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "chat.mailDraft.cc" }), {
      target: { value: "broken@" },
    });
    expect(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" })).toBeDisabled();
    expect(screen.getAllByRole("alert")).toHaveLength(1);
  });

  it("submits multiple valid recipients separated by commas or semicolons", () => {
    const onConfirm = vi.fn();
    render(<MailDraftCard draft={{ ...editableDraft, to: [] }} onConfirm={onConfirm} />);

    fireEvent.change(screen.getByRole("combobox", { name: "chat.mailDraft.to" }), {
      target: { value: "first@example.com; second@example.org" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "chat.mailDraft.cc" }), {
      target: { value: "copy@example.net" },
    });
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));

    expect(onConfirm).toHaveBeenCalledWith(
      "stable",
      1,
      expect.objectContaining({
        to: "first@example.com, second@example.org",
        cc: "copy@example.net",
      }),
      expect.any(Function),
    );
  });
});
