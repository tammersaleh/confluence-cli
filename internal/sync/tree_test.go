package sync

import (
	"testing"

	"github.com/tammersaleh/confluence-cli/internal/confluence"
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

func TestBuildTree_NonPageParentsIntegrate(t *testing.T) {
	// Database and folder nodes (with Type set) should integrate into the tree
	// just like regular pages, based on their ParentID.
	pages := []confluence.Page{
		{ID: "1", Title: "Root", ParentID: "", Type: "page"},
		{ID: "f1", Title: "Archive", ParentID: "1", Type: "folder", ParentType: "page"},
		{ID: "db1", Title: "Customers", ParentID: "f1", Type: "database", ParentType: "folder"},
		{ID: "2", Title: "Jane Street", ParentID: "db1", Type: "page", ParentType: "database"},
	}

	tree := BuildTree(pages)

	if len(tree) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree))
	}

	root := tree[0]
	if root.Page.ID != "1" {
		t.Errorf("root ID = %s, want 1", root.Page.ID)
	}

	// Root should have folder "f1" as child
	if len(root.Children) != 1 {
		t.Fatalf("root has %d children, want 1", len(root.Children))
	}
	folder := root.Children[0]
	if folder.Page.ID != "f1" {
		t.Errorf("folder ID = %s, want f1", folder.Page.ID)
	}

	// Folder should have database "db1" as child
	if len(folder.Children) != 1 {
		t.Fatalf("folder has %d children, want 1", len(folder.Children))
	}
	db := folder.Children[0]
	if db.Page.ID != "db1" {
		t.Errorf("database ID = %s, want db1", db.Page.ID)
	}

	// Database should have page "2" as child
	if len(db.Children) != 1 {
		t.Fatalf("database has %d children, want 1", len(db.Children))
	}
	if db.Children[0].Page.ID != "2" {
		t.Errorf("leaf ID = %s, want 2", db.Children[0].Page.ID)
	}
}

func TestBuildTree_DeterministicOrder(t *testing.T) {
	// BuildTree must return consistent ordering regardless of map iteration order.
	// Run multiple times to catch non-determinism (Go randomizes map iteration).
	pages := []confluence.Page{
		{ID: "1", Title: "Root A", ParentID: ""},
		{ID: "2", Title: "Root B", ParentID: ""},
		{ID: "3", Title: "Root C", ParentID: ""},
		{ID: "4", Title: "Child 1 of A", ParentID: "1"},
		{ID: "5", Title: "Child 2 of A", ParentID: "1"},
		{ID: "6", Title: "Child 3 of A", ParentID: "1"},
	}

	for i := 0; i < 100; i++ {
		tree := BuildTree(pages)

		if len(tree) != 3 {
			t.Fatalf("iteration %d: expected 3 roots, got %d", i, len(tree))
		}

		// Roots must be sorted by ID
		if tree[0].Page.ID != "1" || tree[1].Page.ID != "2" || tree[2].Page.ID != "3" {
			t.Errorf("iteration %d: roots not in ID order: got %s, %s, %s",
				i, tree[0].Page.ID, tree[1].Page.ID, tree[2].Page.ID)
		}

		// Children of root A must be sorted by ID
		rootA := tree[0]
		if len(rootA.Children) != 3 {
			t.Fatalf("iteration %d: Root A should have 3 children, got %d", i, len(rootA.Children))
		}
		if rootA.Children[0].Page.ID != "4" ||
			rootA.Children[1].Page.ID != "5" ||
			rootA.Children[2].Page.ID != "6" {
			t.Errorf("iteration %d: children not in ID order: got %s, %s, %s",
				i, rootA.Children[0].Page.ID, rootA.Children[1].Page.ID, rootA.Children[2].Page.ID)
		}
	}
}
