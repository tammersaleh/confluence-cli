package sync

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/converter"
	"github.com/tammersaleh/confluence-sync/internal/filesystem"
	"github.com/tammersaleh/confluence-sync/pkg/sanitize"
)

type Options struct {
	Clean   bool
	DryRun  bool
	Verbose bool
	Logger  Logger
}

type Logger interface {
	Printf(format string, args ...interface{})
}

type noopLogger struct{}

func (noopLogger) Printf(string, ...interface{}) {}

type Syncer struct {
	client confluence.Client
	fs     filesystem.FileSystem
}

type stats struct {
	pages       int
	attachments int
}

func New(client confluence.Client, fs filesystem.FileSystem) *Syncer {
	return &Syncer{
		client: client,
		fs:     fs,
	}
}

func (s *Syncer) Sync(ctx context.Context, spaceKey, outputDir string, opts Options) error {
	log := opts.Logger
	if log == nil {
		log = noopLogger{}
	}

	// Get space
	space, err := s.client.GetSpace(ctx, spaceKey)
	if err != nil {
		return fmt.Errorf("getting space: %w", err)
	}

	if opts.Verbose {
		log.Printf("Found space: %s (%s)", space.Name, space.Key)
	}

	// Clean output directory if requested
	if opts.Clean && !opts.DryRun {
		if opts.Verbose {
			log.Printf("Cleaning output directory: %s", outputDir)
		}
		if err := s.fs.RemoveAll(outputDir); err != nil {
			return fmt.Errorf("cleaning output directory: %w", err)
		}
	}

	// Get all pages
	pages, err := s.client.GetPages(ctx, space.ID)
	if err != nil {
		return fmt.Errorf("getting pages: %w", err)
	}

	if opts.Verbose {
		log.Printf("Found %d pages", len(pages))
	}

	// Build tree
	tree := BuildTree(pages)

	// Sync each root node
	st := &stats{}
	for _, root := range tree {
		if err := s.syncNode(ctx, root, outputDir, opts, true, log, st); err != nil {
			return err
		}
	}

	if opts.Verbose {
		log.Printf("Synced %d pages, %d attachments", st.pages, st.attachments)
	}

	return nil
}

