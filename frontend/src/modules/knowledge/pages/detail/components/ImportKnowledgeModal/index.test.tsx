import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ImportKnowledgeModal, { type IImportKnowledgeModalRef } from "./index";

const mocks = vi.hoisted(() => ({
  availability: "missing",
  wait: vi.fn().mockResolvedValue(undefined),
  build: vi.fn().mockResolvedValue([{ upload_file_id: "upload-1" }]),
  create: vi.fn().mockResolvedValue({ data: { tasks: [{ task_id: "task-1" }] } }),
  start: vi.fn().mockResolvedValue({ data: {} }),
}));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({ availability: mocks.availability }),
}));
vi.mock("@/runtime/readiness", () => ({
  waitForRuntimeCapability: mocks.wait,
  RuntimeReadinessError: class extends Error {},
}));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code }));
vi.mock("@/modules/knowledge/utils/uploadByHash", () => ({ buildUploadTaskItems: mocks.build }));
vi.mock("@/modules/knowledge/utils/request", () => ({
  getKnowledgeOptions: vi.fn().mockResolvedValue({
    upload: {
      max_files: 20, max_file_bytes: 500 * 1024 * 1024,
      max_batch_bytes: 1024 * 1024 * 1024, file_types: ["pdf"],
    },
  }),
  DocumentServiceApi: () => ({ documentServiceAllDocumentTags: async () => ({ data: { tags: [] } }) }),
  TaskServiceApi: () => ({ createTasks: mocks.create, startTasks: mocks.start }),
}));
vi.mock("@/modules/knowledge/store/dataset_permission", () => ({
  useDatasetPermissionStore: (selector: (state: unknown) => unknown) => selector({
    hasOnlyReadPermission: () => false, hasUploadPermission: () => false, hasWritePermission: () => true,
  }),
}));
vi.mock("@/modules/knowledge/components/TagSelect", () => ({ default: () => null }));
vi.mock("../DragUpload", () => ({
  ALLOWED_FILE_TYPES: ["pdf"],
  default: ({ onChange }: { onChange: (files: unknown[]) => void }) => (
    <><button type="button" onClick={() => onChange([{ originFile: new File(["PDF"], "paper.pdf"), path: "paper.pdf" }])}>select PDF</button>
    <button type="button" onClick={() => onChange(["first.txt", "broken.pdf", "last.txt"].map(name => ({originFile:new File(["content"],name),path:name})))}>select mixed</button><button type="button" onClick={() => onChange(["early.txt", "later.txt"].map(name => ({originFile:new File(["content"],name),path:`shared/${name}`})))}>select folder</button></>
  ),
}));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("storage-only uploads", () => {
  it.each(["create", "start"] as const)("clears upload activity when %s submission fails", async (phase) => {
    mocks[phase].mockRejectedValueOnce(new Error("提交失败"));
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const onUploadSettled = vi.fn();
    const onOk = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    try {
      render(<ImportKnowledgeModal ref={ref} onOk={onOk} onUploadSettled={onUploadSettled} />);
      await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
      fireEvent.click(screen.getByText("select PDF"));
      fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
      await waitFor(() => expect(onUploadSettled).toHaveBeenCalledTimes(1));
      if (phase === "create") {
        expect(onOk).not.toHaveBeenCalled();
        expect((await screen.findAllByText("提交失败")).length).toBeGreaterThan(0);
      } else {
        expect(onOk).toHaveBeenCalled();
        expect((await screen.findAllByText("原文件已保存，处理暂未启动；服务就绪后可在知识库中重试")).length).toBeGreaterThan(0);
      }
    } finally { log.mockRestore(); }
  });
  it.each(["create", "start"] as const)("keeps upload activity until %s submission finishes", async (phase) => {
    let finishSubmission!: (response: { data: { tasks: { task_id: string }[] } }) => void;
    mocks[phase].mockImplementationOnce(() => new Promise((resolve) => { finishSubmission = resolve; }));
    const onUploadStart = vi.fn();
    const onUploadSettled = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    render(<ImportKnowledgeModal ref={ref} onOk={vi.fn()} onUploadStart={onUploadStart} onUploadSettled={onUploadSettled} />);
    await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
    fireEvent.click(screen.getByText("select PDF"));
    fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
    await waitFor(() => expect(mocks[phase]).toHaveBeenCalled());
    expect(onUploadStart).toHaveBeenCalledTimes(1);
    expect(onUploadSettled).not.toHaveBeenCalled();
    await act(async () => finishSubmission({ data: { tasks: [{ task_id: "task-1" }] } }));
    await waitFor(() => expect(onUploadSettled).toHaveBeenCalledTimes(1));
  });
  it("reports browser upload activity before parsing is submitted", async () => {
    let finishUpload!: (items: { upload_file_id: string }[]) => void;
    mocks.build.mockImplementationOnce(() => new Promise((resolve) => { finishUpload = resolve; }));
    const onUploadStart = vi.fn();
    const onUploadSettled = vi.fn();
    const ref = createRef<IImportKnowledgeModalRef>();
    render(<ImportKnowledgeModal ref={ref} onOk={vi.fn()} onUploadStart={onUploadStart} onUploadSettled={onUploadSettled} />);
    await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
    fireEvent.click(screen.getByText("select PDF"));
    fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
    await waitFor(() => expect(mocks.build).toHaveBeenCalled());
    expect(onUploadStart).toHaveBeenCalledTimes(1);
    expect(onUploadSettled).not.toHaveBeenCalled();
    await act(async () => finishUpload([{ upload_file_id: "upload-1" }]));
    await waitFor(() => expect(onUploadSettled).toHaveBeenCalledTimes(1));
  });
  it.each([
    ["missing", false],
    ["ready", true],
  ])("uploads without parser readiness when availability=%s, storageOnly=%s", async (availability, storageOnly) => {
    mocks.availability = availability as string;
    const ref = createRef<IImportKnowledgeModalRef>();
    const onOk = vi.fn();
    render(<ImportKnowledgeModal ref={ref} onOk={onOk} storageOnly={storageOnly as boolean} />);
    await act(async () => ref.current!.handleOpen({ dataset_id: "papers" }));
    expect(screen.getByText(storageOnly
      ? "文件将保留在知识库内，仅保存原文件，不解析、不建立索引。"
      : "原文件可以正常上传。解析和索引进度可在知识库中查看，服务未就绪时可稍后重试。")).toBeInTheDocument();
    fireEvent.click(screen.getByText("select PDF"));
    fireEvent.click(screen.getByRole("button", { name: "上传并保存" }));
    await waitFor(() => expect(onOk).toHaveBeenCalled());
    expect(mocks.wait).not.toHaveBeenCalled();
    expect(mocks.create).toHaveBeenCalledWith("papers", { items: [{ upload_file_id: "upload-1" }], allow_partial: true });
    expect(mocks.start).toHaveBeenCalledWith("papers", { task_ids: ["task-1"], start_mode: "DEFAULT" });
  });
});

 it("refreshes and starts successful items, then retries only the failed file", async () => {
    mocks.availability = "missing";
    mocks.build.mockImplementation(async ({fileItems}: any) => fileItems.map((item:any)=>({upload_file_id:item.originFile.name,task:{display_name:item.originFile.name}})));
    mocks.create.mockResolvedValueOnce({data:{tasks:[{task_id:"first",display_name:"first.txt"},{task_id:"last",display_name:"last.txt"}],outcomes:[
      {input_index:0,status:"CREATED",task_ids:["first"],display_name:"first.txt"},
      {input_index:1,status:"FAILED",task_ids:[],display_name:"broken.pdf",target_pid:"existing-folder",message:"PDF is incomplete"},
      {input_index:2,status:"CREATED",task_ids:["last"],display_name:"last.txt"},
    ]}});
    mocks.start.mockResolvedValueOnce({data:{tasks:[{task_id:"first"},{task_id:"last"}]}});
    const onOk=vi.fn();const ref=createRef<IImportKnowledgeModalRef>();
    render(<ImportKnowledgeModal ref={ref} onOk={onOk}/>);
    await act(async()=>ref.current!.handleOpen({dataset_id:"papers"}));
    fireEvent.click(screen.getByText("select mixed"));
    fireEvent.click(screen.getByRole("button",{name:"上传并保存"}));
    await waitFor(()=>expect(onOk).toHaveBeenCalledTimes(1));
    expect(mocks.start).toHaveBeenCalledWith("papers",{task_ids:["first","last"],start_mode:"DEFAULT"});
    expect((await screen.findAllByText(/broken.pdf.*PDF is incomplete/)).length).toBeGreaterThan(0);
    mocks.create.mockResolvedValueOnce({data:{tasks:[],outcomes:[{input_index:0,status:"FAILED",display_name:"broken.pdf",message:"PDF is incomplete"}]}});
    fireEvent.click(screen.getByRole("button",{name:"上传并保存"}));
    await waitFor(()=>expect(mocks.create).toHaveBeenCalledTimes(2));
    expect(mocks.build.mock.calls[mocks.build.mock.calls.length - 1]?.[0].fileItems.map((x:any)=>x.originFile.name)).toEqual(["broken.pdf"]);
    expect(mocks.start).toHaveBeenCalledTimes(1);
    expect(mocks.create.mock.calls[1][1].items[0].task.document_pid).toBe("existing-folder");
 });

