package paperguide

// The shared Python tests and testdata/request-budget.json measure each real
// instruction, output schema and input wrapper. The largest fixed prefix is
// 9600 UTF-8 bytes including the maximum SECTION map. The 10240 reserve also
// covers the 87-byte default system prompt and 512 bytes of message framing.
// PLAN map entries add at most 18 bytes per listed alias. The model packet
// saves at least 62 bytes for that evidence ID (67 canonical bytes vs. at most
// 5 alias bytes at 2048 evidence entries). The fixed reserve covers the map
// label and empty object. One UTF-8 byte is conservatively one token.
const guidePromptReserve = 10240

// Python's validated model_dump adds bounded default fields (including empty
// top-level arrays, locator start/cells/headers) and normalizes bbox floats.
// MarshalIndent below covers its JSON separator whitespace. These extra bounds
// cover the remaining expansion and are guarded for every input schema at its
// 2048-evidence limit, rather than assuming Go's compact JSON is the full prompt.
const guideInputDefaultsReserve = 256
const guideEvidenceExpansionReserve = 64
