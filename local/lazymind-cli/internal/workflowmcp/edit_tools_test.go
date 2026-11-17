package workflowmcp

import "testing"

func TestEditSlotsPreserveStableIdentityAndRejectNestedPath(t *testing.T) {
	i := 7
	slots, err := encodeEditSlots(map[string][]EditItem{"pages": {{ListIndex: &i}, {ContentType: "text", Value: "new"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(slots["pages"][0]) != 1 || slots["pages"][0]["list_index"] != 7 {
		t.Fatal(slots)
	}
	if _, ok := slots["pages"][1]["list_index"]; ok {
		t.Fatal("new page got invented identity")
	}
	_, err = encodeEditSlots(map[string][]EditItem{"download": {{Value: map[string]any{"local_path": "missing.pptx"}}}})
	if err == nil {
		t.Fatal("nested path accepted")
	}
}
