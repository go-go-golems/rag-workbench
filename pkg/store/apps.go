// Package store keeps saved scripts: an apps directory on disk is the source
// of truth (decision D7: jsverbs scans it directly, so a saved script is
// runnable with no export step), and every save is also recorded as a version
// in SQLite so earlier versions can be listed and restored.
//
// Layout: <dir>/<app>/<path>, where path is a slash-separated relative file
// path such as "search.js", "lib/corpus.js" or "pipeline.wb.js".
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// MaxFileBytes caps one saved file.
const MaxFileBytes = 1 << 20

// Extensions that can be saved. Scripts, their data and notes; nothing executable outside goja.
var allowedExt = map[string]bool{".js": true, ".cjs": true, ".json": true, ".md": true, ".markdown": true, ".txt": true}

var appNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ErrNotFound is returned for a missing app, file or version.
var ErrNotFound = errors.New("not found")

// Apps is the app store.
type Apps struct {
	Dir string
	db  *sql.DB
	mu  sync.Mutex // serializes saves and deletes (history versions are per file)
}

// OpenApps opens (creating as needed) the apps directory and the history table in the SQLite file at dbPath.
func OpenApps(dir, dbPath string) (*Apps, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS app_history (
		app TEXT NOT NULL, path TEXT NOT NULL, version INTEGER NOT NULL,
		sha256 TEXT NOT NULL, source BLOB NOT NULL, comment TEXT NOT NULL DEFAULT '',
		deleted INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL,
		PRIMARY KEY (app, path, version))`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Apps{Dir: abs, db: db}, nil
}

func (a *Apps) Close() error { return a.db.Close() }

// AppInfo summarizes one app directory.
type AppInfo struct {
	Name    string    `json:"name"`
	Files   int       `json:"files"`
	Updated time.Time `json:"updated"`
}

// FileInfo describes one file of an app.
type FileInfo struct {
	Path    string    `json:"path"`
	Role    string    `json:"role"` // workbench | verbs | library | doc | data
	Size    int64     `json:"size"`
	Updated time.Time `json:"updated"`
	Version int       `json:"version"` // latest history version, 0 if never saved through the store
}

// Version is one history entry.
type Version struct {
	App     string    `json:"app"`
	Path    string    `json:"path"`
	Version int       `json:"version"`
	SHA256  string    `json:"sha256"`
	Size    int       `json:"size"`
	Comment string    `json:"comment"`
	Deleted bool      `json:"deleted"`
	Created time.Time `json:"created"`
}

// SaveResult is what Save did.
type SaveResult struct {
	App       string `json:"app"`
	Path      string `json:"path"`
	Version   int    `json:"version"`
	SHA256    string `json:"sha256"`
	Unchanged bool   `json:"unchanged"` // same bytes as the latest version: nothing written
}

// Role classifies a file by name: *.wb.js is a workbench script (ragwb run),
// lib/** a library (require only), other .js files hold verbs (ragwb verbs).
func Role(p string) string {
	switch ext := strings.ToLower(path.Ext(p)); {
	case strings.HasSuffix(strings.ToLower(p), ".wb.js"):
		return "workbench"
	case ext == ".js" || ext == ".cjs":
		if strings.HasPrefix(p, "lib/") {
			return "library"
		}
		return "verbs"
	case ext == ".json":
		return "data"
	default:
		return "doc"
	}
}

// CheckApp validates an app name.
func CheckApp(app string) error {
	if !appNameRE.MatchString(app) || app == "." || app == ".." {
		return fmt.Errorf("invalid app name %q: use letters, digits, '.', '_' and '-' (max 64)", app)
	}
	return nil
}

// CheckPath validates a file path inside an app.
func CheckPath(p string) error {
	if p == "" || len(p) > 256 || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return fmt.Errorf("invalid file path %q: use a clean relative path like lib/corpus.js", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || strings.HasPrefix(seg, ".") {
			return fmt.Errorf("invalid file path %q: no '..' or hidden segments", p)
		}
	}
	if !allowedExt[strings.ToLower(path.Ext(p))] {
		return fmt.Errorf("invalid file path %q: extension must be one of .js .cjs .json .md .markdown .txt", p)
	}
	return nil
}

// AppDir returns the absolute directory of an app (which may not exist yet).
func (a *Apps) AppDir(app string) (string, error) {
	if err := CheckApp(app); err != nil {
		return "", err
	}
	return filepath.Join(a.Dir, app), nil
}

// ListApps lists app directories, most recently changed first.
func (a *Apps) ListApps() ([]AppInfo, error) {
	entries, err := os.ReadDir(a.Dir)
	if err != nil {
		return nil, err
	}
	out := []AppInfo{}
	for _, e := range entries {
		if !e.IsDir() || CheckApp(e.Name()) != nil {
			continue
		}
		files, err := a.Files(e.Name())
		if err != nil {
			return nil, err
		}
		info := AppInfo{Name: e.Name(), Files: len(files)}
		for _, f := range files {
			if f.Updated.After(info.Updated) {
				info.Updated = f.Updated
			}
		}
		if info.Updated.IsZero() {
			if fi, err := e.Info(); err == nil {
				info.Updated = fi.ModTime()
			}
		}
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Files lists the saveable files of an app, sorted by path.
func (a *Apps) Files(app string) ([]FileInfo, error) {
	dir, err := a.AppDir(app)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("app %q: %w", app, ErrNotFound)
	}
	latest, err := a.latestVersions(app)
	if err != nil {
		return nil, err
	}
	out := []FileInfo{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if CheckPath(rel) != nil {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, FileInfo{Path: rel, Role: Role(rel), Size: fi.Size(), Updated: fi.ModTime(), Version: latest[rel]})
		return nil
	})
	return out, err
}

func (a *Apps) latestVersions(app string) (map[string]int, error) {
	rows, err := a.db.Query(`SELECT path, MAX(version) FROM app_history WHERE app = ? GROUP BY path`, app)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var p string
		var v int
		if err := rows.Scan(&p, &v); err != nil {
			return nil, err
		}
		m[p] = v
	}
	return m, rows.Err()
}

// Read returns a file's current contents.
func (a *Apps) Read(app, p string) ([]byte, error) {
	root, err := a.openRoot(app, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := CheckPath(p); err != nil {
		return nil, err
	}
	b, err := root.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s/%s: %w", app, p, ErrNotFound)
	}
	return b, err
}

// openRoot opens the app directory as an os.Root, so file operations cannot
// escape it (also not through symlinks).
func (a *Apps) openRoot(app string, create bool) (*os.Root, error) {
	dir, err := a.AppDir(app)
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("app %q: %w", app, ErrNotFound)
	}
	return root, err
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Save writes a file atomically (temp file + rename) and records a history version.
// If the file on disk was changed outside the store since its last saved version,
// that on-disk content is recorded first (comment "changed on disk"), so an
// overwrite never loses it. Saving the same bytes as the latest version is a no-op.
func (a *Apps) Save(app, p string, src []byte, comment string) (SaveResult, error) {
	if err := CheckApp(app); err != nil {
		return SaveResult{}, err
	}
	if err := CheckPath(p); err != nil {
		return SaveResult{}, err
	}
	if len(src) > MaxFileBytes {
		return SaveResult{}, fmt.Errorf("%s/%s: %d bytes is over the %d-byte limit", app, p, len(src), MaxFileBytes)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	root, err := a.openRoot(app, true)
	if err != nil {
		return SaveResult{}, err
	}
	defer root.Close()

	sum := digest(src)
	last, err := a.latest(app, p)
	if err != nil {
		return SaveResult{}, err
	}
	disk, derr := root.ReadFile(p)
	onDisk := derr == nil
	if derr != nil && !errors.Is(derr, fs.ErrNotExist) {
		return SaveResult{}, derr
	}
	if onDisk && digest(disk) == sum && last != nil && last.SHA256 == sum && !last.Deleted {
		return SaveResult{App: app, Path: p, Version: last.Version, SHA256: sum, Unchanged: true}, nil
	}
	next := 1
	if last != nil {
		next = last.Version + 1
	}
	if onDisk && (last == nil || last.Deleted || last.SHA256 != digest(disk)) && digest(disk) != sum {
		if err := a.insert(app, p, next, disk, "changed on disk", false); err != nil {
			return SaveResult{}, err
		}
		next++
	}
	if dir := path.Dir(p); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return SaveResult{}, err
		}
	}
	tmp := path.Join(path.Dir(p), fmt.Sprintf(".%s.%d.tmp", path.Base(p), time.Now().UnixNano()))
	if err := root.WriteFile(tmp, src, 0o644); err != nil {
		return SaveResult{}, err
	}
	if err := root.Rename(tmp, p); err != nil {
		_ = root.Remove(tmp)
		return SaveResult{}, err
	}
	if err := a.insert(app, p, next, src, comment, false); err != nil {
		return SaveResult{}, err
	}
	return SaveResult{App: app, Path: p, Version: next, SHA256: sum}, nil
}

// Delete removes a file and records a deletion version (its last content stays in history).
func (a *Apps) Delete(app, p, comment string) error {
	if err := CheckPath(p); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	root, err := a.openRoot(app, false)
	if err != nil {
		return err
	}
	defer root.Close()
	disk, err := root.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s/%s: %w", app, p, ErrNotFound)
	}
	if err != nil {
		return err
	}
	last, err := a.latest(app, p)
	if err != nil {
		return err
	}
	next := 1
	if last != nil {
		next = last.Version + 1
	}
	if err := root.Remove(p); err != nil {
		return err
	}
	if comment == "" {
		comment = "deleted"
	}
	return a.insert(app, p, next, disk, comment, true)
}

func (a *Apps) insert(app, p string, version int, src []byte, comment string, deleted bool) error {
	_, err := a.db.Exec(`INSERT INTO app_history (app, path, version, sha256, source, comment, deleted, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		app, p, version, digest(src), src, comment, deleted, time.Now().UnixMilli())
	return err
}

