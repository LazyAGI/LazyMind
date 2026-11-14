import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import PaperGuideGenerationDetails from "./PaperGuideGenerationDetails";
import { revision } from "./fixtures";

afterEach(() => vi.restoreAllMocks());
it("keeps model selection and regeneration reachable in the remaining reader height", async () => {
  let bottom = 210;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      return {
        top: 60,
        bottom: this.classList.contains("paper-guide-toolbar") ? bottom : 400,
        height: bottom - 60,
      } as DOMRect;
    },
  );
  const select = vi.fn(),
    regenerate = vi.fn();
  const view = render(
    <div className="paper-reading-workspace">
      <header className="paper-guide-toolbar">
        <PaperGuideGenerationDetails
          zh={false}
          model=""
          setModel={select}
          models={{
            options: [
              {
                id: "own:long",
                label:
                  "A deliberately long provider and model label for the native select",
              },
            ],
          }}
          busy={false}
          active={false}
          canGenerate
          onRegenerate={regenerate}
        />
      </header>
    </div>,
  );
  const details = view.container.querySelector("details")!;
  details.open = true;
  fireEvent(details, new Event("toggle"));
  const panel = details.querySelector("div")!;
  await waitFor(() =>
    expect(parseFloat(panel.style.maxHeight)).toBeLessThanOrEqual(182),
  );
  expect(parseFloat(panel.style.maxHeight)).toBeGreaterThan(0);
  // A wrapped toolbar at 200% zoom leaves too little room below it: overlay
  // from the toolbar's top, with internal scrolling, instead of clipping actions.
  bottom = 370;
  fireEvent(window, new Event("resize"));
  await waitFor(() => expect(panel.style.top).toBe("0px"));
  expect(parseFloat(panel.style.maxHeight)).toBeLessThanOrEqual(332);
  fireEvent.change(screen.getByRole("combobox"), {
    target: { value: "own:long" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
  expect(select).toHaveBeenCalledWith("own:long");
  expect(regenerate).toHaveBeenCalledTimes(1);
  fireEvent.keyDown(screen.getByRole("combobox"), { key: "Escape" });
  expect(details.open).toBe(false);
  expect(details.querySelector("summary")).toHaveFocus();
});

it("separates translated coverage warnings from coverage values and preserves unknown parser information", () => {
  const r = revision();
  r.coverage.warnings = [
    "Formula transcription is unavailable; only original text and page are readable.",
    "Custom parser diagnostic 42",
  ];
  render(
    <PaperGuideGenerationDetails
      zh
      model=""
      setModel={() => {}}
      revision={r}
      busy={false}
      active={false}
      canGenerate
      onRegenerate={() => {}}
    />,
  );
  const warnings = screen.getAllByRole("listitem", { hidden: true });
  expect(warnings).toHaveLength(2);
  expect(warnings[0]).toHaveTextContent("公式转写不可用");
  expect(warnings[1]).toHaveTextContent(
    "解析提示：Custom parser diagnostic 42",
  );
});
