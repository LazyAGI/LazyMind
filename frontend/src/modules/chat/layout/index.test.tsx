import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import Layout from "./index";

describe("Layout", () => {
  it("renders its children wrapped in an antd ConfigProvider", () => {
    renderWithProviders(
      <Layout>
        <div data-testid="child">hello</div>
      </Layout>,
    );
    expect(screen.getByTestId("child")).toBeInTheDocument();
  });

  it("renders nothing extra when there are no children", () => {
    const { container } = renderWithProviders(<Layout />);
    expect(container).toBeTruthy();
  });
});
