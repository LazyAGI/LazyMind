package taskdisplay

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var ErrInvalidProcessStep = errors.New("invalid public process step")
var publicID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func ValidateProcessStep(step PublicProcessStep) error {
	if !publicID.MatchString(step.StepID) || step.Revision < 1 || step.Order < 0 || step.Order > MaxProcessSteps || strings.TrimSpace(step.Title) == "" || utf8.RuneCountInString(step.Title) > 100 || utf8.RuneCountInString(step.Summary) > 300 {
		return ErrInvalidProcessStep
	}
	switch step.Status {
	case "pending", "running", "succeeded", "failed", "interrupted", "canceled":
	default:
		return ErrInvalidProcessStep
	}
	if step.ElapsedMS != nil && *step.ElapsedMS < 0 {
		return ErrInvalidProcessStep
	}
	if step.StartedAt != nil && step.FinishedAt != nil && step.FinishedAt.Before(*step.StartedAt) {
		return ErrInvalidProcessStep
	}
	if (step.Status == "pending" || step.Status == "running") && step.FinishedAt != nil {
		return ErrInvalidProcessStep
	}
	return nil
}
func Terminal(status string) bool {
	switch status {
	case "succeeded", "failed", "interrupted", "canceled", "cancelled", "expired":
		return true
	}
	return false
}
func ValidateTransition(previous, next PublicProcessStep) error {
	if err := ValidateProcessStep(next); err != nil {
		return err
	}
	if Terminal(previous.Status) && next.Status != previous.Status {
		return ErrInvalidProcessStep
	}
	if previous.Status == "running" && next.Status == "pending" {
		return ErrInvalidProcessStep
	}
	return nil
}