func (s *Syncer) syncNode(ctx context.Context, node *PageNode, parentDir string, opts Options, isRoot bool, log Logger, st *stats) error {
	// Get page content
	content, err := s.client.GetPageContent(ctx, node.Page.ID)
	if err != nil {
		return fmt.Errorf("getting content for %s: %w", node.Page.Title, err)
	}

	// Get attachments
	attachments, err := s.client.GetAttachments(ctx, node.Page.ID)
	if err != nil {
		return fmt.Errorf("getting attachments for %s: %w", node.Page.Title, err)
	}

	hasAttachments := len(attachments) > 0
	hasChildren := node.HasChildren()

	// Determine output path
	var pageDir string
	var mdPath string

	if isRoot {
		// Root page always becomes index.md in the output directory
		pageDir = parentDir
		mdPath = filepath.Join(parentDir, "index.md")
	} else if hasChildren || hasAttachments {
		// Pages with children or attachments get their own directory
		dirName := sanitize.Filename(node.Page.Title)
		pageDir = filepath.Join(parentDir, dirName)
		mdPath = filepath.Join(pageDir, "index.md")
	} else {
		// Leaf pages without attachments become single .md files
		pageDir = parentDir
		filename := s.getUniqueFilename(parentDir, node.Page.Title, opts)
		mdPath = filepath.Join(parentDir, filename+".md")
	}

	if opts.Verbose {
		log.Printf("  %s -> %s", node.Page.Title, mdPath)
	}

	// Convert content to Markdown
	attachmentPath := ""
	if hasAttachments {
		attachmentPath = "_attachments"
	}
	markdown := converter.ConvertWithOptions(content.Body, converter.Options{
		AttachmentPath: attachmentPath,
	})

	// Write markdown file
	if !opts.DryRun {
		if err := s.fs.MkdirAll(filepath.Dir(mdPath), 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
		if err := s.fs.WriteFile(mdPath, []byte(markdown), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", mdPath, err)
		}
	}
	st.pages++

	// Download attachments
	if hasAttachments && !opts.DryRun {
		attDir := filepath.Join(pageDir, "_attachments")
		if err := s.fs.MkdirAll(attDir, 0755); err != nil {
			return fmt.Errorf("creating attachments directory: %w", err)
		}

		for _, att := range attachments {
			if opts.Verbose {
				log.Printf("    attachment: %s", att.Title)
			}
			if err := s.downloadAttachment(ctx, att, attDir); err != nil {
				return fmt.Errorf("downloading attachment %s: %w", att.Title, err)
			}
			st.attachments++
		}
	}

	// Recursively sync children
	usedNames := make(map[string]bool)
	for _, child := range node.Children {
		if err := s.syncNodeWithUsedNames(ctx, child, pageDir, opts, usedNames, log, st); err != nil {
			return err
		}
	}

	return nil
}

func (s *Syncer) syncNodeWithUsedNames(ctx context.Context, node *PageNode, parentDir string, opts Options, usedNames map[string]bool, log Logger, st *stats) error {
	// Get page content
	content, err := s.client.GetPageContent(ctx, node.Page.ID)
	if err != nil {
		return fmt.Errorf("getting content for %s: %w", node.Page.Title, err)
	}

	// Get attachments
	attachments, err := s.client.GetAttachments(ctx, node.Page.ID)
	if err != nil {
		return fmt.Errorf("getting attachments for %s: %w", node.Page.Title, err)
	}

	hasAttachments := len(attachments) > 0
	hasChildren := node.HasChildren()

	// Determine output path
	var pageDir string
	var mdPath string

	if hasChildren || hasAttachments {
		// Pages with children or attachments get their own directory
		dirName := sanitize.FilenameWithCollision(node.Page.Title, usedNames)
		usedNames[dirName] = true
		pageDir = filepath.Join(parentDir, dirName)
		mdPath = filepath.Join(pageDir, "index.md")
	} else {
		// Leaf pages without attachments become single .md files
		pageDir = parentDir
		filename := sanitize.FilenameWithCollision(node.Page.Title, usedNames)
		usedNames[filename] = true
		mdPath = filepath.Join(parentDir, filename+".md")
	}

	if opts.Verbose {
		log.Printf("  %s -> %s", node.Page.Title, mdPath)
	}

	// Convert content to Markdown
	attachmentPath := ""
	if hasAttachments {
		attachmentPath = "_attachments"
	}
	markdown := converter.ConvertWithOptions(content.Body, converter.Options{
		AttachmentPath: attachmentPath,
	})

	// Write markdown file
	if !opts.DryRun {
		if err := s.fs.MkdirAll(filepath.Dir(mdPath), 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
		if err := s.fs.WriteFile(mdPath, []byte(markdown), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", mdPath, err)
		}
	}
	st.pages++

	// Download attachments
	if hasAttachments && !opts.DryRun {
		attDir := filepath.Join(pageDir, "_attachments")
		if err := s.fs.MkdirAll(attDir, 0755); err != nil {
			return fmt.Errorf("creating attachments directory: %w", err)
		}

		for _, att := range attachments {
			if opts.Verbose {
				log.Printf("    attachment: %s", att.Title)
			}
			if err := s.downloadAttachment(ctx, att, attDir); err != nil {
				return fmt.Errorf("downloading attachment %s: %w", att.Title, err)
			}
			st.attachments++
		}
	}

	// Recursively sync children
	childUsedNames := make(map[string]bool)
	for _, child := range node.Children {
		if err := s.syncNodeWithUsedNames(ctx, child, pageDir, opts, childUsedNames, log, st); err != nil {
			return err
		}
	}

	return nil
}

func (s *Syncer) getUniqueFilename(dir, title string, opts Options) string {
	base := sanitize.Filename(title)
	filename := base
	counter := 2

	for {
		path := filepath.Join(dir, filename+".md")
		_, err := s.fs.Stat(path)
		if os.IsNotExist(err) {
			return filename
		}
		filename = fmt.Sprintf("%s-%d", base, counter)
		counter++
	}
}

func (s *Syncer) downloadAttachment(ctx context.Context, att confluence.Attachment, dir string) error {
	rc, err := s.client.DownloadAttachment(ctx, att)
	if err != nil {
		return err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("reading attachment: %w", err)
	}

	path := filepath.Join(dir, att.Title)
	return s.fs.WriteFile(path, data, 0644)
}
