import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import MailMailboxCard from "./MailMailboxCard";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: { mailbox?: string }) =>
      options?.mailbox ? `${key}:${options.mailbox}` : key,
  }),
}));

describe("MailMailboxCard", () => {
  it("submits a mailbox choice once and permits retry if the request was not started", async () => {
    let resolve!: (started: boolean) => void;
    const onConfirm = vi.fn(() => new Promise<boolean>((done) => { resolve = done; }));
    render(<MailMailboxCard draft={{ draft_id: "one", mailboxes: [{ email: "sender@example.com" }] }} onConfirm={onConfirm} />);
    fireEvent.click(screen.getByRole("button", { name: "sender@example.com" }));
    const confirm = screen.getByRole("button", { name: "chat.mailMailbox.confirm" });
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledOnce();
    resolve(false);
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "chat.mailMailbox.confirm" })).toBeEnabled());
  });
  it("only confirms a mailbox from the connected list", () => {
    const onConfirm = vi.fn();
    render(
      <MailMailboxCard
        draft={{
          draft_id: "draft_1",
          status: "needs_mailbox",
          mailboxes: [
            { email: "a@qq.com", provider: "qqmail" },
            { email: "b@163.com", provider: "netease163" },
            { email: "a@qq.com", provider: "qqmail" },
          ],
        }}
        onConfirm={onConfirm}
      />,
    );

    expect(screen.getAllByRole("button", { name: /a@qq.com/ })).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: /b@163.com/ }));
    fireEvent.click(screen.getByRole("button", { name: "chat.mailMailbox.confirm" }));
    expect(onConfirm).toHaveBeenCalledWith("b@163.com", "draft_1");
  });
});