func (a *Apps) latest(app, p string) (*Version, error) {
	vs, err := a.query(`WHERE app = ? AND path = ? ORDER BY version DESC LIMIT 1`, app, p)
	if err != nil || len(vs) == 0 {
		return nil, err
	}
	return &vs[0], nil
}

// History lists versions, newest first: of one file, or of every file in the app when p is "".
func (a *Apps) History(app, p string) ([]Version, error) {
	if err := CheckApp(app); err != nil {
		return nil, err
	}
	if p == "" {
		return a.query(`WHERE app = ? ORDER BY created DESC, path, version DESC`, app)
	}
	if err := CheckPath(p); err != nil {
		return nil, err
	}
	return a.query(`WHERE app = ? AND path = ? ORDER BY version DESC`, app, p)
}

func (a *Apps) query(where string, args ...any) ([]Version, error) {
	rows, err := a.db.Query(`SELECT app, path, version, sha256, length(source), comment, deleted, created FROM app_history `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		var created int64
		if err := rows.Scan(&v.App, &v.Path, &v.Version, &v.SHA256, &v.Size, &v.Comment, &v.Deleted, &created); err != nil {
			return nil, err
		}
		v.Created = time.UnixMilli(created)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReadVersion returns the content of one history version.
func (a *Apps) ReadVersion(app, p string, version int) ([]byte, error) {
	if err := CheckApp(app); err != nil {
		return nil, err
	}
	if err := CheckPath(p); err != nil {
		return nil, err
	}
	var src []byte
	err := a.db.QueryRow(`SELECT source FROM app_history WHERE app = ? AND path = ? AND version = ?`, app, p, version).Scan(&src)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s/%s@%d: %w", app, p, version, ErrNotFound)
	}
	return src, err
}
