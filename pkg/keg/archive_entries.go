package keg

import (
	"sort"
	"strings"
)

// archiveEntries is read access to the file entries of one keg-archive. Names
// are slash-separated and unique; when the tar carried a name more than once,
// the last entry wins.
type archiveEntries interface {
	// Read returns the payload of name. ok is false when the archive has no
	// such entry.
	Read(name string) (data []byte, ok bool, err error)
	// Names returns every entry name in ascending order.
	Names() []string
	// WithPrefix returns the entry names that start with prefix, in ascending
	// order.
	WithPrefix(prefix string) []string
}

// sortedNamesWithPrefix returns the run of names, which must be sorted, that
// start with prefix.
func sortedNamesWithPrefix(names []string, prefix string) []string {
	start := sort.SearchStrings(names, prefix)
	end := start
	for end < len(names) && strings.HasPrefix(names[end], prefix) {
		end++
	}
	return names[start:end:end]
}

func readRequiredArchiveEntry(entries archiveEntries, path string) ([]byte, error) {
	value, ok, err := entries.Read(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalid
	}
	return value, nil
}
