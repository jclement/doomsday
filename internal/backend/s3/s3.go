// Package s3 implements a Backend backed by S3-compatible object storage.
//
// It works with AWS S3, Backblaze B2 (S3-compatible endpoint), MinIO,
// Cloudflare R2, Wasabi, and any other S3-compatible store.
package s3

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jclement/doomsday/internal/types"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Backend stores repository data in an S3-compatible object store.
type Backend struct {
	client   *minio.Client
	bucket   string
	prefix   string
	endpoint string
}

// Ensure Backend implements types.Backend at compile time.
var _ types.Backend = (*Backend)(nil)

// New creates an S3 backend targeting the given bucket and prefix.
// It verifies the bucket exists before returning.
func New(endpoint, bucket, prefix, keyID, secretKey string, useSSL bool) (*Backend, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(keyID, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("s3.New: create client: %w", err)
	}

	// Verify the bucket exists.
	exists, err := client.BucketExists(context.Background(), bucket)
	if err != nil {
		return nil, fmt.Errorf("s3.New: check bucket %q: %w", bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("s3.New: bucket %q does not exist", bucket)
	}

	// Normalize prefix: strip leading/trailing slashes.
	prefix = strings.Trim(prefix, "/")

	return &Backend{
		client:   client,
		bucket:   bucket,
		prefix:   prefix,
		endpoint: endpoint,
	}, nil
}

func (b *Backend) Location() string {
	if b.prefix == "" {
		return fmt.Sprintf("s3:%s/%s", b.endpoint, b.bucket)
	}
	return fmt.Sprintf("s3:%s/%s/%s", b.endpoint, b.bucket, b.prefix)
}

func (b *Backend) Save(ctx context.Context, t types.FileType, name string, rd io.Reader) error {
	if err := types.ValidateName(name); err != nil {
		return fmt.Errorf("s3.Save: %w", err)
	}
	key := b.key(t, name)
	_, err := b.client.PutObject(ctx, b.bucket, key, rd, -1, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("s3.Save: %w", err)
	}
	return nil
}

func (b *Backend) Load(ctx context.Context, t types.FileType, name string, offset, length int64) (io.ReadCloser, error) {
	if err := types.ValidateName(name); err != nil {
		return nil, fmt.Errorf("s3.Load: %w", err)
	}
	key := b.key(t, name)

	opts := minio.GetObjectOptions{}
	if offset > 0 || length > 0 {
		// SetRange sets the HTTP Range header.
		// When length is 0 (but offset > 0), we want from offset to end of object.
		if length > 0 {
			err := opts.SetRange(offset, offset+length-1)
			if err != nil {
				return nil, fmt.Errorf("s3.Load: set range: %w", err)
			}
		} else {
			// offset > 0, length == 0: read from offset to end.
			// SetRange with end=-1 is not supported; use a large end value.
			// minio-go handles this correctly: if start is set, omitting end
			// means "to end of object" when we use a raw range header.
			err := opts.SetRange(offset, 0)
			if err != nil {
				return nil, fmt.Errorf("s3.Load: set range: %w", err)
			}
		}
	}

	obj, err := b.client.GetObject(ctx, b.bucket, key, opts)
	if err != nil {
		return nil, fmt.Errorf("s3.Load: %w", err)
	}

	// GetObject is lazy — the first Read or Stat triggers the actual HTTP request.
	// We do a Stat here to detect not-found errors eagerly.
	_, err = obj.Stat()
	if err != nil {
		obj.Close()
		if isNotFound(err) {
			return nil, fmt.Errorf("s3.Load: %w", types.ErrNotFound)
		}
		return nil, fmt.Errorf("s3.Load: %w", err)
	}

	return obj, nil
}

func (b *Backend) Stat(ctx context.Context, t types.FileType, name string) (types.FileInfo, error) {
	if err := types.ValidateName(name); err != nil {
		return types.FileInfo{}, fmt.Errorf("s3.Stat: %w", err)
	}
	key := b.key(t, name)

	info, err := b.client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return types.FileInfo{}, fmt.Errorf("s3.Stat: %w", types.ErrNotFound)
		}
		return types.FileInfo{}, fmt.Errorf("s3.Stat: %w", err)
	}

	return types.FileInfo{Name: name, Size: info.Size}, nil
}