it("keeps partial archive task IDs and does not offer the whole archive for blind retry",async()=>{
  mocks.availability="missing";
  mocks.build.mockResolvedValueOnce([{upload_file_id:"archive",task:{display_name:"archive.zip"}}]);
  mocks.create.mockResolvedValueOnce({data:{tasks:[{task_id:"member"}],outcomes:[{input_index:0,status:"PARTIAL",task_ids:["member"],display_name:"archive.zip",message:"broken.pdf: PDF is incomplete"}]}});
  mocks.start.mockResolvedValueOnce({data:{tasks:[{task_id:"member"}]}});
  const onOk=vi.fn(),ref=createRef<IImportKnowledgeModalRef>();render(<ImportKnowledgeModal ref={ref} onOk={onOk}/>);
  await act(async()=>ref.current!.handleOpen({dataset_id:"papers"}));
  fireEvent.click(screen.getByText("select PDF"));fireEvent.click(screen.getByRole("button",{name:"上传并保存"}));
  await waitFor(()=>expect(onOk).toHaveBeenCalledTimes(1));
  expect((await screen.findAllByText(/部分成员已保存/)).length).toBeGreaterThan(0);
  fireEvent.click(screen.getByRole("button",{name:"上传并保存"}));
  await screen.findByText("knowledge.selectFile");
  expect(mocks.create).toHaveBeenCalledTimes(1);
});

