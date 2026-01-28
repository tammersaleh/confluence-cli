package sync

import (
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/confluence"
)

func TestBuildTree(t *testing.T) {
	pages := []confluence.Page{
		{ID: "1", Title: "Root", ParentID: ""},
		{ID: "2", Title: "Child A", ParentID: "1"},
		{ID: "3", Title: "Child B", ParentID: "1"},
		{ID: "4", Title: "Grandchild", ParentID: "2"},
	}

	tree := BuildTree(pages)

	if len(tree) != 1 {
		t.Fatalf("expected 1 root node, got %d", len(tree))
	}

	root := tree[0]
	if root.Page.ID != "1" {
		t.Errorf("root ID = %s, want 1", root.Page.ID)
	}
	if len(root.Children) != 2 {
		t.Errorf("root has %d children, want 2", len(root.Children))
	}

	// Find Child A
	var childA *PageNode
	for _, c := range root.Children {
		if c.Page.ID == "2" {
			childA = c
			break
		}
	}
	if childA == nil {
		t.Fatal("Child A not found")
	}
	if len(childA.Children) != 1 {
		t.Errorf("Child A has %d children, want 1", len(childA.Children))
	}
	if childA.Children[0].Page.ID != "4" {
		t.Errorf("grandchild ID = %s, want 4", childA.Children[0].Page.ID)
	}
}

func TestBuildTree_MultipleRoots(t *testing.T) {
	// Confluence spaces can have multiple root pages
	pages := []confluence.Page{
		{ID: "1", Title: "Root A", ParentID: ""},
		{ID: "2", Title: "Root B", ParentID: ""},
		{ID: "3", Title: "Child of A", ParentID: "1"},
	}

	tree := BuildTree(pages)

	if len(tree) != 2 {
		t.Fatalf("expected 2 root nodes, got %d", len(tree))
	}
}

func TestBuildTree_OrphanedPages(t *testing.T) {
	// Pages with non-existent parents should become roots
	pages := []confluence.Page{
		{ID: "1", Title: "Normal", ParentID: ""},
		{ID: "2", Title: "Orphan", ParentID: "999"},
	}

	tree := BuildTree(pages)

	if len(tree) != 2 {
		t.Fatalf("expected 2 root nodes (including orphan), got %d", len(tree))
	}
}

func TestPageNode_HasChildren(t *testing.T) {
	node := &PageNode{
		Page:     confluence.Page{ID: "1"},
		Children: []*PageNode{{Page: confluence.Page{ID: "2"}}},
	}

	if !node.HasChildren() {
		t.Error("HasChildren() = false, want true")
	}

	leaf := &PageNode{Page: confluence.Page{ID: "3"}}
	if leaf.HasChildren() {
		t.Error("leaf HasChildren() = true, want false")
	}
}
