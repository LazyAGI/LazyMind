package doc

import (
	"context"
	"log"
	"time"
)

const formulaBackupDelay = 6 * time.Second

// One shared flight owns both attempts. Only a validated result can win;
// cancelling one viewer still leaves the other viewers' shared work intact.
func runFormulaGeneration(ctx context.Context, generate func(context.Context) (formulaExplanation, error), backup <-chan time.Time) (formulaExplanation, error) {
	if err := ctx.Err(); err != nil {
		return formulaExplanation{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		value formulaExplanation
		err   error
	}
	answers := make(chan answer, 2)
	start := func() { go func() { value, err := generate(ctx); answers <- answer{value, err} }() }
	start()
	pending := 1
	backupStarted := false
	for {
		select {
		case <-ctx.Done():
			return formulaExplanation{}, ctx.Err()
		case result := <-answers:
			pending--
			if result.err == nil {
				return result.value, nil
			}
			if pending == 0 {
				if err := ctx.Err(); err != nil {
					return formulaExplanation{}, err
				}
				if backupStarted {
					return formulaExplanation{}, result.err
				}
				backupStarted = true
				backup = nil
				log.Print("formula_explanation failed_request_retry_started attempts=2")
				pending++
				start()
			}
		case <-backup:
			backup = nil
			if err := ctx.Err(); err != nil {
				return formulaExplanation{}, err
			}
			// Prefer an already completed first call over starting unnecessary work.
			select {
			case result := <-answers:
				pending--
				if result.err == nil {
					return result.value, nil
				}
			default:
			}
			backupStarted = true
			log.Print("formula_explanation slow_request_backup_started attempts=2")
			pending++
			start()
		}
	}
}
