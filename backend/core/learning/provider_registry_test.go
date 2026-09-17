package learning

import "testing"

func TestLearningRegistryIsInternallyConsistent(t *testing.T) {
	if err := ValidateRegistry(); err != nil {
		t.Fatal(err)
	}
}
