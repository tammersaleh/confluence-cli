package main

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/alecthomas/kong"
	"github.com/tammersaleh/confluence-sync/internal/cli"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

func main() {
	var c cli.CLI
	ctx := kong.Parse(&c,
		kong.Name("confluence"),
		kong.Description("CLI for Confluence."),
		kong.UsageOnError(),
	)

	err := ctx.Run(&c)
	if err == nil {
		return
	}

	var exitErr *output.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.Code)
	}

	var oErr *output.Error
	if errors.As(err, &oErr) {
		_ = json.NewEncoder(os.Stderr).Encode(oErr)
		os.Exit(oErr.Code)
	}

	// Unstructured errors: wrap in JSON for consistency.
	_ = json.NewEncoder(os.Stderr).Encode(map[string]string{
		"error":  "general_error",
		"detail": err.Error(),
	})
	os.Exit(output.ExitGeneral)
}
