package tree

import (
	"os"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/types"
)

func TestMarshalUnmarshal(t *testing.T) {
	now := time.Now().Truncate(time.Second) // JSON loses nanoseconds

	tree := &Tree{
		Nodes: []Node{
			{
				Name:    "readme.md",
				Type:    NodeTypeFile,
				Mode:    0644,
				Size:    1234,
				UID:     1000,
				GID:     1000,
				User:    "jsc",
				Group:   "staff",
				ModTime: now,
				Content: []types.BlobID{{0x01}, {0x02}},
			},
			{
				Name:    "src",
				Type:    NodeTypeDir,
				Mode:    os.ModeDir | 0755,
				UID:     1000,
				GID:     1000,
				ModTime: now,
				Subtree: types.BlobID{0xAA, 0xBB},
			},
			{
				Name:          "link",
				Type:          NodeTypeSymlink,
				Mode:          os.ModeSymlink | 0777,
				ModTime:       now,
				SymlinkTarget: "../readme.md",
			},
		},
	}

	data, err := Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}

	tree2, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(tree2.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(tree2.Nodes))
	}

	// Check file
	file := tree2.Nodes[0]
	if file.Name != "readme.md" {
		t.Errorf("name = %q", file.Name)
	}
	if file.Size != 1234 {
		t.Errorf("size = %d", file.Size)
	}
	if len(file.Content) != 2 {
		t.Errorf("content blobs = %d", len(file.Content))
	}
	if file.User != "jsc" {
		t.Errorf("user = %q", file.User)
	}

	// Check dir
	dir := tree2.Nodes[1]
	if dir.Type != NodeTypeDir {
		t.Errorf("type = %q", dir.Type)
	}
	if dir.Subtree.IsZero() {
		t.Error("subtree should not be zero")
	}

	// Check symlink
	link := tree2.Nodes[2]
	if link.SymlinkTarget != "../readme.md" {
		t.Errorf("symlink target = %q", link.SymlinkTarget)
	}
}

func TestFind(t *testing.T) {
	tree := &Tree{
		Nodes: []Node{
			{Name: "a.txt"},
			{Name: "b.txt"},
			{Name: "c.txt"},
		},
	}

	if n := tree.Find("b.txt"); n == nil {
		t.Error("should find b.txt")
	} else if n.Name != "b.txt" {
		t.Errorf("found %q", n.Name)
	}

	if n := tree.Find("d.txt"); n != nil {
		t.Error("should not find d.txt")
	}
}

func TestXattrs(t *testing.T) {
	tree := &Tree{
		Nodes: []Node{
			{
				Name: "with-xattrs",
				Type: NodeTypeFile,
				Xattrs: map[string][]byte{
					"user.comment": []byte("doomsday backup"),
				},
			},
		},
	}

	data, err := Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}

	tree2, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}

	xattr := tree2.Nodes[0].Xattrs["user.comment"]
	if string(xattr) != "doomsday backup" {
		t.Errorf("xattr = %q", string(xattr))
	}
}

func TestEmptyTree(t *testing.T) {
	tree := &Tree{Nodes: []Node{}}
	data, err := Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	tree2, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree2.Nodes) != 0 {
		t.Errorf("expected empty tree, got %d nodes", len(tree2.Nodes))
	}
}

func FuzzTreeUnmarshal(f *testing.F) {
	tree := &Tree{Nodes: []Node{{Name: "test", Type: NodeTypeFile}}}
	seed, _ := Marshal(tree)
	f.Add(seed)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Should never panic
		Unmarshal(data)
	})
}
