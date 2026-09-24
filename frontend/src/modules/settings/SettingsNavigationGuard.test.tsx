import { useState } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, Link, RouterProvider } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { SettingsNavigationGuard, useSettingsDraft } from "./SettingsNavigationGuard";

vi.mock("react-i18next", async (original) => ({
  ...await original<typeof import("react-i18next")>(),
  useTranslation: () => ({ t: (key: string) => key }),
}));

function Editor({ save }: { save: () => Promise<boolean> }) {
  const [text, setText] = useState("");
  useSettingsDraft({ dirty: text !== "", save, discard: () => setText("") });
  return <>
    <input aria-label="draft" value={text} onChange={(event) => setText(event.target.value)} />
    <Link to="/settings?section=knowledge&tool=web-search">Search settings</Link>
    <Link to="/chat">Chat</Link>
  </>;
}

function setup(save = vi.fn().mockResolvedValue(true)) {
  const router = createMemoryRouter([
    { path: "/settings", element: <SettingsNavigationGuard><Editor save={save} /></SettingsNavigationGuard> },
    { path: "/chat", element: <p>Chat page</p> },
  ], { initialEntries: ["/chat", "/settings?section=models&view=providers"], initialIndex: 1 });
  render(<RouterProvider router={router} />);
  return router;
}

describe("settings unsaved navigation", () => {
  it("allows clean navigation and restores the complete settings URL on back", async () => {
    const router = setup();
    fireEvent.click(screen.getByText("Search settings"));
    await waitFor(() => expect(router.state.location.search).toBe("?section=knowledge&tool=web-search"));
    await act(async () => { await router.navigate(-1); });
    expect(router.state.location.search).toBe("?section=models&view=providers");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("blocks a query-only section change and keeps the draft when canceled", async () => {
    const router = setup();
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    fireEvent.click(screen.getByText("Search settings"));
    fireEvent.click(await screen.findByText("settingsPage.unsaved.stay"));
    expect(router.state.location.search).toBe("?section=models&view=providers");
    expect(screen.getByLabelText("draft")).toHaveValue("unsaved");
  });

  it("blocks browser back and proceeds only after explicit discard", async () => {
    const router = setup();
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    await act(async () => { await router.navigate(-1); });
    expect(router.state.location.pathname).toBe("/settings");
    fireEvent.click(await screen.findByText("settingsPage.unsaved.discard"));
    expect(await screen.findByText("Chat page")).toBeInTheDocument();
  });

  it("keeps the form on save failure and retries before leaving", async () => {
    const save = vi.fn().mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    const router = setup(save);
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    fireEvent.click(screen.getByText("Chat"));
    fireEvent.click(await screen.findByText("settingsPage.unsaved.save"));
    expect(await screen.findByRole("alert")).toHaveTextContent("settingsPage.unsaved.saveFailed");
    expect(router.state.location.pathname).toBe("/settings");
    expect(screen.getByLabelText("draft")).toHaveValue("unsaved");
    fireEvent.click(screen.getByText("settingsPage.unsaved.save"));
    expect(await screen.findByText("Chat page")).toBeInTheDocument();
    expect(save).toHaveBeenCalledTimes(2);
  });

  it("warns on document reload only while there are unsaved changes", () => {
    setup();
    const clean = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(clean);
    expect(clean.defaultPrevented).toBe(false);
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    const dirty = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(dirty);
    expect(dirty.defaultPrevented).toBe(true);
  });
});
