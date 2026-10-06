package archiver

import (
	"context"
	"fmt"
	"sync"
	"time"

	mega "github.com/t3rm1n4l/go-mega"
)

// Uploader stores one local file of localSize bytes in remote storage.
// dir is the folder path under the storage root, created if missing. It
// returns the size the remote reports, which the caller compares with
// localSize before any local data is deleted.
type Uploader interface {
	Upload(ctx context.Context, localPath string, dir []string, name string, localSize int64) (remoteSize int64, err error)
}

// megaUploader uploads to MEGA via go-mega. It logs in lazily and once:
// go-mega keeps its session and filesystem tree in memory.
type megaUploader struct {
	email, password string

	mu sync.Mutex
	m  *mega.Mega
}

func newMegaUploader(email, password string) *megaUploader {
	return &megaUploader{email: email, password: password}
}

func (u *megaUploader) client() (*mega.Mega, error) {
	if u.m != nil {
		return u.m, nil
	}
	m := mega.New()
	m.SetRetries(5)
	m.SetTimeOut(2 * time.Minute)
	if err := m.Login(u.email, u.password); err != nil {
		return nil, fmt.Errorf("mega login: %w", err)
	}
	u.m = m
	return m, nil
}

// Upload is idempotent per name: when a file of that name and the same
// size already sits in dir (an earlier run uploaded it, then failed before
// recording it), the existing file is reported instead of uploading again.
// go-mega has no context support; ctx is checked before starting.
func (u *megaUploader) Upload(ctx context.Context, localPath string, dir []string, name string, localSize int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	m, err := u.client()
	if err != nil {
		return 0, err
	}
	parent, err := ensureDir(m, dir)
	if err != nil {
		u.m = nil // force a fresh login and tree next time
		return 0, err
	}
	children, err := m.FS.GetChildren(parent)
	if err != nil {
		u.m = nil
		return 0, fmt.Errorf("mega list %v: %w", dir, err)
	}
	for _, c := range children {
		if c.GetType() == mega.FILE && c.GetName() == name && c.GetSize() == localSize {
			return c.GetSize(), nil
		}
	}
	node, err := m.UploadFile(localPath, parent, name, nil)
	if err != nil {
		u.m = nil
		return 0, fmt.Errorf("mega upload %s: %w", name, err)
	}
	return node.GetSize(), nil
}

// ensureDir walks dir from the MEGA root, creating missing folders.
func ensureDir(m *mega.Mega, dir []string) (*mega.Node, error) {
	node := m.FS.GetRoot()
	for _, name := range dir {
		children, err := m.FS.GetChildren(node)
		if err != nil {
			return nil, fmt.Errorf("mega list %s: %w", node.GetName(), err)
		}
		var next *mega.Node
		for _, c := range children {
			if c.GetType() == mega.FOLDER && c.GetName() == name {
				next = c
				break
			}
		}
		if next == nil {
			if next, err = m.CreateDir(name, node); err != nil {
				return nil, fmt.Errorf("mega mkdir %s: %w", name, err)
			}
		}
		node = next
	}
	return node, nil
}
