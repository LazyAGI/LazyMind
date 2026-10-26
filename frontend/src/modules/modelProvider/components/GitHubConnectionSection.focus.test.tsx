import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";
import GitHubConnectionSection from "./GitHubConnectionSection";

describe("GitHub authorization focus", () => {
  it.each(["CANCELLED", "COMPLETED", "FAILED"])("returns focus after %s and removes the device code", async status => {
    let finish: (status: string) => void = () => {};
    function Harness() {
      const [sessionStatus, setSessionStatus] = useState("");
      const [githubDialogOpen, setGitHubDialogOpen] = useState(false);
      finish = setSessionStatus;
      const labels: Record<string, string> = {
        connect: "Connect GitHub", cancel: "Cancel authorization", authorizationTitle: "Connect GitHub",
        start: "Start connecting",
      };
      const vm = {
        t: (key: string) => labels[key.split(".").pop()!] || key,
        githubDialogOpen, setGitHubDialogOpen, githubDialogMode: "quick",
        handleOpenGitHubSetup: () => setGitHubDialogOpen(true),
        github: {
          accounts: [], updating: [], notice: "", retryCreate: false, browserBlocked: false,
          active: sessionStatus === "WAITING_USER",
          session: sessionStatus ? { session_id: "fixture-session", status: sessionStatus,
            ...(sessionStatus === "WAITING_USER" ? { user_code: "TEST-CODE" } : {}) } : null,
          start: () => {
            // Native Chromium can drop focus when the initiating button becomes disabled.
            (document.activeElement as HTMLElement | null)?.blur();
            setSessionStatus("WAITING_USER");
          },
          cancel: () => setSessionStatus("CANCELLED"),
        },
      } as unknown as CloudDocumentProvidersVm;
      return <ConfigProvider theme={{ token: { motion: false } }}>
        <GitHubConnectionSection vm={vm} logo={<span />} />
      </ConfigProvider>;
    }

    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Connect GitHub" });
    trigger.focus();
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("button", { name: "Start connecting" }));
    await waitFor(() => expect(screen.getByText("TEST-CODE")).toBeVisible());
    const cancel = screen.getByRole("button", { name: "Cancel authorization" });
    cancel.focus();
    expect(cancel).toHaveFocus();
    await act(async () => {
      if (status === "CANCELLED") fireEvent.click(cancel);
      else finish(status);
    });
    expect(screen.queryByText("TEST-CODE")).not.toBeInTheDocument();
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
