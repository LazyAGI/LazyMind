import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import DatasetTemplateDownload from "./DatasetTemplateDownload";

const jsonToSheetMock = vi.hoisted(() => vi.fn(() => ({})));
const sheetToCsvMock = vi.hoisted(() => vi.fn(() => "csv-content"));
const bookNewMock = vi.hoisted(() => vi.fn(() => ({})));
const bookAppendSheetMock = vi.hoisted(() => vi.fn());
const xlsxWriteMock = vi.hoisted(() => vi.fn(() => new Uint8Array([1, 2, 3])));

vi.mock("xlsx", () => ({
  utils: {
    json_to_sheet: jsonToSheetMock,
    sheet_to_csv: sheetToCsvMock,
    book_new: bookNewMock,
    book_append_sheet: bookAppendSheetMock,
  },
  write: xlsxWriteMock,
}));

describe("DatasetTemplateDownload", () => {
  beforeEach(() => {
    jsonToSheetMock.mockClear();
    sheetToCsvMock.mockClear();
    bookNewMock.mockClear();
    bookAppendSheetMock.mockClear();
    xlsxWriteMock.mockClear();
    vi.stubGlobal("URL", {
      ...URL,
      createObjectURL: vi.fn(() => "blob:mock"),
      revokeObjectURL: vi.fn(),
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders the three download buttons", () => {
    renderWithProviders(<DatasetTemplateDownload />);
    expect(screen.getByText("datasetManagement.template.downloadExcel")).toBeInTheDocument();
    expect(screen.getByText("datasetManagement.template.downloadCsv")).toBeInTheDocument();
    expect(screen.getByText("datasetManagement.template.downloadJson")).toBeInTheDocument();
  });

  it("triggers an xlsx download and shows a success message when the excel button is clicked", async () => {
    const { message } = await import("antd");
    const successSpy = vi.spyOn(message, "success");

    renderWithProviders(<DatasetTemplateDownload />);
    fireEvent.click(screen.getByText("datasetManagement.template.downloadExcel"));

    expect(xlsxWriteMock).toHaveBeenCalled();
    expect(successSpy).toHaveBeenCalledWith("datasetManagement.template.downloaded");
  });

  it("triggers a csv download using sheet_to_csv when the csv button is clicked", async () => {
    const { message } = await import("antd");
    const successSpy = vi.spyOn(message, "success");

    renderWithProviders(<DatasetTemplateDownload />);
    fireEvent.click(screen.getByText("datasetManagement.template.downloadCsv"));

    expect(sheetToCsvMock).toHaveBeenCalled();
    expect(successSpy).toHaveBeenCalledWith("datasetManagement.template.downloaded");
  });

  it("triggers a json download when the json button is clicked", async () => {
    const { message } = await import("antd");
    const successSpy = vi.spyOn(message, "success");

    renderWithProviders(<DatasetTemplateDownload />);
    fireEvent.click(screen.getByText("datasetManagement.template.downloadJson"));

    expect(successSpy).toHaveBeenCalledWith("datasetManagement.template.downloaded");
  });

  it("shows an error message when generating the template throws", async () => {
    jsonToSheetMock.mockImplementationOnce(() => {
      throw new Error("boom");
    });
    const { message } = await import("antd");
    const errorSpy = vi.spyOn(message, "error");

    renderWithProviders(<DatasetTemplateDownload />);
    fireEvent.click(screen.getByText("datasetManagement.template.downloadExcel"));

    expect(errorSpy).toHaveBeenCalledWith("datasetManagement.template.downloadFailed");
  });
});
