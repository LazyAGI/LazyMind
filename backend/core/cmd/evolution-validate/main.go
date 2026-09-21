// evolution-validate runs the real Evo validator and records its bounded evidence.
// It requires trusted database access; it is deliberately not an HTTP endpoint.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	user := flag.String("user-id", "", "model owner or authorized user")
	modelID := flag.String("model-id", "", "model ID; omit to validate the configured default")
	inputsPath := flag.String("isolated-inputs", "", "ThreadInputs JSON for an isolated test router/corpus (never production)")
	reportPath := flag.String("report", "", "new private JSON report file")
	flag.Parse()
	if *user == "" || *reportPath == "" {
		return fmt.Errorf("--user-id and --report are required")
	}
	driver, dsn := os.Getenv("ACL_DB_DRIVER"), os.Getenv("ACL_DB_DSN")
	if driver == "" || dsn == "" {
		return fmt.Errorf("ACL_DB_DRIVER and ACL_DB_DSN are required")
	}
	db, err := orm.Connect(driver, dsn)
	if err != nil {
		return fmt.Errorf("database unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	config, model, err := modelconfig.EvolutionValidationConfig(ctx, db.DB, *user, *modelID)
	if err != nil {
		return fmt.Errorf("authorized model configuration unavailable")
	}
	var inputs map[string]any
	if *inputsPath != "" {
		file, err := os.Open(*inputsPath)
		if err != nil {
			return fmt.Errorf("cannot read isolated inputs")
		}
		defer file.Close()
		if err := json.NewDecoder(io.LimitReader(file, 1024*1024)).Decode(&inputs); err != nil {
			return fmt.Errorf("invalid isolated inputs")
		}
	}
	file, err := os.OpenFile(*reportPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("report must be a new writable file")
	}
	defer file.Close()
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("cannot allocate validation nonce")
	}
	nonce := hex.EncodeToString(nonceBytes)
	packet, err := json.Marshal(map[string]any{"llm_config": config, "model_ref": model.ModelRef, "nonce": nonce, "workflow_inputs": inputs})
	if err != nil {
		return fmt.Errorf("cannot prepare validation")
	}
	args := flag.Args()
	if len(args) == 0 {
		args = []string{"python3", "-m", "evo.validation"}
	}
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdin = bytes.NewReader(packet)
	command.Stderr = io.Discard // provider logs may contain private configuration
	raw, runErr := command.Output()
	var report modelconfig.EvolutionValidationReport
	if runErr != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, &report) != nil {
		// A failed runner revokes previous evidence for this exact configuration.
		report = modelconfig.EvolutionValidationReport{ModelRef: model.ModelRef, Nonce: nonce,
			ValidationVersion: modelconfig.EvolutionValidationVersion, Failures: map[string]string{"runner": "validation_failed"}}
	}
	// Serialize only the public report schema, never arbitrary runner output.
	report.Passed = report.Valid(model.ModelRef, nonce)
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode report")
	}
	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("cannot save report")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("cannot persist report")
	}
	digest := sha256.Sum256(encoded)
	evidenceID := "sha256:" + hex.EncodeToString(digest[:])
	persistContext, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelPersist()
	if err := modelconfig.SaveEvolutionValidation(persistContext, db.DB, *user, model.ModelRef, nonce, evidenceID, report); err != nil {
		return fmt.Errorf("configuration changed or evidence could not be saved; rerun validation")
	}
	fmt.Printf("model=%s passed=%t evidence=%s\n", model.DisplayName, report.Passed, evidenceID)
	if !report.Passed {
		return fmt.Errorf("capability validation incomplete; inspect the private report")
	}
	return nil
}
