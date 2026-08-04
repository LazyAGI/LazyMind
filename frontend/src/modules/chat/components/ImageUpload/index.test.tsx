import { createRef } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { act, renderWithProviders, waitFor } from "@/test/testUtils";
import ImageUpload, { type ImageUploadImperativeProps, allowedImageTypes } from "./index";

const mockUploadFileInChunks = vi.fn();

vi.mock("@/modules/chat/utils/chunkUpload", () => ({
  uploadFileInChunks: (...args: unknown[]) => mockUploadFileInChunks(...args),
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => `error:${code}`,
}));

vi.mock("antd", async () => {
  const actual = await vi.importActual<typeof import("antd")>("antd");
  return {
    ...actual,
    message: {
      ...actual.message,
      warning: vi.fn(),
      error: vi.fn(),
      info: vi.fn(),
    },
  };
});

function makeFile(name: string, size = 1024, type = "text/plain") {
  return new File(["x".repeat(size)], name, { type });
}

function baseProps(overrides: Partial<React.ComponentProps<typeof ImageUpload>> = {}) {
  return {
    max: 3,
    types: [".png", ".txt"],
    icon: <span>upload-icon</span>,
    updateFiles: vi.fn(),
    listNum: 0,
    ...overrides,
  };
}

describe("ImageUpload", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUploadFileInChunks.mockResolvedValue("stored/path.txt");
  });

  it("renders the provided icon", () => {
    const { getByText } = renderWithProviders(<ImageUpload {...baseProps()} />);
    expect(getByText("upload-icon")).toBeInTheDocument();
  });

  it("uploads a valid file and reports the stored path via updateFiles", async () => {
    const updateFiles = vi.fn();
    const ref = createRef<ImageUploadImperativeProps>();
    renderWithProviders(<ImageUpload ref={ref} {...baseProps({ updateFiles })} />);

    await act(async () => {
      ref.current?.uploadFiles([makeFile("notes.txt")]);
    });

    await waitFor(() => expect(mockUploadFileInChunks).toHaveBeenCalled());
    await waitFor(() =>
      expect(updateFiles).toHaveBeenCalledWith(
        expect.arrayContaining([expect.objectContaining({ uri: "stored/path.txt" })]),
      ),
    );
  });

  it("rejects files with disallowed extensions", async () => {
    const antd = await import("antd");
    const updateFiles = vi.fn();
    const ref = createRef<ImageUploadImperativeProps>();
    renderWithProviders(<ImageUpload ref={ref} {...baseProps({ updateFiles, types: [".png"] })} />);

    await act(async () => {
      ref.current?.uploadFiles([makeFile("script.exe")]);
    });

    expect(antd.message.warning).toHaveBeenCalled();
    expect(mockUploadFileInChunks).not.toHaveBeenCalled();
  });

  it("rejects images larger than 5MB", async () => {
    const antd = await import("antd");
    const ref = createRef<ImageUploadImperativeProps>();
    renderWithProviders(
      <ImageUpload ref={ref} {...baseProps({ types: allowedImageTypes })} />,
    );
    const bigFile = makeFile("photo.png", 6 * 1024 * 1024, "image/png");

    await act(async () => {
      ref.current?.uploadFiles([bigFile]);
    });

    expect(antd.message.error).toHaveBeenCalledWith("chat.uploadSizeLimit5MB");
    expect(mockUploadFileInChunks).not.toHaveBeenCalled();
  });

  it("blocks new uploads once the max file count is reached", async () => {
    const antd = await import("antd");
    const ref = createRef<ImageUploadImperativeProps>();
    renderWithProviders(<ImageUpload ref={ref} {...baseProps({ max: 1 })} />);

    await act(async () => {
      ref.current?.uploadFiles([makeFile("a.txt")]);
    });
    await waitFor(() => expect(mockUploadFileInChunks).toHaveBeenCalledTimes(1));

    await act(async () => {
      ref.current?.uploadFiles([makeFile("b.txt")]);
    });

    expect(antd.message.warning).toHaveBeenCalledWith("chat.maxThreeFilesAndImages");
    expect(mockUploadFileInChunks).toHaveBeenCalledTimes(1);
  });

  it("removes a file from the tracked list via removeFile", async () => {
    const updateFiles = vi.fn();
    const ref = createRef<ImageUploadImperativeProps>();
    renderWithProviders(<ImageUpload ref={ref} {...baseProps({ updateFiles })} />);

    await act(async () => {
      ref.current?.uploadFiles([makeFile("keep.txt")]);
    });
    await waitFor(() => expect(mockUploadFileInChunks).toHaveBeenCalled());
    const uid = ref.current?.getFiles()[0]?.uid;

    act(() => {
      ref.current?.removeFile(uid);
    });

    expect(ref.current?.getFiles()).toHaveLength(0);
    expect(updateFiles).toHaveBeenLastCalledWith([]);
  });

  it("clears all files via clear", async () => {
    const ref = createRef<ImageUploadImperativeProps>();
    renderWithProviders(<ImageUpload ref={ref} {...baseProps()} />);

    await act(async () => {
      ref.current?.uploadFiles([makeFile("a.txt"), makeFile("b.txt")]);
    });
    await waitFor(() => expect(ref.current?.getFiles().length).toBeGreaterThan(0));

    act(() => {
      ref.current?.clear();
    });
    expect(ref.current?.getFiles()).toHaveLength(0);
  });

  it("shows an error message and removes the file when upload fails", async () => {
    const antd = await import("antd");
    mockUploadFileInChunks.mockRejectedValueOnce(new Error("network down"));
    const ref = createRef<ImageUploadImperativeProps>();
    renderWithProviders(<ImageUpload ref={ref} {...baseProps()} />);

    await act(async () => {
      ref.current?.uploadFiles([makeFile("fails.txt")]);
    });

    await waitFor(() => expect(antd.message.error).toHaveBeenCalledWith("error:2000509"));
    expect(ref.current?.getFiles()).toHaveLength(0);
  });

  it("blocks uploads and shows the disabled reason when disabled", async () => {
    const antd = await import("antd");
    const ref = createRef<ImageUploadImperativeProps>();
    renderWithProviders(
      <ImageUpload ref={ref} {...baseProps({ disabled: true, disabledReason: "chat.uploadDisabled" })} />,
    );

    await act(async () => {
      ref.current?.uploadFiles([makeFile("a.txt")]);
    });

    expect(antd.message.warning).toHaveBeenCalledWith("chat.uploadDisabled");
    expect(mockUploadFileInChunks).not.toHaveBeenCalled();
  });
});
