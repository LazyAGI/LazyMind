package paperguide

// Shared cross-language guards in testdata/request-budget.json and the Python
// tests measure all four real instructions, output schemas and input wrappers.
// The largest prefix is 9420 UTF-8 bytes; 10240 also covers the 87-byte default
// system prompt and 512 bytes of provider/message framing, with headroom.
// As elsewhere in this runner, one UTF-8 byte is conservatively one token.
const guidePromptReserve = 10240

// Python's validated model_dump adds bounded default fields (including empty
// top-level arrays, locator start/cells/headers) and normalizes bbox floats.
// MarshalIndent below covers its JSON separator whitespace. These extra bounds
// cover the remaining expansion and are guarded for every input schema at its
// 2048-evidence limit, rather than assuming Go's compact JSON is the full prompt.
const guideInputDefaultsReserve = 256
const guideEvidenceExpansionReserve = 64
