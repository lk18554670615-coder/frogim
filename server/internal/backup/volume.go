package backup

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Volumes are archived offline. Symlinks, hardlinks, devices, sockets, xattrs,
// set-ID bits and paths meaningful outside the volume are never accepted.
func ExportVolume(root string, out io.Writer) error {
	if !filepath.IsAbs(root) {
		return ErrInvalid
	}
	tw := tar.NewWriter(out)
	var total int64
	entries := 0
	e := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		entries++
		if entries > 1_000_000 {
			return ErrInvalid
		}
		if walkErr != nil {
			return ErrInvalid
		}
		info, e := entry.Info()
		if e != nil || (!info.IsDir() && !info.Mode().IsRegular()) || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return ErrInvalid
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return ErrInvalid
		}
		h, e := tar.FileInfoHeader(info, "")
		if e != nil {
			return ErrInvalid
		}
		h.Name = filepath.ToSlash(rel)
		h.Uname, h.Gname = "", ""
		h.AccessTime, h.ChangeTime = h.ModTime, h.ModTime
		if !safeHeader(h) {
			return ErrInvalid
		}
		total += h.Size
		if total > MaxFileSize {
			return ErrInvalid
		}
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return ErrInvalid
		}
		_, e = io.CopyN(tw, f, h.Size)
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			return ErrInvalid
		}
		return nil
	})
	if e != nil {
		return e
	}
	return tw.Close()
}

func safeHeader(h *tar.Header) bool {
	for key := range h.PAXRecords {
		switch key {
		case "path", "size", "uid", "gid", "mtime", "atime", "ctime":
		default:
			return false
		}
	}
	return (h.Typeflag == tar.TypeDir || h.Typeflag == tar.TypeReg) && fs.ValidPath(h.Name) && !strings.ContainsAny(h.Name, "\\:\x00") && len(h.Name) <= 4096 &&
		(h.Name != "." || h.Typeflag == tar.TypeDir) && h.Linkname == "" && len(h.Xattrs) == 0 && h.Uid >= 0 && h.Uid <= 1<<20 && h.Gid >= 0 && h.Gid <= 1<<20 &&
		h.Mode >= 0 && h.Mode & ^int64(0777) == 0 && h.Size >= 0 && h.Size <= MaxFileSize && (h.Typeflag != tar.TypeDir || h.Size == 0)
}

// Validation and extraction share the same parser. Destination must be empty;
// it is never recursively deleted, merged or overwritten. os.Root confines
// every write even if the directory is concurrently changed outside this tool.
func ReadVolume(src io.Reader, destination string) error {
	var root *os.Root
	if destination != "" {
		if !filepath.IsAbs(destination) {
			return ErrInvalid
		}
		entries, e := os.ReadDir(destination)
		if e != nil || len(entries) != 0 {
			return ErrInvalid
		}
		root, e = os.OpenRoot(destination)
		if e != nil {
			return ErrInvalid
		}
		defer root.Close()
	}
	counted := &countReader{Reader: src}
	tr := tar.NewReader(counted)
	seen := map[string]bool{}
	directories := map[string]bool{}
	var total int64
	type directory struct {
		path     string
		uid, gid int
		mode     os.FileMode
	}
	dirs := []directory{}
	for {
		before := counted.n
		h, e := tr.Next()
		if e == io.EOF {
			if counted.n-before < 1024 {
				return ErrInvalid
			}
			break
		}
		if e != nil || !safeHeader(h) || seen[h.Name] || len(seen) >= 1_000_000 {
			return ErrInvalid
		}
		if len(seen) == 0 && h.Name != "." {
			return ErrInvalid
		}
		if h.Name != "." && !directories[path.Dir(h.Name)] {
			return ErrInvalid
		}
		seen[h.Name] = true
		directories[h.Name] = h.Typeflag == tar.TypeDir
		total += h.Size
		if total > MaxFileSize {
			return ErrInvalid
		}
		if root == nil {
			if _, e = io.Copy(io.Discard, tr); e != nil {
				return ErrInvalid
			}
			continue
		}
		if h.Typeflag == tar.TypeDir {
			if h.Name != "." {
				if e = root.Mkdir(h.Name, 0700); e != nil {
					return ErrInvalid
				}
			}
			dirs = append(dirs, directory{h.Name, h.Uid, h.Gid, os.FileMode(h.Mode)})
			continue
		}
		f, e := root.OpenFile(h.Name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return ErrInvalid
		}
		_, e = io.CopyN(f, tr, h.Size)
		if e == nil {
			e = f.Chown(h.Uid, h.Gid)
		}
		if e == nil {
			e = f.Chmod(os.FileMode(h.Mode))
		}
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			return ErrInvalid
		}
	}
	if !seen["."] {
		return ErrInvalid
	}
	// Our exporter writes exactly one footer; no concatenated archive or
	// arbitrary trailing padding is accepted.
	var extra [1]byte
	if n, e := src.Read(extra[:]); n != 0 || e != io.EOF {
		return ErrInvalid
	}
	if root != nil {
		for i := len(dirs) - 1; i >= 0; i-- {
			d := dirs[i]
			if root.Chown(d.path, d.uid, d.gid) != nil || root.Chmod(d.path, d.mode) != nil {
				return ErrInvalid
			}
		}
	}
	return nil
}

type countReader struct {
	io.Reader
	n int64
}

func (r *countReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.n += int64(n)
	return n, e
}
