// Package doors reads what the challenges repository uploaded to the bucket: each door's bundle and page.
// It knows nothing about opening times; whether a door may be served is decided by the
// caller, on every request, with the schedule.
package doors

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"cloud.google.com/go/storage"
)

// ErrNotFound means nothing was uploaded under that name.
var ErrNotFound = errors.New("not uploaded")

// Storage hands out uploaded files by name, like "doors/01/page.yaml". The bucket in
// production, a folder locally.
type Storage interface {
	Open(ctx context.Context, name string) (io.ReadCloser, error)
}

// PageName and BundleName are where the upload puts a door's files.
func PageName(door int) string   { return fmt.Sprintf("doors/%02d/page.yaml", door) }
func BundleName(door int) string { return fmt.Sprintf("doors/%02d/bundle.tar.gz", door) }

// Dir reads files from a local folder laid out like the bucket. `devbox run api` points it
// at the challenges repository's build/, which `make bundle` fills.
type Dir string

func (d Dir) Open(_ context.Context, name string) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(string(d), filepath.FromSlash(name)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Bucket reads files from a GCS bucket, as the service's own identity.
type Bucket struct {
	handle *storage.BucketHandle
}

// NewBucket connects to the bucket. The client is meant to live as long as the process.
func NewBucket(ctx context.Context, name string) (*Bucket, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &Bucket{handle: client.Bucket(name)}, nil
}

func (b *Bucket) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	r, err := b.handle.Object(name).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, ErrNotFound
	}
	return r, err
}
