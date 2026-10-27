// Package sqlfiles lists and prepares the versioned .sql files.
package sqlfiles

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

type File struct {
	Dir  string // directory it was read from, e.g. transforms/deliverydb
	Name string // base name, e.g. 02_skus.sql
	SQL  string
}

// List returns dir's .sql files sorted by file name, which is execution order.
func List(fsys fs.FS, dir string) ([]File, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var files []File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, File{Dir: dir, Name: e.Name(), SQL: string(b)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// ListMerged lists the .sql files of several directories as one sequence sorted
// by file name, e.g. transforms/common and transforms/deliverydb. A directory
// that doesn't exist contributes nothing. The same file name in two
// directories is an error, since the run order would be ambiguous.
func ListMerged(fsys fs.FS, dirs ...string) ([]File, error) {
	var all []File
	seen := map[string]string{}
	for _, dir := range dirs {
		files, err := List(fsys, dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if prev, ok := seen[f.Name]; ok {
				return nil, fmt.Errorf("%s exists in both %s and %s", f.Name, prev, dir)
			}
			seen[f.Name] = dir
			f.Dir = dir
			all = append(all, f)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

// Path is the file's path within the SQL file system.
func (f File) Path() string { return path.Join(f.Dir, f.Name) }

// Stem is the file name without extension.
func (f File) Stem() string { return strings.TrimSuffix(f.Name, ".sql") }

// Query returns the SQL trimmed of surrounding whitespace and trailing
// semicolons, ready to be wrapped in a subquery.
func (f File) Query() string {
	return strings.TrimRight(strings.TrimSpace(f.SQL), "; \t\r\n")
}

// ShowAll reports whether a profile file asks for every row instead of a sample.
func (f File) ShowAll() bool { return strings.Contains(f.SQL, "-- profile:all") }

// Wrap embeds q as a derived table. Newlines guard against a trailing line comment.
func Wrap(prefix, q, suffix string) string {
	return prefix + "\n" + q + "\n" + suffix
}
