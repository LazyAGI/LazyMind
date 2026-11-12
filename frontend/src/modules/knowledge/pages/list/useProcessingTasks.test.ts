import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import { useProcessingTasks } from "./useProcessingTasks";
vi.mock("@/components/request", () => ({ BASE_URL:"", axiosInstance:{get:vi.fn()} }));
const get=vi.mocked(axiosInstance.get);
const page=(count:number)=>({data:{items:[],counts:{active:count},unfinished_count:count,total:0,page:1,page_size:20}});
beforeEach(()=>{vi.useFakeTimers();vi.clearAllMocks();vi.spyOn(document,"hidden","get").mockReturnValue(false);get.mockResolvedValue(page(0));});
afterEach(()=>{cleanup();vi.restoreAllMocks();vi.useRealTimers();});
it("polls new work and retains last snapshot on failure",async()=>{
 get.mockResolvedValueOnce(page(2)).mockRejectedValueOnce(new Error("offline"));
 const {result}=renderHook(()=>useProcessingTasks(true,""));await act(async()=>{});
 await act(async()=>{await vi.advanceTimersByTimeAsync(5000);});
 expect(result.current.queryFailed).toBe(true);expect(result.current.data?.unfinished_count).toBe(2);
});
it("ignores a stale response after filter changes",async()=>{
 let done!:(value:ReturnType<typeof page>)=>void;get.mockImplementationOnce(()=>new Promise(resolve=>{done=resolve;}));
 const {result}=renderHook(()=>useProcessingTasks(true,""));
 await act(async()=>{result.current.setFilters({status:"failed",level:"",q:"",page:1});});
 await act(async()=>{done(page(9));});
 expect(result.current.data?.unfinished_count).toBe(0);
 expect(get).toHaveBeenLastCalledWith(expect.any(String),expect.objectContaining({params:expect.objectContaining({status:"failed"})}));
});
it("does not claim an empty task list on initial failure",async()=>{
 get.mockRejectedValue(new Error("offline"));const {result}=renderHook(()=>useProcessingTasks(false,""));await act(async()=>{});
 expect(result.current.data).toBeUndefined();expect(result.current.queryFailed).toBe(true);
});
