package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/historyinjection"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: history-injection export|compact|pack|import [flags]")
	}
	switch args[0] {
	case "export":
		return runExport(ctx, args[1:])
	case "pack":
		return runPack(args[1:])
	case "compact":
		return runCompact(args[1:])
	case "import":
		return runImport(ctx, args[1:])
	default:
		return fmt.Errorf("unknown history-injection command %q", args[0])
	}
}

func runCompact(args []string) error {
	flags := flag.NewFlagSet("compact", flag.ContinueOnError)
	source := flags.String("source", "", "")
	output := flags.String("output", "", "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	stats, err := historyinjection.CompactPortableSQLFile(*source, *output)
	if err != nil {
		return err
	}
	fmt.Printf("compacted sub_agent_steps %d -> %d (removed %d)\n",
		stats.InputSteps, stats.OutputSteps, stats.MergedSteps())
	return nil
}

func database() (*orm.DB, error) {
	driver := strings.TrimSpace(os.Getenv("ACL_DB_DRIVER"))
	dsn := strings.TrimSpace(os.Getenv("ACL_DB_DSN"))
	if driver == "" || dsn == "" {
		return nil, fmt.Errorf("ACL_DB_DRIVER and ACL_DB_DSN are required")
	}
	return orm.Connect(driver, dsn)
}

func runExport(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	bundleID := flags.String("bundle-id", "", "")
	category := flags.String("category", "ppt", "")
	title := flags.String("title", "", "")
	conversationID := flags.String("conversation-id", "", "")
	workflowRef := flags.String("workflow-ref", "builtin:ppt-workflow", "")
	output := flags.String("output", "", "")
	uploadRoot := flags.String("upload-root", envOr("LAZYMIND_UPLOAD_ROOT", "/var/lib/lazymind/uploads"), "")
	subagentRoot := flags.String("subagent-root", envOr("LAZYMIND_SUBAGENT_WORKSPACE", "/data/subagent"), "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	db, err := database()
	if err != nil {
		return err
	}
	manifest, err := historyinjection.Export(ctx, db.DB, historyinjection.ExportOptions{
		BundleID: *bundleID, Category: *category, Title: *title, ConversationID: *conversationID,
		WorkflowRef: *workflowRef, OutputDir: *output, UploadRoot: *uploadRoot, SubagentRoot: *subagentRoot,
	})
	if err != nil {
		return err
	}
	fmt.Printf("exported %s (%s) with %d files\n", manifest.BundleID, manifest.ConversationID, len(manifest.Files))
	return nil
}

func runPack(args []string) error {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	source := flags.String("source", "", "")
	output := flags.String("output", "", "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return historyinjection.Pack(*source, *output)
}

func runImport(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	root := flags.String("root", "", "")
	ownerID := flags.String("owner-id", "", "")
	ownerName := flags.String("owner-name", "admin", "")
	uploadRoot := flags.String("upload-root", envOr("LAZYMIND_UPLOAD_ROOT", "/var/lib/lazymind/uploads"), "")
	subagentRoot := flags.String("subagent-root", envOr("LAZYMIND_SUBAGENT_WORKSPACE", "/data/subagent"), "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	db, err := database()
	if err != nil {
		return err
	}
	results, err := historyinjection.ApplyAll(ctx, db.DB, *root,
		historyinjection.TargetOwner{ID: *ownerID, Username: *ownerName},
		historyinjection.RuntimeRoots{Uploads: *uploadRoot, Subagent: *subagentRoot})
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Printf("injected %s conversation=%s files=%d already_present=%t\n",
			result.BundleID, result.ConversationID, result.FilesCopied, result.AlreadyPresent)
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
