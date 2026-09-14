import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import StreamRecoveryBanner from "./StreamRecoveryBanner";
import RunStatusCard from "../../RunStatusCard";

vi.mock("react-i18next", async () => {
  const { default: messages } = await import("@/i18n/locales/zh-CN");
  return { useTranslation: () => ({ t: (key: string) => key.split(".").reduce<any>((value, part) => value?.[part], messages) || key }) };
});

describe("persistent Chinese connection feedback", () => {
  it("keeps a disconnected banner visible until connection state changes and offers reconnect", () => {
    const onReconnect = vi.fn();
    const recovery = { status: "failed" as const, conversationId: "local-test", attempt: 3, maxAttempts: 3 };
    const { rerender } = render(<StreamRecoveryBanner recovery={recovery} onReconnect={onReconnect} />);
    expect(screen.getByRole("alert")).toHaveTextContent("无法恢复与 LazyMind 服务的连接，当前内容已保留。");
    fireEvent.click(screen.getByRole("button", { name: "重新连接" }));
    expect(onReconnect).toHaveBeenCalledOnce();
    expect(screen.getByRole("alert")).toBeInTheDocument();
    rerender(<StreamRecoveryBanner recovery={{ ...recovery, status: "idle" }} onReconnect={onReconnect} />);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("renders model transport failures as an inline Chinese status", () => {
    render(<RunStatusCard terminal={{ status: "failed", reason: "model_failure", code: "transport_error", partial_output: true }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("无法稳定连接到模型服务。");
    expect(screen.getByRole("alert")).not.toHaveTextContent("transport_error");
  });
});
