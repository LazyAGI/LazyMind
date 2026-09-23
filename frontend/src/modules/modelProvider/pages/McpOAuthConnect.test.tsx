import { StrictMode } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
const { authorize } = vi.hoisted(() => ({ authorize: vi.fn() }));
vi.mock("@/modules/memory/toolApi", () => ({ authorizeMcpServer: authorize }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
import McpOAuthConnect from "./McpOAuthConnect";

beforeEach(() => {
  authorize.mockReset().mockResolvedValue(undefined);
  window.history.replaceState(null, "", "/oauth/mcp/connect?server=personal-notion&conversation=original");
});
it("starts OAuth once in the new tab and carries the original conversation", async () => {
  render(<StrictMode><McpOAuthConnect /></StrictMode>);
  await waitFor(() => expect(authorize).toHaveBeenCalledWith("personal-notion", "original"));
  expect(authorize).toHaveBeenCalledTimes(1);
});
it("allows retry after an unavailable authorization service", async () => {
  authorize.mockRejectedValueOnce(new Error("unavailable"));
  render(<McpOAuthConnect />);
  fireEvent.click(await screen.findByText("toolConfiguration.retryConnection"));
  await screen.findByText("toolConfiguration.connecting");
  expect(authorize).toHaveBeenCalledTimes(2);
});
it("rejects a missing server instead of authorizing an arbitrary connection", async () => {
  window.history.replaceState(null, "", "/oauth/mcp/connect?conversation=c");
  render(<McpOAuthConnect />);
  await screen.findByText("admin.memoryMcpOAuthError");
  expect(authorize).not.toHaveBeenCalled();
});
