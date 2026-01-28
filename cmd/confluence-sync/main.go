package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/filesystem"
	"github.com/tammersaleh/confluence-sync/internal/sync"
)

const (
	ExitSuccess       = 0
	ExitConfigError   = 1
	ExitAuthError     = 2
	ExitAPIError      = 3
	ExitFilesysError  = 4
)

var (
	outputDir string
	clean     bool
	dryRun    bool
	verbose   bool
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(ExitConfigError)
	}
}

var rootCmd = &cobra.Command{
	Use:   "confluence-sync <space-url>",
	Short: "Sync Confluence space pages to local Markdown files",
	Long: `confluence-sync fetches all pages from a Confluence space and writes them
as Markdown files to a local directory. The page hierarchy is preserved
as a directory structure.

Environment variables:
  ATLASSIAN_API_KEY    API token for authentication
  ATLASSIAN_API_EMAIL  Account email for authentication

Example:
  confluence-sync https://acme.atlassian.net/wiki/spaces/ENG -o ./docs`,
	Args: cobra.ExactArgs(1),
	RunE: runSync,
}

func init() {
	rootCmd.Flags().StringVarP(&outputDir, "output", "o", "./output", "Output directory")
	rootCmd.Flags().BoolVar(&clean, "clean", false, "Delete output directory before sync")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be synced without writing files")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
}

func runSync(cmd *cobra.Command, args []string) error {
	spaceURL := args[0]

	// Parse space URL
	baseURL, spaceKey, err := confluence.ParseSpaceURL(spaceURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid space URL: %v\n", err)
		os.Exit(ExitConfigError)
	}

	// Get credentials from environment
	apiKey := os.Getenv("ATLASSIAN_API_KEY")
	apiEmail := os.Getenv("ATLASSIAN_API_EMAIL")

	if apiKey == "" || apiEmail == "" {
		fmt.Fprintln(os.Stderr, "Missing required environment variables:")
		if apiKey == "" {
			fmt.Fprintln(os.Stderr, "  ATLASSIAN_API_KEY")
		}
		if apiEmail == "" {
			fmt.Fprintln(os.Stderr, "  ATLASSIAN_API_EMAIL")
		}
		os.Exit(ExitConfigError)
	}

	// Create client and syncer
	client := confluence.NewClient(baseURL, apiEmail, apiKey)
	fs := filesystem.OS{}
	syncer := sync.New(client, fs)

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if verbose {
			fmt.Fprintln(os.Stderr, "\nInterrupted, cleaning up...")
		}
		cancel()
	}()

	// Run sync
	if verbose {
		fmt.Printf("Syncing space %s to %s\n", spaceKey, outputDir)
		if dryRun {
			fmt.Println("(dry-run mode)")
		}
	}

	opts := sync.Options{
		Clean:   clean,
		DryRun:  dryRun,
		Verbose: verbose,
	}

	if err := syncer.Sync(ctx, spaceKey, outputDir, opts); err != nil {
		return handleError(err)
	}

	if verbose {
		fmt.Println("Sync complete")
	}

	return nil
}

func handleError(err error) error {
	if errors.Is(err, confluence.ErrUnauthorized) {
		fmt.Fprintln(os.Stderr, "Authentication failed: check ATLASSIAN_API_KEY and ATLASSIAN_API_EMAIL")
		os.Exit(ExitAuthError)
	}
	if errors.Is(err, confluence.ErrForbidden) {
		fmt.Fprintln(os.Stderr, "Access denied: insufficient permissions for this space")
		os.Exit(ExitAuthError)
	}
	if errors.Is(err, confluence.ErrSpaceNotFound) {
		fmt.Fprintf(os.Stderr, "Space not found\n")
		os.Exit(ExitAPIError)
	}
	if errors.Is(err, confluence.ErrAPIError) {
		fmt.Fprintf(os.Stderr, "API error: %v\n", err)
		os.Exit(ExitAPIError)
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "Operation canceled")
		os.Exit(ExitConfigError)
	}

	// Check for filesystem errors
	if os.IsPermission(err) || os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Filesystem error: %v\n", err)
		os.Exit(ExitFilesysError)
	}

	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(ExitAPIError)
	return nil
}
