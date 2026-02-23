package sync

import (
	"slices"

	"github.com/tammersaleh/confluence-sync/internal/confluence"
)

type PageNode struct {
	Page     confluence.Page
	Children []*PageNode
}

func (n *PageNode) HasChildren() bool {
	return len(n.Children) > 0
}

// BuildTree converts a flat list of pages into a tree structure.
// Pages with missing parents are treated as root nodes.
func BuildTree(pages []confluence.Page) []*PageNode {
	// Index pages by ID
	nodeMap := make(map[string]*PageNode, len(pages))
	for _, p := range pages {
		nodeMap[p.ID] = &PageNode{Page: p}
	}

	// Build parent-child relationships
	var roots []*PageNode
	for _, node := range nodeMap {
		if node.Page.ParentID == "" {
			roots = append(roots, node)
		} else if parent, ok := nodeMap[node.Page.ParentID]; ok {
			parent.Children = append(parent.Children, node)
		} else {
			// Orphaned page (parent not in this space) - treat as root
			roots = append(roots, node)
		}
	}

	// Sort for deterministic ordering (map iteration order is random)
	sortNodes(roots)

	return roots
}

func sortNodes(nodes []*PageNode) {
	slices.SortFunc(nodes, func(a, b *PageNode) int {
		if a.Page.ID < b.Page.ID {
			return -1
		}
		if a.Page.ID > b.Page.ID {
			return 1
		}
		return 0
	})
	for _, n := range nodes {
		sortNodes(n.Children)
	}
}
