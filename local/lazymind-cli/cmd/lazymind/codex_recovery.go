package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/credentials"
)

var recoveryID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func runCodexRecovery(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: internal codex-recovery <wait|dismiss|reconnect> [request-id]")
	}
	store, err := credentials.NewStore("", "")
	if err != nil {
		return err
	}
	home := store.Directory()
	switch args[0] {
	case "view-complete":
		if len(args) != 2 || !recoveryID.MatchString(args[1]) {
			return errors.New("invalid recovery request identifier")
		}
		if err := codexhost.CompleteViewRecovery(ctx, home, args[1]); err != nil {
			return err
		}
		return printJSON(stdout, map[string]bool{"ok": true})
	case "status":
		if len(args) != 2 || !recoveryID.MatchString(args[1]) {
			return errors.New("invalid recovery request identifier")
		}
		bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		ready, err := codexhost.PresentationReady(bounded, home, args[1])
		if err != nil {
			return err
		}
		return printJSON(stdout, map[string]bool{"ready": ready})
	case "wait":
		if len(args) != 1 {
			return errors.New("unexpected recovery arguments")
		}
		bounded, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		result, err := codexhost.WaitRecovery(bounded, home)
		if err != nil {
			return err
		}
		return printJSON(stdout, result)
	case "dismiss", "reconnect":
		if len(args) != 2 || !recoveryID.MatchString(args[1]) {
			return errors.New("invalid recovery request identifier")
		}
		if args[0] == "dismiss" {
			if err := codexhost.DismissRecovery(ctx, home, args[1]); err != nil {
				return err
			}
			return printJSON(stdout, map[string]bool{"ok": true})
		}
		bounded, cancel := context.WithTimeout(ctx, 150*time.Second)
		defer cancel()
		// Resolve against the current pairing immediately before doing anything.
		request, err := codexhost.CurrentRecovery(bounded, home)
		if err != nil {
			return err
		}
		if request.Request == nil || request.Request.ID != args[1] || !request.Request.CanRestart {
			return errors.New("Codex recovery request expired; retry the original operation")
		}
		encoder := json.NewEncoder(stdout)
		err = codexhost.ReconnectRequest(bounded, home, args[1], func(stage string) { _ = encoder.Encode(map[string]string{"stage": stage}) })
		if err != nil {
			return err
		}
		return encoder.Encode(map[string]string{"stage": "done"})
	default:
		return errors.New("unsupported Codex recovery action")
	}
}
