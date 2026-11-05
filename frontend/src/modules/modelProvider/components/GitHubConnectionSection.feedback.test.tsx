import { render, screen, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { expect, it } from "vitest";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";
import GitHubConnectionSection from "./GitHubConnectionSection";

it.each(["network", "cancelFailed"])("exposes %s feedback within the active authorization dialog", async notice => {
  const vm = {
    t: (key: string) => key,
    github: {
      accounts: [], updating: [], notice, retryCreate: false, browserBlocked: false, active: true,
      session: { session_id: "fixture-session", status: "WAITING_USER", user_code: "TEST-CODE" },
    },
  } as unknown as CloudDocumentProvidersVm;
  render(<ConfigProvider theme={{ token: { motion: false } }}>
    <GitHubConnectionSection vm={vm} logo={<span />} />
  </ConfigProvider>);
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByRole("alert")).toHaveTextContent(`modelProvider.cloudDocuments.githubCli.${notice}`);
});
