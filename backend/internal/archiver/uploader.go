package archiver

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
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

// RemoteFile is one entry of a remote folder.
type RemoteFile struct {
	Name  string
	Size  int64
	IsDir bool
}

// Downloader reads back what Uploader stored. dir is a folder path under the
// storage root; a missing folder lists as empty, not as an error.
type Downloader interface {
	List(ctx context.Context, dir []string) ([]RemoteFile, error)
	Download(ctx context.Context, dir []string, name, localPath string) error
}

// megaStore uploads to and downloads from MEGA via go-mega. It logs in lazily and once:
// go-mega keeps its session and filesystem tree in memory.
type megaStore struct {
	email, password string

	mu sync.Mutex
	m  *mega.Mega
}

func newMegaStore(email, password string) *megaStore {
	return &megaStore{email: email, password: password}
}

func (u *megaStore) client() (*mega.Mega, error) {
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
func (u *megaStore) Upload(ctx context.Context, localPath string, dir []string, name string, localSize int64) (int64, error) {
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
	var stale []*mega.Node
	for _, c := range children {
		if c.GetType() != mega.FILE || c.GetName() != name {
			continue
		}
		if c.GetSize() == localSize {
			return c.GetSize(), nil
		}
		stale = append(stale, c)
	}
	node, err := m.UploadFile(localPath, parent, name, nil)
	if err != nil {
		u.m = nil
		return 0, fmt.Errorf("mega upload %s: %w", name, err)
	}
	// A same-name file of another size is an older upload of this name
	// (a whole-journal snapshot from before per-day export). Move it to
	// the rubbish bin so the folder keeps one file per name.
	for _, c := range stale {
		if err := m.Delete(c, false); err != nil {
			log.Printf("[archiver] could not remove old %s/%s: %v", strings.Join(dir, "/"), name, err)
		}
	}
	return node.GetSize(), nil
}

// List returns the entries of dir, or none when dir does not exist.
func (u *megaStore) List(ctx context.Context, dir []string) ([]RemoteFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	m, err := u.client()
	if err != nil {
		return nil, err
	}
	node, err := findDir(m, dir)
	if err != nil || node == nil {
		return nil, err
	}
	children, err := m.FS.GetChildren(node)
	if err != nil {
		return nil, fmt.Errorf("mega list %v: %w", dir, err)
	}
	out := make([]RemoteFile, 0, len(children))
	for _, c := range children {
		out = append(out, RemoteFile{Name: c.GetName(), Size: c.GetSize(), IsDir: c.GetType() == mega.FOLDER})
	}
	return out, nil
}

// Download writes dir/name to localPath. go-mega has no context support;
// ctx is checked before starting.
func (u *megaStore) Download(ctx context.Context, dir []string, name, localPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	m, err := u.client()
	if err != nil {
		return err
	}
	parent, err := findDir(m, dir)
	if err != nil {
		return err
	}
	if parent == nil {
		return fmt.Errorf("mega %v: %w", dir, os.ErrNotExist)
	}
	children, err := m.FS.GetChildren(parent)
	if err != nil {
		return fmt.Errorf("mega list %v: %w", dir, err)
	}
	for _, c := range children {
		if c.GetType() == mega.FILE && c.GetName() == name {
			if err := m.DownloadFile(c, localPath, nil); err != nil {
				u.m = nil
				return fmt.Errorf("mega download %s: %w", name, err)
			}
			return nil
		}
	}
	return fmt.Errorf("mega %v/%s: %w", dir, name, os.ErrNotExist)
}

// findDir walks dir from the MEGA root without creating anything. It
// returns nil when a folder on the path is missing.
func findDir(m *mega.Mega, dir []string) (*mega.Node, error) {
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
			return nil, nil
		}
		node = next
	}
	return node, nil
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
