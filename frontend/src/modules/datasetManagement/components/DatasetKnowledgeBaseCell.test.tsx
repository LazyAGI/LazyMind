import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import DatasetKnowledgeBaseCell from "./DatasetKnowledgeBaseCell";

describe("DatasetKnowledgeBaseCell", () => {
  it("renders a dash when there are no knowledge bases", () => {
    render(<DatasetKnowledgeBaseCell knowledgeBases={[]} />);
    expect(screen.getByText("-")).toBeInTheDocument();
  });

  it("renders a dash when knowledgeBases is undefined", () => {
    render(<DatasetKnowledgeBaseCell />);
    expect(screen.getByText("-")).toBeInTheDocument();
  });

  it("renders the first knowledge base name without a +N tag when there is only one", () => {
    render(
      <DatasetKnowledgeBaseCell knowledgeBases={[{ id: "kb1", name: "Docs KB" }]} />,
    );
    expect(screen.getByText("Docs KB")).toBeInTheDocument();
    expect(screen.queryByText(/^\+\d+$/)).not.toBeInTheDocument();
  });

  it("falls back to the id when the name is empty", () => {
    render(<DatasetKnowledgeBaseCell knowledgeBases={[{ id: "kb1", name: "" }]} />);
    expect(screen.getByText("kb1")).toBeInTheDocument();
  });

  it("shows a +N overflow tag when there are multiple knowledge bases", () => {
    render(
      <DatasetKnowledgeBaseCell
        knowledgeBases={[
          { id: "kb1", name: "Docs KB" },
          { id: "kb2", name: "Support KB" },
          { id: "kb3", name: "Legal KB" },
        ]}
      />,
    );
    expect(screen.getByText("Docs KB")).toBeInTheDocument();
    expect(screen.getByText("+2")).toBeInTheDocument();
  });

  it("filters out entries with neither id nor name", () => {
    render(
      <DatasetKnowledgeBaseCell
        knowledgeBases={[{ id: "", name: "" }, { id: "kb1", name: "Docs KB" }]}
      />,
    );
    expect(screen.getByText("Docs KB")).toBeInTheDocument();
    expect(screen.queryByText(/^\+\d+$/)).not.toBeInTheDocument();
  });
});
