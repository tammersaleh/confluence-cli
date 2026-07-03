package sync

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/converter"
	"github.com/tammersaleh/confluence-sync/internal/filesystem"
	"github.com/tammersaleh/confluence-sync/pkg/sanitize"
)

type Options struct {
	Prune  bool
	DryRun bool
	Logger Logger
}

type Logger interface {
	Printf(format string, args ...interface{})
}

type noopLogger struct{}

func (noopLogger) Printf(string, ...interface{}) {}

type syncManifest struct {
	expectedPaths map[string]bool
	protectedDirs map[string]bool
}

type Syncer struct {
	client confluence.Client
	fs     filesystem.FileSystem
}

func New(client confluence.Client, fs filesystem.FileSystem) *Syncer {
	return &Syncer{client: client, fs: fs}
}

func (s *Syncer) Sync(ctx context.Context, spaceKey, outputDir string, opts Options) error {
	log := opts.Logger
	if log == nil {
		log = noopLogger{}
	}

	space, err := s.client.GetSpace(ctx, spaceKey)
	if err != nil {
		return fmt.Errorf("getting space: %w", err)
	}

	pages, err := s.client.GetPages(ctx, space.ID)
	if err != nil {
		return fmt.Errorf("getting pages: %w", err)
	}

	tree := BuildTree(pages)

	manifest := &syncManifest{
		expectedPaths: make(map[string]bool),
		protectedDirs: make(map[string]bool),
	}

	// All top-level items share usedNames to avoid filename collisions.
	// First root becomes index.md, its children and subsequent roots get unique names.
	topLevelUsedNames := make(map[string]bool)
	for i, root := range tree {
		if err := s.syncNode(ctx, root, outputDir, opts, log, topLevelUsedNames, i == 0, manifest); err != nil {
			return err
		}
	}

	if opts.Prune {
		if err := s.pruneStaleFiles(outputDir, manifest, log, opts.DryRun); err != nil {
			return fmt.Errorf("pruning stale files: %w", err)
		}
	}

	return nil
}

func isContainer(page confluence.Page) bool {
	return page.Type == "database" || page.Type == "folder"
}

func (s *Syncer) syncNode(ctx context.Context, node *PageNode, parentDir string, opts Options, log Logger, usedNames map[string]bool, isRoot bool, manifest *syncManifest) error {
	// Database and folder nodes are directory-only containers.
	// They create subdirectories but produce no markdown files.
	if isContainer(node.Page) {
		return s.syncContainerNode(ctx, node, parentDir, opts, log, usedNames, manifest)
	}

	content, err := s.client.GetPageContent(ctx, node.Page.ID)
	if err != nil {
		return fmt.Errorf("getting content for %s: %w", node.Page.Title, err)
	}

	attachments, err := s.client.GetAttachments(ctx, node.Page.ID)
	if err != nil {
		return fmt.Errorf("getting attachments for %s: %w", node.Page.Title, err)
	}

	hasAttachments := len(attachments) > 0
	hasChildren := node.HasChildren()

	var pageDir, mdPath string

	if isRoot {
		pageDir = parentDir
		mdPath = filepath.Join(parentDir, "index.md")
	} else if hasChildren || hasAttachments {
		dirName := sanitize.FilenameWithCollision(node.Page.Title, usedNames)
		usedNames[dirName] = true
		pageDir = filepath.Join(parentDir, dirName)
		mdPath = filepath.Join(pageDir, "index.md")
		manifest.expectedPaths[pageDir] = true
	} else {
		pageDir = parentDir
		filename := sanitize.FilenameWithCollision(node.Page.Title, usedNames)
		usedNames[filename] = true
		mdPath = filepath.Join(parentDir, filename+".md")
	}

	manifest.expectedPaths[mdPath] = true

	// Check if local file already has the same version
	if existing, err := s.fs.ReadFile(mdPath); err == nil {
		if localVersion, ok := extractVersion(existing); ok && localVersion == content.Version {
			// Version matches, skip this page but still process children.
			// Protect _attachments/ dir if it exists on disk since we didn't
			// re-fetch the attachment list.
			attDir := filepath.Join(pageDir, "_attachments")
			if _, err := s.fs.Stat(attDir); err == nil {
				manifest.expectedPaths[attDir] = true
				manifest.protectedDirs[attDir] = true
			}

			childUsedNames := usedNames
			if !isRoot {
				childUsedNames = make(map[string]bool)
			}
			for _, child := range node.Children {
				if err := s.syncNode(ctx, child, pageDir, opts, log, childUsedNames, false, manifest); err != nil {
					return err
				}
			}
			return nil
		}
	}

	log.Printf("%s", content.WebURL)

	attachmentPath := ""
	if hasAttachments {
		attachmentPath = "_attachments"
	}
	markdown := converter.ConvertWithOptions(content.Body, converter.Options{
		AttachmentPath: attachmentPath,
	})

	// Prepend frontmatter
	frontmatter := buildFrontmatter(content)
	markdown = frontmatter + markdown

	if !opts.DryRun {
		if err := s.fs.MkdirAll(filepath.Dir(mdPath), 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
		if err := s.fs.WriteFile(mdPath, []byte(markdown), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", mdPath, err)
		}
	}

	if hasAttachments {
		attDir := filepath.Join(pageDir, "_attachments")
		manifest.expectedPaths[attDir] = true
		for _, att := range attachments {
			manifest.expectedPaths[filepath.Join(attDir, att.Title)] = true
		}

		if !opts.DryRun {
			if err := s.fs.MkdirAll(attDir, 0755); err != nil {
				return fmt.Errorf("creating attachments directory: %w", err)
			}

			for _, att := range attachments {
				if err := s.downloadAttachment(ctx, att, attDir); err != nil {
					log.Printf("warning: skipping attachment %s: %v", att.Title, err)
				}
			}
		}
	}

	// If this is the root page, children share usedNames with sibling roots.
	// Otherwise, children get their own namespace.
	childUsedNames := usedNames
	if !isRoot {
		childUsedNames = make(map[string]bool)
	}
	for _, child := range node.Children {
		if err := s.syncNode(ctx, child, pageDir, opts, log, childUsedNames, false, manifest); err != nil {
			return err
		}
	}

	return nil
}

func (s *Syncer) syncContainerNode(ctx context.Context, node *PageNode, parentDir string, opts Options, log Logger, usedNames map[string]bool, manifest *syncManifest) error {
	dirName := sanitize.FilenameWithCollision(node.Page.Title, usedNames)
	usedNames[dirName] = true
	containerDir := filepath.Join(parentDir, dirName)

	manifest.expectedPaths[containerDir] = true

	if !opts.DryRun {
		if err := s.fs.MkdirAll(containerDir, 0755); err != nil {
			return fmt.Errorf("creating container directory: %w", err)
		}
	}

	childUsedNames := make(map[string]bool)
	for _, child := range node.Children {
		if err := s.syncNode(ctx, child, containerDir, opts, log, childUsedNames, false, manifest); err != nil {
			return err
		}
	}

	return nil
}

func (s *Syncer) downloadAttachment(ctx context.Context, att confluence.Attachment, dir string) error {
	rc, err := s.client.DownloadAttachment(ctx, att)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("reading attachment: %w", err)
	}

	return s.fs.WriteFile(filepath.Join(dir, att.Title), data, 0644)
}

