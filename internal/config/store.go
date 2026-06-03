package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	backupSuffix      = ".bak."
	defaultKeepBackup = 10
)

type Store struct {
	Path       string
	KeepBackup int
	Now        func() time.Time
}

func NewStore(path string) *Store {
	return &Store{Path: path, KeepBackup: defaultKeepBackup, Now: time.Now}
}

// Read returns the raw bytes of the config file.
func (s *Store) Read() ([]byte, error) {
	return os.ReadFile(s.Path)
}

// Write replaces the config file atomically and creates a timestamped backup
// of the previous content. Backups beyond KeepBackup are rotated out.
func (s *Store) Write(content []byte) (BackupInfo, error) {
	if !json.Valid(content) {
		return BackupInfo{}, errors.New("content is not valid JSON")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return BackupInfo{}, err
	}

	backup, err := s.snapshot()
	if err != nil {
		return BackupInfo{}, err
	}

	tmp := s.Path + ".new"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return backup, err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		os.Remove(tmp)
		return backup, err
	}

	if err := s.rotate(); err != nil {
		return backup, fmt.Errorf("rotate backups: %w", err)
	}
	return backup, nil
}

type BackupInfo struct {
	Path      string    `json:"path,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Bytes     int64     `json:"bytes,omitempty"`
}

func (s *Store) snapshot() (BackupInfo, error) {
	src, err := os.Open(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BackupInfo{}, nil
		}
		return BackupInfo{}, err
	}
	defer src.Close()

	ts := s.Now().UTC().Format("20060102T150405Z")
	dst := s.Path + backupSuffix + ts
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return BackupInfo{}, err
	}
	n, err := io.Copy(out, src)
	if err != nil {
		out.Close()
		os.Remove(dst)
		return BackupInfo{}, err
	}
	if err := out.Close(); err != nil {
		return BackupInfo{}, err
	}
	return BackupInfo{Path: dst, Timestamp: s.Now().UTC(), Bytes: n}, nil
}

func (s *Store) rotate() error {
	backups, err := s.listBackups()
	if err != nil {
		return err
	}
	keep := s.KeepBackup
	if keep <= 0 {
		keep = defaultKeepBackup
	}
	if len(backups) <= keep {
		return nil
	}
	for _, p := range backups[keep:] {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) listBackups() ([]string, error) {
	dir := filepath.Dir(s.Path)
	base := filepath.Base(s.Path) + backupSuffix
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), base) {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	// Newest first (timestamps are lexicographically sortable).
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

func (s *Store) ListBackups() ([]string, error) { return s.listBackups() }

// BackupMeta describes a backup file for listing in the UI.
type BackupMeta struct {
	Name      string    `json:"name"`
	Timestamp time.Time `json:"timestamp"`
	Bytes     int64     `json:"bytes"`
}

// ListBackupMeta returns metadata for each backup, newest first. The
// timestamp is parsed from the filename suffix (…bak.<UTC>); on parse
// failure it falls back to the file mtime.
func (s *Store) ListBackupMeta() ([]BackupMeta, error) {
	paths, err := s.listBackups()
	if err != nil {
		return nil, err
	}
	base := filepath.Base(s.Path) + backupSuffix
	out := make([]BackupMeta, 0, len(paths))
	for _, p := range paths {
		name := filepath.Base(p)
		m := BackupMeta{Name: name}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		m.Bytes = st.Size()
		stamp := strings.TrimPrefix(name, base)
		if ts, perr := time.Parse("20060102T150405Z", stamp); perr == nil {
			m.Timestamp = ts.UTC()
		} else {
			m.Timestamp = st.ModTime().UTC()
		}
		out = append(out, m)
	}
	return out, nil
}

// ReadBackup returns the content of a backup by its base name. The name is
// validated to be a real backup of this config (correct prefix, no path
// separators) to prevent traversal.
func (s *Store) ReadBackup(name string) ([]byte, error) {
	base := filepath.Base(s.Path) + backupSuffix
	if name != filepath.Base(name) || !strings.HasPrefix(name, base) {
		return nil, fmt.Errorf("invalid backup name %q", name)
	}
	return os.ReadFile(filepath.Join(filepath.Dir(s.Path), name))
}
