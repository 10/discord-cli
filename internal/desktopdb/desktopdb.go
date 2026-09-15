// Package desktopdb opens disposable Discord storage copies and synthetic fixtures.
// It must never be used to open the live Discord database.
package desktopdb

import (
	"errors"
	"os"
	"sync"

	"github.com/golang/leveldb"
	ldb "github.com/golang/leveldb/db"
)

func Open(path string) (ldb.DB, error) {
	fs := &fileSystem{FileSystem: ldb.DefaultFileSystem}
	db, err := leveldb.Open(path, &ldb.Options{FileSystem: fs, VerifyChecksums: true})
	if err != nil {
		return nil, errors.Join(err, fs.close())
	}
	return &database{DB: db, fs: fs}, nil
}

type database struct {
	ldb.DB
	fs *fileSystem
}

func (d *database) Close() error {
	// LevelDB waits for compaction before closing, but leaves its manifest open.
	err := d.DB.Close()
	return errors.Join(err, d.fs.close())
}

type fileSystem struct {
	ldb.FileSystem
	mu    sync.Mutex
	files []*os.File
}

func (fs *fileSystem) Open(name string) (ldb.File, error) {
	// Journal recovery reopens a table and calls Sync, which needs write access
	// on Windows. These files belong only to a disposable copy or fixture.
	return fs.track(os.OpenFile(name, os.O_RDWR, 0))
}

func (fs *fileSystem) Create(name string) (ldb.File, error) {
	return fs.track(os.Create(name))
}

func (fs *fileSystem) track(f *os.File, err error) (ldb.File, error) {
	if err != nil {
		return nil, err
	}
	fs.mu.Lock()
	fs.files = append(fs.files, f)
	fs.mu.Unlock()
	return f, nil
}

func (fs *fileSystem) close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var errs []error
	for _, f := range fs.files {
		if err := f.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}
	fs.files = nil
	return errors.Join(errs...)
}
