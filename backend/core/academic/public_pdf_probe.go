package academic

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

func probeOpenPDFCandidates(ctx context.Context, client *http.Client, candidates []FulltextCandidate, doi string) paperPDFLookup {
	result := paperPDFLookup{}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		index     int
		candidate FulltextCandidate
		err       error
	}
	jobs := make(chan int, len(candidates))
	results := make(chan outcome, len(candidates))
	for i := range candidates {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for worker := 0; worker < 2 && worker < len(candidates); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				if probeCtx.Err() != nil {
					return
				}
				// Reserve time for queued copies without imposing a 750 ms limit on
				// a lone candidate. Each wave gets a share of the time still left.
				deadline, _ := probeCtx.Deadline()
				waves := (len(candidates) - i + 1) / 2
				budget := time.Until(deadline) / time.Duration(waves)
				attempt, stop := context.WithTimeout(probeCtx, budget)
				candidate := candidates[i]
				address, err := probePublicPDFCandidate(attempt, client, candidate, doi)
				candidate.URL = address
				if candidate.SourceType == "open_access_landing" {
					candidate.SourceType = "open_access_pdf"
				}
				if attempt.Err() != nil {
					err = attempt.Err()
				}
				stop()
				results <- outcome{i, candidate, err}
			}
		}()
	}
	go func() { workers.Wait(); close(results) }()
	failures := make([]string, len(candidates))
	appendFailures := func() {
		for _, failure := range failures {
			if failure != "" {
				result.Warnings = append(result.Warnings, failure)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			appendFailures()
			result.Warnings = append(result.Warnings, "开放来源: "+ctx.Err().Error())
			return result
		case out, ok := <-results:
			if !ok {
				appendFailures()
				return result
			}
			if out.err == nil && out.candidate.URL != "" {
				result.Candidate = &out.candidate
				appendFailures()
				return result
			}
			if out.err != nil {
				failures[out.index] = fmt.Sprintf("%s 副本 %d: %v", candidates[out.index].SourceProvider, out.index+1, out.err)
			}
		}
	}
}
