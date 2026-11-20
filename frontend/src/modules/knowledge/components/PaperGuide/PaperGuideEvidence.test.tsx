import { render, screen, fireEvent } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import PaperGuideEvidence from "./PaperGuideEvidence";
const evidence = {
  id: "e1",
  text: "Original words",
  source_block_id: "b1",
  source_type: "paragraph",
  locator: { kind: "text", page: 2 },
};
it("previews the source before navigating and displays one-based PDF pages", () => {
  const go = vi.fn();
  render(
    <PaperGuideEvidence
      value={{ evidence, source_current: true }}
      currentSourceHash="hash"
      revisionSourceHash="hash"
      language="en"
      onNavigate={go}
      onClose={() => {}}
    />,
  );
  expect(go).not.toHaveBeenCalled();
  expect(screen.getByText("Original words")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Open PDF page 3" }));
  expect(go).toHaveBeenCalledWith(evidence);
});
it("disables historical coordinates after a source hash change", () => {
  render(
    <PaperGuideEvidence
      value={{ evidence, source_current: true }}
      currentSourceHash="new"
      revisionSourceHash="old"
      language="en"
      onNavigate={vi.fn()}
      onClose={() => {}}
    />,
  );
  expect(
    screen.getByRole("button", { name: "Open PDF page 3" }),
  ).toBeDisabled();
});
it("disables old coordinates when the PDF hash is unchanged but positioning was updated",()=>{
 const go=vi.fn();
 render(<PaperGuideEvidence value={{evidence,source_current:false}} currentSourceHash="hash" revisionSourceHash="hash" language="en" onNavigate={go} onClose={()=>{}}/>);
 expect(screen.getByRole('button',{name:'Open PDF page 3'})).toBeDisabled();
 expect(screen.getByRole('status')).toHaveTextContent('positioning');
 fireEvent.click(screen.getByRole('button',{name:'Open PDF page 3'}));expect(go).not.toHaveBeenCalled();
});
