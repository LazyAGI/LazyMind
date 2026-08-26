package modelprovider

import (
	"context"
	"testing"
)

func TestEvoAlwaysRequiresDynamicSelection(t *testing.T) {
	dynamic, err := requiresDynamicSelection(context.Background(), EvoModelKey)
	if err != nil || !dynamic {
		t.Fatalf("evo_llm dynamic requirement = (%v, %v), want (true, nil)", dynamic, err)
	}
}