it("reuses the created folder for a file that failed before task creation",async()=>{
  mocks.availability="missing";
  mocks.build.mockImplementationOnce(async ({fileItems,onFileError}:any)=>{
    onFileError(fileItems[0],new Error("upload failed"));
    return [{upload_file_id:"later-upload",task:{display_name:"later.txt",relative_path:"shared/later.txt"}}];
  });
  mocks.build.mockImplementationOnce(async ({fileItems}:any)=>fileItems.map((file:any)=>({upload_file_id:"early-upload",task:{display_name:file.originFile.name,relative_path:file.path}})));
  mocks.create.mockResolvedValueOnce({data:{tasks:[{task_id:"later"}],outcomes:[{input_index:0,status:"CREATED",target_pid:"existing-shared",task_ids:["later"]}]}});
  mocks.create.mockResolvedValueOnce({data:{tasks:[{task_id:"early"}],outcomes:[{input_index:0,status:"CREATED",target_pid:"existing-shared",task_ids:["early"]}]}});
  mocks.start.mockResolvedValue({data:{tasks:[]}});
  const onOk=vi.fn(),ref=createRef<IImportKnowledgeModalRef>();render(<ImportKnowledgeModal ref={ref} onOk={onOk}/>);
  await act(async()=>ref.current!.handleOpen({dataset_id:"papers",importMode:"folder"}));
  fireEvent.click(screen.getByText("select folder"));fireEvent.click(screen.getByRole("button",{name:"上传并保存"}));
  await waitFor(()=>expect(onOk).toHaveBeenCalledTimes(1));
  expect((await screen.findAllByText(/shared\/early.txt.*upload failed/)).length).toBeGreaterThan(0);
  fireEvent.click(screen.getByRole("button",{name:"上传并保存"}));
  await waitFor(()=>expect(mocks.create).toHaveBeenCalledTimes(2));
  expect(mocks.create.mock.calls[1][1].items).toEqual([{upload_file_id:"early-upload",task:{display_name:"early.txt",relative_path:"shared/early.txt",document_pid:"existing-shared"}}]);
});

it("localizes duplicate-name failures returned in batch outcomes", async () => {
  mocks.create.mockResolvedValueOnce({data:{tasks:[],outcomes:[{input_index:0,status:"FAILED",message:"target already contains this name"}]}});
  const ref=createRef<IImportKnowledgeModalRef>();
  render(<ImportKnowledgeModal ref={ref} onOk={vi.fn()} />);
  await act(async()=>ref.current!.handleOpen({dataset_id:"papers"}));
  fireEvent.click(screen.getByText("select PDF"));
  fireEvent.click(screen.getByRole("button",{name:"上传并保存"}));
  expect((await screen.findAllByText(/当前文件夹中已有同名文件/)).length).toBeGreaterThan(0);
  expect(screen.queryByText(/target already contains this name/)).toBeNull();
});