func (s *Syncer) pruneStaleFiles(dir string, manifest *syncManifest, log Logger, dryRun bool) error {
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			if manifest.protectedDirs[fullPath] {
				continue
			}
			if manifest.expectedPaths[fullPath] {
				if err := s.pruneStaleFiles(fullPath, manifest, log, dryRun); err != nil {
					return err
				}
				continue
			}
			log.Printf("prune: %s", fullPath)
			if !dryRun {
				if err := s.fs.RemoveAll(fullPath); err != nil {
					return fmt.Errorf("removing %s: %w", fullPath, err)
				}
			}
			continue
		}

		if !manifest.expectedPaths[fullPath] {
			log.Printf("prune: %s", fullPath)
			if !dryRun {
				if err := s.fs.RemoveAll(fullPath); err != nil {
					return fmt.Errorf("removing %s: %w", fullPath, err)
				}
			}
		}
	}

	return nil
}

var versionRe = regexp.MustCompile(`(?m)^version:\s*(\d+)`)

func extractVersion(data []byte) (int, bool) {
	match := versionRe.FindSubmatch(data)
	if match == nil {
		return 0, false
	}
	v, err := strconv.Atoi(string(match[1]))
	if err != nil {
		return 0, false
	}
	return v, true
}

func buildFrontmatter(content *confluence.PageContent) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "confluence_page_id: %q\n", content.ID)
	fmt.Fprintf(&b, "title: %q\n", content.Title)
	if content.Author != "" {
		fmt.Fprintf(&b, "author: %q\n", content.Author)
	} else if content.AuthorID != "" {
		fmt.Fprintf(&b, "author_id: %q\n", content.AuthorID)
	}
	if content.WebURL != "" {
		fmt.Fprintf(&b, "confluence_url: %q\n", content.WebURL)
	}
	fmt.Fprintf(&b, "version: %d\n", content.Version)
	if content.CreatedAt != "" {
		fmt.Fprintf(&b, "created_at: %q\n", content.CreatedAt)
	}
	if content.ModifiedAt != "" {
		fmt.Fprintf(&b, "modified_at: %q\n", content.ModifiedAt)
	}
	b.WriteString("---\n\n")
	return b.String()
}
