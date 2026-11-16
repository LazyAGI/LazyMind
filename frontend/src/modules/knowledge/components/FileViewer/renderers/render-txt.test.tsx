import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import RenderTxt from "./render-txt";
vi.mock("@/i18n", () => ({ default: { t: (key: string) => key } }));
afterEach(cleanup);
const payload = '<img src=x onerror="alert(1)"><script>alert(1)</script>';
it.each([null, payload])("renders untrusted text literally with selection %s", (content) => {
  const text = `before ${payload} middle ${payload} after`;
  const { container } = render(<RenderTxt fileData={new TextEncoder().encode(text).buffer} content={content} />);
  expect(container.querySelector("pre")?.textContent).toBe(text);
  expect(container.querySelector("img,script")).toBeNull();
  expect(container.querySelectorAll(".txt-keyword")).toHaveLength(content ? 2 : 0);
});
it("updates text and removes obsolete highlights when the file changes", () => {
  const {container, rerender} = render(<RenderTxt fileData={new TextEncoder().encode("hello hello").buffer} content="hello" />);
  expect(container.querySelectorAll(".txt-keyword")).toHaveLength(2);
  rerender(<RenderTxt fileData={new TextEncoder().encode("next <b>file</b>").buffer} content={null} />);
  expect(container.querySelector("pre")?.textContent).toBe("next <b>file</b>");
  expect(container.querySelector("b,.txt-keyword")).toBeNull();
});
