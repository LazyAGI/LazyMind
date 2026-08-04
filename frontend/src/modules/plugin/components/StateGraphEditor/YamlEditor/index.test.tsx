import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import YamlEditor from "./index";

vi.mock("./Editor", () => ({
  default: ({ value }: { value: string }) => <div data-testid="mock-editor">{value}</div>,
}));

describe("YamlEditor", () => {
  it("lazily renders the underlying Editor with the given props", async () => {
    render(<YamlEditor value="a: 1" onChange={vi.fn()} errors={[]} />);

    await waitFor(() => expect(screen.getByTestId("mock-editor")).toBeInTheDocument());
    expect(screen.getByTestId("mock-editor")).toHaveTextContent("a: 1");
  });
});
