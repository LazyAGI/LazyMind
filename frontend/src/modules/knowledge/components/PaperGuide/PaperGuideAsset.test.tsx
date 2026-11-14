import { ConfigProvider } from "antd";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import PaperGuideAsset from "./PaperGuideAsset";
import type { PaperGuideEvidence } from "@/api/generated/core-client";
it("restores the enlarged figure opener after Escape closes the guide modal", async () => {
  render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <PaperGuideAsset
        language="en"
        evidence={
          {
            id: "figure",
            text: "A table",
            source_block_id: "table",
            source_type: "table",
            locator: { kind: "bbox", page: 0 },
          } as PaperGuideEvidence
        }
        preview={async () => ({
          url: "data:image/png;base64,AA==",
          width: 100,
          height: 100,
        })}
      />
    </ConfigProvider>,
  );
  const opener = await screen.findByRole("button", {
    name: "Enlarge original figure",
  });
  opener.focus();
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog");
  const close = screen.getByRole("button", { name: "Close" });
  close.focus();
  fireEvent.keyDown(dialog, { key: "Escape", code: "Escape", keyCode: 27 });
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  await waitFor(() => expect(opener).toHaveFocus());
});
