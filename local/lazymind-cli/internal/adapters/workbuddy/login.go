package workbuddy

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"lazymind/agentconnector/internal/agentexec"
)

// Login brings the native client to the foreground and waits for its session.
// WorkBuddy owns the sign-in UI, credential protection and token refresh. There
// is no verified public login deep link, so do not invent one or run its CLI
// under a different authentication product.
func Login(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return waitForLogin(ctx, authFile(), launchForLogin, 500*time.Millisecond)
}

func waitForLogin(ctx context.Context, path string, launch func(context.Context) error, interval time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ready, _ := availability(path); ready {
		return nil
	}
	if err := launch(ctx); err != nil {
		return fmt.Errorf("open WorkBuddy sign-in: %w", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		ready, reason := availability(path)
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", reason, ctx.Err())
		case <-ticker.C:
		}
	}
}

func launchForLogin(ctx context.Context) error {
	application, err := agentexec.FindDesktopApplication(agentexec.DesktopApplication{
		BindingTarget:   agentexec.WorkBuddyDesktop,
		ExecutableNames: []string{"WorkBuddy", "WorkBuddy.exe", "WorkBuddy CN.exe"},
		Protocols:       []string{"workbuddy"},
		DisplayNames:    []string{"WorkBuddy", "WorkBuddy CN"},
	})
	if err != nil {
		return fmt.Errorf("locate WorkBuddy desktop: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "/usr/bin/open", "-a", application).Run()
	case "windows":
		// The interactive client must survive the login wait's cancellation.
		// Launch directly, without a shell or a forced restart of user sessions.
		cmd := exec.Command(application)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	default:
		return fmt.Errorf("WorkBuddy desktop sign-in is not supported on %s", runtime.GOOS)
	}
}
