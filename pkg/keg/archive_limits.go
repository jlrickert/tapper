package keg

import (
	"errors"
	"fmt"
)

// ErrArchiveTooLarge reports a keg-archive that exceeds an ArchiveLimits
// ceiling. *ArchiveLimitError unwraps to it.
var ErrArchiveTooLarge = errors.New("archive exceeds limit")

// Archive limit names carried by ArchiveLimitError.Limit.
const (
	ArchiveLimitEntries         = "entries"
	ArchiveLimitEntryBytes      = "entry_bytes"
	ArchiveLimitTarBytes        = "tar_bytes"
	ArchiveLimitCompressedBytes = "compressed_bytes"
	ArchiveLimitIndexBytes      = "index_bytes"
)

// ArchiveLimits bounds a keg-archive. Export and import measure the same
// quantities the same way, so an archive that export produces under a set of
// limits is accepted by an import under the same limits. A zero field is
// unlimited.
type ArchiveLimits struct {
	// MaxEntries bounds the number of file entries. Directory entries are not
	// counted; export never writes them.
	MaxEntries int
	// MaxEntryBytes bounds the payload size of any one entry.
	MaxEntryBytes int64
	// MaxTarBytes bounds the uncompressed tar stream, headers, padding, and
	// end-of-archive marker included.
	MaxTarBytes int64
	// MaxCompressedBytes bounds the gzip stream.
	MaxCompressedBytes int64
	// MaxIndexBytes bounds the memory an import's entry index takes: the
	// sum, over every file entry, of its name's length plus
	// ArchiveIndexEntryOverhead. Entry names are caller-supplied, and the
	// index holds every one of them for the whole import.
	MaxIndexBytes int64
}

// ArchiveIndexEntryOverhead is what MaxIndexBytes charges each entry on top
// of its name: the bookkeeping an index keeps per entry.
const ArchiveIndexEntryOverhead = 64

// archiveIndexCost is what one entry adds to the measure MaxIndexBytes
// bounds.
func archiveIndexCost(name string) int64 {
	return int64(len(name)) + ArchiveIndexEntryOverhead
}

// ArchiveLimitError reports which ArchiveLimits ceiling an archive crossed.
// Limits are checked while the archive streams, so for the entry count and
// the byte totals Actual is the value at the point the ceiling was crossed,
// not the archive's final total.
type ArchiveLimitError struct {
	// Limit is one of the ArchiveLimit* names.
	Limit string
	// Max is the configured ceiling.
	Max int64
	// Actual is the measured value that exceeded Max.
	Actual int64
	// Entry names the entry being processed, when there is one.
	Entry string
}

func (e *ArchiveLimitError) Error() string {
	switch e.Limit {
	case ArchiveLimitEntries:
		return fmt.Sprintf("archive has more than %d entries", e.Max)
	case ArchiveLimitEntryBytes:
		return fmt.Sprintf("archive entry %q is %d bytes, over the %d byte limit", e.Entry, e.Actual, e.Max)
	case ArchiveLimitTarBytes:
		return fmt.Sprintf("archive expands beyond the %d byte limit", e.Max)
	case ArchiveLimitCompressedBytes:
		return fmt.Sprintf("archive is larger than the %d byte limit", e.Max)
	case ArchiveLimitIndexBytes:
		return fmt.Sprintf("archive entry names need more than the %d byte index limit", e.Max)
	default:
		return fmt.Sprintf("archive exceeds %s limit of %d", e.Limit, e.Max)
	}
}

func (e *ArchiveLimitError) Unwrap() error { return ErrArchiveTooLarge }

// tarBlock is the tar record size; headers and payloads are padded to it.
const tarBlock = 512
