package restore

import (
	"testing"
)

// TestAudit_ValidateRestorePath_RootTarget verifies that restoring to "/"
// allows all paths (root target means we're restoring to original locations).
func TestAudit_ValidateRestorePath_RootTarget(t *testing.T) {
	tests := []struct {
		name      string
		finalPath string
	}{
		{"etc_passwd", "/etc/passwd"},
		{"home_user_file", "/home/user/file.txt"},
		{"just_root", "/"},
		{"deep_path", "/a/b/c/d/e/f/g.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRestorePath("/", tc.finalPath)
			if err != nil {
				t.Errorf("validateRestorePath(\"/\", %q) = %v, want nil", tc.finalPath, err)
			}
		})
	}
}

// TestAudit_ValidateRestorePath_PrefixAttack verifies that a target that is
// a prefix of another directory is correctly rejected (e.g. /tmp/restore vs
// /tmp/restore-evil).
func TestAudit_ValidateRestorePath_PrefixAttack(t *testing.T) {
	err := validateRestorePath("/tmp/restore", "/tmp/restore-evil/file.txt")
	if err == nil {
		t.Error("expected error for prefix attack path")
	}
}

// TestAudit_ValidateNodeName_Table is a comprehensive table-driven test for
// node name validation, testing edge cases.
func TestAudit_ValidateNodeName_Table(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty", "", true},
		{"dot", ".", true},
		{"dotdot", "..", true},
		{"forward_slash", "a/b", true},
		{"backslash", "a\\b", true},
		{"null_byte", "a\x00b", true},
		{"normal_name", "normal", false},
		{"hidden_file", ".hidden", false},
		{"triple_dot", "...", false},
		{"file_with_ext", "file.txt", false},
		{"spaces", "file name.txt", false},
		{"unicode", "日本語.txt", false},
		{"emoji", "🎉.txt", false},
		{"dash_underscore", "my-file_v2.txt", false},
		{"starts_with_dot", ".gitignore", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNodeName(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("validateNodeName(%q) = nil, want error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateNodeName(%q) = %v, want nil", tc.input, err)
			}
		})
	}
}

// TestAudit_PathIncluded_DeepNesting verifies that pathIncluded correctly
// handles deeply nested paths and prefixes.
func TestAudit_PathIncluded_DeepNesting(t *testing.T) {
	tests := []struct {
		name     string
		relPath  string
		includes []string
		want     bool
	}{
		{
			name:     "deep child of shallow prefix",
			relPath:  "a/b/c/d/e/f/g.txt",
			includes: []string{"a"},
			want:     true,
		},
		{
			name:     "shallow ancestor of deep prefix",
			relPath:  "a",
			includes: []string{"a/b/c/d/e/f/g.txt"},
			want:     true,
		},
		{
			name:     "partial name match not included",
			relPath:  "abc",
			includes: []string{"ab"},
			want:     false,
		},
		{
			name:     "sibling not included",
			relPath:  "a/x",
			includes: []string{"a/y"},
			want:     false,
		},
		{
			name:     "exact match",
			relPath:  "a/b/c",
			includes: []string{"a/b/c"},
			want:     true,
		},
		{
			name:     "multiple includes, one matches",
			relPath:  "docs/readme.md",
			includes: []string{"src", "docs"},
			want:     true,
		},
		{
			name:     "empty includes means no match (caller checks len first)",
			relPath:  "anything",
			includes: []string{},
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pathIncluded(tc.relPath, tc.includes)
			if got != tc.want {
				t.Errorf("pathIncluded(%q, %v) = %v, want %v",
					tc.relPath, tc.includes, got, tc.want)
			}
		})
	}
}
