package keg

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

// ArchiveSource is random access to a staged, uncompressed keg-archive tar,
// such as the output of StageArchive. A *bytes.Reader satisfies it.
type ArchiveSource interface {
	io.ReaderAt
	// Size returns the length of the staged tar in bytes.
	Size() int64
}

// archiveIndex serves archive entries from an ArchiveSource by offset, so an
// import holds only the entry it is reading.
type archiveIndex struct {
	src     io.ReaderAt
	offsets map[string]archiveIndexEntry
	names   []string
}

type archiveIndexEntry struct {
	off  int64
	size int64
}

// indexArchive scans the tar in src once and records where each file entry's
// payload lives. It applies the same rules as StageArchive: directory entries
// are skipped, unsafe names (overlong ones included) are rejected, names are
// slash-normalized, the last of duplicate names wins, and lim is enforced,
// MaxIndexBytes as the index grows, so its memory stays within that bound.
func indexArchive(ctx context.Context, src ArchiveSource, lim ArchiveLimits) (*archiveIndex, error) {
	if lim.MaxTarBytes > 0 && src.Size() > lim.MaxTarBytes {
		return nil, &ArchiveLimitError{Limit: ArchiveLimitTarBytes, Max: lim.MaxTarBytes, Actual: src.Size()}
	}
	sr := io.NewSectionReader(src, 0, src.Size())
	tr := tar.NewReader(sr)
	idx := &archiveIndex{src: src, offsets: map[string]archiveIndexEntry{}}
	entries := 0
	var index int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, archiveStreamError(err)
		}
		if header.FileInfo().IsDir() {
			continue
		}
		if !safeArchiveEntryName(header.Name) {
			return nil, unsafeArchiveEntryError(header.Name)
		}
		// A sparse entry's payload is not stored contiguously, so it has no
		// single offset. StageArchive rewrites sparse entries as regular ones.
		if header.Typeflag == tar.TypeGNUSparse || isPAXSparse(header) {
			return nil, fmt.Errorf("sparse archive entry %q is not supported: %w", header.Name, ErrInvalid)
		}
		entries++
		if lim.MaxEntries > 0 && entries > lim.MaxEntries {
			return nil, &ArchiveLimitError{Limit: ArchiveLimitEntries, Max: int64(lim.MaxEntries), Actual: int64(entries), Entry: header.Name}
		}
		if lim.MaxEntryBytes > 0 && header.Size > lim.MaxEntryBytes {
			return nil, &ArchiveLimitError{Limit: ArchiveLimitEntryBytes, Max: lim.MaxEntryBytes, Actual: header.Size, Entry: header.Name}
		}
		name := filepath.ToSlash(header.Name)
		index += archiveIndexCost(name)
		if lim.MaxIndexBytes > 0 && index > lim.MaxIndexBytes {
			return nil, &ArchiveLimitError{Limit: ArchiveLimitIndexBytes, Max: lim.MaxIndexBytes, Actual: index, Entry: name}
		}
		// After Next the section reader sits at the start of the payload.
		off, err := sr.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, archiveStreamError(err)
		}
		if off+header.Size > src.Size() {
			return nil, archiveStreamError(io.ErrUnexpectedEOF)
		}
		if _, seen := idx.offsets[name]; !seen {
			idx.names = append(idx.names, name)
		}
		idx.offsets[name] = archiveIndexEntry{off: off, size: header.Size}
	}
	slices.Sort(idx.names)
	return idx, nil
}

func isPAXSparse(header *tar.Header) bool {
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse.") {
			return true
		}
	}
	return false
}

func (x *archiveIndex) Read(name string) ([]byte, bool, error) {
	entry, ok := x.offsets[name]
	if !ok {
		return nil, false, nil
	}
	data := make([]byte, entry.size)
	if n, err := x.src.ReadAt(data, entry.off); n < len(data) {
		if err == nil || err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, false, fmt.Errorf("unable to read archive payload %s: %w", name, err)
	}
	return data, true, nil
}

func (x *archiveIndex) Names() []string { return x.names }

func (x *archiveIndex) WithPrefix(prefix string) []string {
	return sortedNamesWithPrefix(x.names, prefix)
}
