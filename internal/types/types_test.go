package types

import (
	"testing"
)

func TestBlobID_String(t *testing.T) {
	var id BlobID
	for i := range id {
		id[i] = byte(i)
	}
	got := id.String()
	want := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	if got != want {
		t.Errorf("BlobID.String() = %q, want %q", got, want)
	}
}

func TestBlobID_Short(t *testing.T) {
	var id BlobID
	id[0] = 0xde
	id[1] = 0xad
	id[2] = 0xbe
	id[3] = 0xef
	if got := id.Short(); got != "deadbeef" {
		t.Errorf("BlobID.Short() = %q, want %q", got, "deadbeef")
	}
}

func TestBlobID_IsZero(t *testing.T) {
	var zero BlobID
	if !zero.IsZero() {
		t.Error("zero BlobID should be zero")
	}
	zero[0] = 1
	if zero.IsZero() {
		t.Error("non-zero BlobID should not be zero")
	}
}

func TestParseBlobID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", false},
		{"too short", "0001020304", true},
		{"invalid hex", "zzzz", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBlobID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseBlobID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestParseBlobID_Roundtrip(t *testing.T) {
	var original BlobID
	for i := range original {
		original[i] = byte(i * 7)
	}
	parsed, err := ParseBlobID(original.String())
	if err != nil {
		t.Fatalf("ParseBlobID roundtrip: %v", err)
	}
	if parsed != original {
		t.Errorf("roundtrip failed: got %s, want %s", parsed, original)
	}
}

func TestFileType_String(t *testing.T) {
	tests := []struct {
		ft   FileType
		want string
	}{
		{FileTypePack, "data"},
		{FileTypeIndex, "index"},
		{FileTypeSnapshot, "snapshots"},
		{FileTypeKey, "keys"},
		{FileTypeConfig, "config"},
		{FileTypeLock, "locks"},
		{FileType(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.ft.String(); got != tt.want {
				t.Errorf("FileType(%d).String() = %q, want %q", tt.ft, got, tt.want)
			}
		})
	}
}

func TestBlobType_String(t *testing.T) {
	if got := BlobTypeData.String(); got != "data" {
		t.Errorf("BlobTypeData.String() = %q", got)
	}
	if got := BlobTypeTree.String(); got != "tree" {
		t.Errorf("BlobTypeTree.String() = %q", got)
	}
}
