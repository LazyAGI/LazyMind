import { describe, expect, it } from "vitest";
import { cloudActionErrorMessage } from "./cloudErrorMessage";
const translate=(key:string)=>key;
describe("cloud action errors",()=>{
  it.each([[2005301,"invalid_token"],[2005302,"credentials_unavailable"],[2005304,"rate_limited"],[2005305,"service_unavailable"]])("maps safe API code %s",(code,key)=>{
    expect(cloudActionErrorMessage({response:{data:{code,message:"secret upstream"}}},translate)).toBe(`vocabularyCloud.errors.${key}`);
  });
  it("maps known transport failures without exposing arbitrary response text",()=>{
    expect(cloudActionErrorMessage({code:"ERR_NETWORK"},translate)).toBe("vocabularyCloud.errors.request_failed");
    for(const error of [new Error("secret"),{response:{data:{code:99999,message:"secret"}}},{code:"secret"},{response:{data:{code:"constructor"}}},{response:{data:{code:"__proto__"}}},null])expect(cloudActionErrorMessage(error,translate)).toBe("vocabularyCloud.actionFailed");
  });
});
