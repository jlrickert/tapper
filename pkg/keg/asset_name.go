package keg

import (
	"fmt"
	"path/filepath"
	"strings"
)

// validAssetName rejects node asset names that could escape a node's asset
// directory. Asset names are single path components (bare filenames such as
// "diagram.png" or "notes.txt"); the WriteImage/WriteFile archive round-trip
// strips any directory prefix, so a legitimate name never contains a separator.
//
// Anything containing a path separator, a "." / ".." component, or an absolute
// path is rejected so repository implementations cannot resolve outside the
// node's attachment namespace (CWE-22). Enforced at every Repository asset
// sink, which is the only chokepoint that also covers archive import — that
// path writes assets through the Repository directly, bypassing LocalKeg.
func validAssetName(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		return fmt.Errorf("%w: %q", ErrInvalidAssetName, name)
	}
	return nil
}

// ValidateAssetName reports whether name is a single safe node asset filename.
func ValidateAssetName(name string) error {
	return validAssetName(name)
}

// safeArchiveEntryName reports whether a keg-archive tar entry name is safe to
// store and later join onto disk: relative, with no ".." path segment. Hostile
// archives plant ".." in an asset entry (e.g. keg-archive/nodes/1/assets/../../x)
// to escape the keg root when imported (zip-slip, CWE-22). Import rejects an
// archive containing any such entry before writing anything to the repository.
//
// A name longer than maxArchiveNameBytes is refused too. No export writes one,
// and a tar header can carry a name of up to a megabyte, which an import
// would otherwise keep in memory for every entry.
func safeArchiveEntryName(name string) bool {
	if len(name) > maxArchiveNameBytes {
		return false
	}
	name = filepath.ToSlash(name)
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// maxArchiveNameBytes is the longest keg-archive entry name accepted, the
// usual PATH_MAX.
const maxArchiveNameBytes = 4096

// unsafeArchiveEntryError reports an entry safeArchiveEntryName refused,
// quoting at most the start of its name so a huge one is not echoed back.
func unsafeArchiveEntryError(name string) error {
	if len(name) > maxArchiveNameBytes {
		return fmt.Errorf("archive entry name of %d bytes starting %q is over the %d byte limit: %w", len(name), name[:64], maxArchiveNameBytes, ErrInvalid)
	}
	return fmt.Errorf("unsafe archive entry %q: %w", name, ErrInvalid)
}
