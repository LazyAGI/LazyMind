package currentmemory

import "fmt"

type PreferenceCapacityExceededError struct {
	UsedItems int `json:"used_items"`
	MaxItems  int `json:"max_items"`
}

func (e *PreferenceCapacityExceededError) Error() string {
	return fmt.Sprintf(
		"preference index capacity exceeded: used_items=%d max_items=%d",
		e.UsedItems,
		e.MaxItems,
	)
}

func ValidatePreferenceCapacity(currentItems, nextItems, maxItems int) error {
	if nextItems > maxItems && nextItems > currentItems {
		return &PreferenceCapacityExceededError{
			UsedItems: nextItems,
			MaxItems:  maxItems,
		}
	}
	return nil
}
