package deployment

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/linli/im/server/internal/privatefile"
)

// WriteEnterpriseRelease requires a pre-existing operator-owned directory.
// Private Compose data is durable before publishing release.json. A partially
// written artifact is never adopted or overwritten, even on an explicit retry.
func WriteEnterpriseRelease(root string, c EnterpriseConfig, release Release) (Release, error) {
	if !filepath.IsAbs(root) {
		return Release{}, ErrBundle
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Release{}, ErrBundle
	}
	raw, r, e := BuildEnterpriseBundle(c, release)
	if e != nil {
		return Release{}, e
	}
	metadata, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return Release{}, ErrBundle
	}
	bounded, e := os.OpenRoot(root)
	if e != nil {
		return Release{}, ErrBundle
	}
	defer bounded.Close()
	for _, id := range r.RollbackTo {
		// A declared rollback target must be an intact earlier local artifact,
		// not merely an arbitrary ID that will fail after deployment starts.
		data, err := bounded.Open(filepath.Join(id, "release.json"))
		if err != nil {
			return Release{}, ErrBundle
		}
		var prior Release
		decoder := json.NewDecoder(io.LimitReader(data, 65537))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&prior)
		trailing := decoder.Decode(new(any))
		data.Close()
		if err != nil || trailing != io.EOF || !prior.Valid() || prior.ID != id || prior.Sequence >= r.Sequence || prior.Runtime != r.Runtime || prior.SchemaVersion != r.SchemaVersion || !prior.MatchesTarget(r.TenantID, r.ServerID) || prior.isolationMode() != r.isolationMode() || prior.DatastoreMode != r.DatastoreMode || prior.IngressMode != r.IngressMode {
			return Release{}, ErrBundle
		}
		if _, _, err = readBundle(root, id, prior.ComposeSHA256); err != nil {
			return Release{}, ErrBundle
		}
	}
	if info, e = bounded.Lstat(r.ID); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Release{}, ErrBundle
		}
		old, _, e := readBundle(root, r.ID, r.ComposeSHA256)
		stored, e2 := bounded.ReadFile(filepath.Join(r.ID, "release.json"))
		if e != nil || e2 != nil || !bytes.Equal(old, raw) || !bytes.Equal(stored, metadata) {
			return Release{}, ErrBundle
		}
		return r, nil
	} else if !os.IsNotExist(e) {
		return Release{}, ErrBundle
	}
	if e = bounded.Mkdir(r.ID, 0700); e != nil {
		return Release{}, ErrBundle
	}
	for _, file := range []struct {
		name string
		data []byte
	}{{"compose.json", raw}, {"release.json", metadata}} {
		f, e := privatefile.Create(filepath.Join(root, r.ID, file.name))
		if e != nil {
			return Release{}, ErrBundle
		}
		_, e = f.Write(file.data)
		syncErr := f.Sync()
		closeErr := f.Close()
		if e != nil || syncErr != nil || closeErr != nil {
			return Release{}, ErrBundle
		}
	}
	return r, nil
}
