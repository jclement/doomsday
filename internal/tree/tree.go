// Package tree defines directory tree structures for snapshot metadata.
package tree

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jclement/doomsday/internal/types"
)

// NodeType identifies the type of a filesystem entry.
type NodeType string

const (
	NodeTypeFile    NodeType = "file"
	NodeTypeDir     NodeType = "dir"
	NodeTypeSymlink NodeType = "symlink"
	NodeTypeDev     NodeType = "dev"    // device node (metadata only)
	NodeTypeFIFO    NodeType = "fifo"   // named pipe (metadata only)
	NodeTypeSocket  NodeType = "socket" // socket (metadata only)
)

// Node represents a single filesystem entry within a tree.
type Node struct {
	Name          string            `json:"name"`
	Type          NodeType          `json:"type"`
	Mode          os.FileMode       `json:"mode"`
	Size          int64             `json:"size,omitempty"`
	UID           uint32            `json:"uid"`
	GID           uint32            `json:"gid"`
	User          string            `json:"user,omitempty"`
	Group         string            `json:"group,omitempty"`
	ModTime       time.Time         `json:"mtime"`
	AccessTime    time.Time         `json:"atime,omitempty"`
	ChangeTime    time.Time         `json:"ctime,omitempty"`
	Xattrs        map[string][]byte `json:"xattrs,omitempty"`
	SymlinkTarget string            `json:"symlink_target,omitempty"`
	DevMajor      uint32            `json:"dev_major,omitempty"`
	DevMinor      uint32            `json:"dev_minor,omitempty"`
	Inode         uint64            `json:"inode,omitempty"`
	Links         uint64            `json:"links,omitempty"`

	// For files: ordered list of content blob IDs
	Content []types.BlobID `json:"content,omitempty"`

	// For directories: the blob ID of the child tree
	Subtree types.BlobID `json:"subtree,omitempty"`
}

// Tree is an ordered list of nodes within a single directory.
type Tree struct {
	Nodes []Node `json:"nodes"`
}

// Marshal serializes a tree to JSON.
func Marshal(t *Tree) ([]byte, error) {
	data, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("tree.Marshal: %w", err)
	}
	return data, nil
}

// Unmarshal deserializes a tree from JSON.
func Unmarshal(data []byte) (*Tree, error) {
	var t Tree
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("tree.Unmarshal: %w", err)
	}
	return &t, nil
}

// Find returns the node with the given name, or nil if not found.
func (t *Tree) Find(name string) *Node {
	for i := range t.Nodes {
		if t.Nodes[i].Name == name {
			return &t.Nodes[i]
		}
	}
	return nil
}
