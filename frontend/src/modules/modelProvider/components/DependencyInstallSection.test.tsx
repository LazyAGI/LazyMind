import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import DependencyInstallSection from "./DependencyInstallSection";
import {
  checkFFmpegDependency,
  getFFmpegDependencyStatus,
  installFFmpegDependency,
  updateFFmpegDependency,
} from "../api/systemDependencies";
import { isDesktopRuntime, isLocalRuntime } from "@/runtime/mode";

vi.mock("../api/systemDependencies", () => ({
  getFFmpegDependencyStatus: vi.fn(),
  updateFFmpegDependency: vi.fn(),
  checkFFmpegDependency: vi.fn(),
  installFFmpegDependency: vi.fn(),
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: () => "",
}));

vi.mock("@/runtime/mode", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/runtime/mode")>();
  return {
    ...actual,
    isLocalRuntime: vi.fn(),
    isDesktopRuntime: vi.fn(),
  };
});

const mockStatus = {
  installed: false,
  source: "auto",
  affectedFeatures: [],
  runtimeLocal: true,
  installSupported: true,
};

describe("DependencyInstallSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (isLocalRuntime as any).mockReturnValue(true);
    (isDesktopRuntime as any).mockReturnValue(false);
    (getFFmpegDependencyStatus as any).mockResolvedValue({ ...mockStatus });
  });

  it("renders nothing when not running in a local or desktop runtime", () => {
    (isLocalRuntime as any).mockReturnValue(false);
    const { container } = renderWithProviders(<DependencyInstallSection />);
    expect(container).toBeEmptyDOMElement();
  });

  it("loads and renders the ffmpeg dependency card when in local runtime", async () => {
    renderWithProviders(<DependencyInstallSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.dependencyFfmpegTitle"),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByText("modelProvider.external.status.missing"),
    ).toBeInTheDocument();
  });

  it("shows an error alert with retry when loading the status fails", async () => {
    (getFFmpegDependencyStatus as any).mockRejectedValue(new Error("boom"));
    renderWithProviders(<DependencyInstallSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.dependencyLoadFailed"),
      ).toBeInTheDocument();
    });
  });

  it("opens the config modal and installs the bundled dependency", async () => {
    (installFFmpegDependency as any).mockResolvedValue({
      ...mockStatus,
      installed: true,
    });
    renderWithProviders(<DependencyInstallSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.dependencyFfmpegTitle"),
      ).toBeInTheDocument();
    });
    fireEvent.click(
      screen.getByText("modelProvider.external.dependencyFfmpegTitle"),
    );
    const installButton = await screen.findByText(
      "modelProvider.external.dependencyInstallAction",
    );
    fireEvent.click(installButton);
    await waitFor(() => {
      expect(installFFmpegDependency).toHaveBeenCalledTimes(1);
    });
  });

  it("saves a custom path from the config modal", async () => {
    (updateFFmpegDependency as any).mockResolvedValue({
      ...mockStatus,
      installed: true,
      source: "custom",
      customPath: "/usr/local/bin/ffmpeg",
    });
    renderWithProviders(<DependencyInstallSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.dependencyFfmpegTitle"),
      ).toBeInTheDocument();
    });
    fireEvent.click(
      screen.getByText("modelProvider.external.dependencyFfmpegTitle"),
    );
    const input = await screen.findByPlaceholderText(
      "modelProvider.external.dependencyCustomPathPlaceholder",
    );
    fireEvent.change(input, { target: { value: "/usr/local/bin/ffmpeg" } });
    fireEvent.click(
      screen.getByText("modelProvider.external.dependencySavePathAction"),
    );
    await waitFor(() => {
      expect(updateFFmpegDependency).toHaveBeenCalledWith({
        source: "custom",
        customPath: "/usr/local/bin/ffmpeg",
      });
    });
  });

  it("filters the visible card based on the search input", async () => {
    renderWithProviders(<DependencyInstallSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.dependencyFfmpegTitle"),
      ).toBeInTheDocument();
    });
    const searchInput = screen.getByPlaceholderText(
      "modelProvider.external.searchPlaceholder",
    );
    fireEvent.change(searchInput, { target: { value: "no-match-keyword" } });
    await waitFor(() => {
      expect(
        screen.queryByText("modelProvider.external.dependencyFfmpegTitle"),
      ).not.toBeInTheDocument();
    });
    expect(
      screen.getByText("modelProvider.external.noMatchedServices"),
    ).toBeInTheDocument();
  });
});
