package sync

import (
	"context"
	"fmt"
	"io"
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


	if opts.Clean && !opts.DryRun {
		if err := s.fs.RemoveAll(outputDir); err != nil {
			return fmt.Errorf("cleaning output directory: %w", err)
		}
	}

	pages, err := s.client.GetPages(ctx, space.ID)
	if err != nil {
		return fmt.Errorf("getting pages: %w", err)
	}

	tree := BuildTree(pages)

	st := &stats{}
	for _, root := range tree {
		if err := s.syncNode(ctx, root, outputDir, opts, log, st, nil); err != nil {
			return err
		}
	}

	return nil
}

func (s *Syncer) syncNode(ctx context.Context, node *PageNode, parentDir string, opts Options, log Logger, st *stats, usedNames map[string]bool) error {
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
	isRoot := usedNames == nil

	var pageDir, mdPath string

	if isRoot {
		pageDir = parentDir
		mdPath = filepath.Join(parentDir, "index.md")
	} else if hasChildren || hasAttachments {
		dirName := sanitize.FilenameWithCollision(node.Page.Title, usedNames)
		usedNames[dirName] = true
		pageDir = filepath.Join(parentDir, dirName)
		mdPath = filepath.Join(pageDir, "index.md")
	} else {
		pageDir = parentDir
		filename := sanitize.FilenameWithCollision(node.Page.Title, usedNames)
		usedNames[filename] = true
		mdPath = filepath.Join(parentDir, filename+".md")
	}

	// Check if local file already has the same version
	if existing, err := s.fs.ReadFile(mdPath); err == nil {
		if localVersion, ok := extractVersion(existing); ok && localVersion == content.Version {
			// Version matches, skip this page but still process children
			childUsedNames := make(map[string]bool)
			for _, child := range node.Children {
				if err := s.syncNode(ctx, child, pageDir, opts, log, st, childUsedNames); err != nil {
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
	st.pages++

	if hasAttachments && !opts.DryRun {
		attDir := filepath.Join(pageDir, "_attachments")
		if err := s.fs.MkdirAll(attDir, 0755); err != nil {
			return fmt.Errorf("creating attachments directory: %w", err)
		}

		for _, att := range attachments {
			if err := s.downloadAttachment(ctx, att, attDir); err != nil {
				return fmt.Errorf("page %s: attachment %s: %w", content.WebURL, att.Title, err)
			}
			st.attachments++
		}
	}

	childUsedNames := make(map[string]bool)
	for _, child := range node.Children {
		if err := s.syncNode(ctx, child, pageDir, opts, log, st, childUsedNames); err != nil {
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
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("reading attachment: %w", err)
	}

	return s.fs.WriteFile(filepath.Join(dir, att.Title), data, 0644)
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
	b.WriteString(fmt.Sprintf("confluence_page_id: %q\n", content.ID))
	b.WriteString(fmt.Sprintf("title: %q\n", content.Title))
	if content.Author != "" {
		b.WriteString(fmt.Sprintf("author: %q\n", content.Author))
	} else if content.AuthorID != "" {
		b.WriteString(fmt.Sprintf("author_id: %q\n", content.AuthorID))
	}
	if content.WebURL != "" {
		b.WriteString(fmt.Sprintf("confluence_url: %q\n", content.WebURL))
	}
	b.WriteString(fmt.Sprintf("version: %d\n", content.Version))
	if content.CreatedAt != "" {
		b.WriteString(fmt.Sprintf("created_at: %q\n", content.CreatedAt))
	}
	if content.ModifiedAt != "" {
		b.WriteString(fmt.Sprintf("modified_at: %q\n", content.ModifiedAt))
	}
	b.WriteString("---\n\n")
	return b.String()
}