func (b *Backend) Remove(ctx context.Context, t types.FileType, name string) error {
	if err := types.ValidateName(name); err != nil {
		return fmt.Errorf("s3.Remove: %w", err)
	}
	key := b.key(t, name)

	// S3 RemoveObject is already idempotent: removing a non-existent key
	// does not return an error.
	err := b.client.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("s3.Remove: %w", err)
	}
	return nil
}

func (b *Backend) List(ctx context.Context, t types.FileType, fn func(types.FileInfo) error) error {
	if t == types.FileTypeConfig {
		// Config is a single object, not a prefix. Check if it exists.
		key := b.key(t, "")
		info, err := b.client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return fmt.Errorf("s3.List: %w", err)
		}
		return fn(types.FileInfo{Name: "config", Size: info.Size})
	}

	prefix := b.dir(t)

	// Collect all objects under the prefix, then sort for deterministic ordering.
	var items []types.FileInfo

	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	for obj := range b.client.ListObjects(ctx, b.bucket, opts) {
		if obj.Err != nil {
			return fmt.Errorf("s3.List: %w", obj.Err)
		}

		// Extract the name relative to the file type directory.
		// For packs: prefix is "<prefix>/data/", key might be "<prefix>/data/ab/abcdef..."
		//   -> strip prefix dir to get plain name "abcdef..." for consistency
		//      with Save/Load which accept plain names.
		// For others: prefix is "<prefix>/snapshots/", key might be "<prefix>/snapshots/snap-001"
		//   -> name = "snap-001"
		name := strings.TrimPrefix(obj.Key, prefix)
		if name == "" || name == "/" {
			continue
		}
		// Strip hex prefix directory from pack names (e.g., "ab/abcdef..." -> "abcdef...")
		if t == types.FileTypePack {
			if idx := strings.LastIndex(name, "/"); idx >= 0 {
				name = name[idx+1:]
			}
		}

		items = append(items, types.FileInfo{
			Name: name,
			Size: obj.Size,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})

	for _, item := range items {
		if err := fn(item); err != nil {
			return err
		}
	}

	return nil
}

func (b *Backend) Close() error {
	// S3 is stateless HTTP — nothing to close.
	return nil
}

// key returns the full S3 object key for a file type and name.
func (b *Backend) key(t types.FileType, name string) string {
	if t == types.FileTypePack && len(name) >= 2 {
		// Pack files use 2-char hex prefix subdirectories: data/<hex2>/<name>
		return b.join(t.String(), name[:2], name)
	}
	if t == types.FileTypeConfig {
		// Config is a single file at the prefix root.
		return b.join("config")
	}
	return b.join(t.String(), name)
}

// dir returns the S3 prefix for listing files of a given type.
// The returned string always ends with "/" so ListObjects works correctly.
func (b *Backend) dir(t types.FileType) string {
	if t == types.FileTypeConfig {
		// Config is a single file, not a directory. For listing, we use the
		// prefix up to "config" — but config is not typically listed.
		return b.join("config")
	}
	return b.join(t.String()) + "/"
}

// join constructs an S3 key by joining the prefix with path segments using "/".
func (b *Backend) join(parts ...string) string {
	if b.prefix == "" {
		return strings.Join(parts, "/")
	}
	return b.prefix + "/" + strings.Join(parts, "/")
}

// isNotFound returns true if the error indicates a 404 / NoSuchKey response.
func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.StatusCode == 404 || resp.Code == "NoSuchKey"
}
