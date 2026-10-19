package keg

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

// archiveManifestName is the archive entry that describes the archive.
const archiveManifestName = "keg-archive/manifest.json"

// countingWriter counts the bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// tarBudgetWriter refuses any write that would take the tar past
// MaxTarBytes, holding back room for the end-of-archive marker until close
// writes it. Nothing past the ceiling ever reaches the destination.
type tarBudgetWriter struct {
	w *countingWriter
	a *archiveWriter
}

func (b tarBudgetWriter) Write(p []byte) (int, error) {
	if max := b.a.lim.MaxTarBytes; max > 0 {
		total := b.w.n + int64(len(p))
		if !b.a.closing {
			total += 2 * tarBlock
		}
		if total > max {
			return 0, &ArchiveLimitError{Limit: ArchiveLimitTarBytes, Max: max, Actual: total, Entry: b.a.entry}
		}
	}
	return b.w.Write(p)
}

// archiveWriter writes keg-archive tar entries and enforces ArchiveLimits.
// Export and staging share it, so both measure an archive the same way.
type archiveWriter struct {
	tw      *tar.Writer
	tar     *countingWriter
	lim     ArchiveLimits
	entries int
	// index is the archive's measure under MaxIndexBytes so far.
	index int64
	// entry names the entry being written, for limit errors.
	entry string
	// closing is set while close writes the end-of-archive marker.
	closing bool
}

func newArchiveWriter(w io.Writer, lim ArchiveLimits) *archiveWriter {
	cw := &countingWriter{w: w}
	a := &archiveWriter{tar: cw, lim: lim}
	a.tw = tar.NewWriter(tarBudgetWriter{w: cw, a: a})
	return a
}

// admit counts one entry of the given size, failing before anything is
// written when it would cross the entry-count, entry-size, or index ceiling,
// or when its header and padded payload alone would take the tar past
// MaxTarBytes.
func (a *archiveWriter) admit(name string, size int64) error {
	a.entry = name
	a.entries++
	if a.lim.MaxEntries > 0 && a.entries > a.lim.MaxEntries {
		return &ArchiveLimitError{Limit: ArchiveLimitEntries, Max: int64(a.lim.MaxEntries), Actual: int64(a.entries), Entry: name}
	}
	if a.lim.MaxEntryBytes > 0 && size > a.lim.MaxEntryBytes {
		return &ArchiveLimitError{Limit: ArchiveLimitEntryBytes, Max: a.lim.MaxEntryBytes, Actual: size, Entry: name}
	}
	a.index += archiveIndexCost(filepath.ToSlash(name))
	if a.lim.MaxIndexBytes > 0 && a.index > a.lim.MaxIndexBytes {
		return &ArchiveLimitError{Limit: ArchiveLimitIndexBytes, Max: a.lim.MaxIndexBytes, Actual: a.index, Entry: name}
	}
	if a.lim.MaxTarBytes > 0 && size >= 0 {
		// The least this entry can add: one header block and its payload
		// padded to a block, plus the end-of-archive marker.
		if size > a.lim.MaxTarBytes {
			return &ArchiveLimitError{Limit: ArchiveLimitTarBytes, Max: a.lim.MaxTarBytes, Actual: size, Entry: name}
		}
		padded := (size + tarBlock - 1) / tarBlock * tarBlock
		if need := a.tar.n + tarBlock + padded + 2*tarBlock; need > a.lim.MaxTarBytes {
			return &ArchiveLimitError{Limit: ArchiveLimitTarBytes, Max: a.lim.MaxTarBytes, Actual: need, Entry: name}
		}
	}
	return nil
}

// settle flushes the current entry's padding. The tar total is enforced on
// every write, so an entry that fits has already been checked.
func (a *archiveWriter) settle(name string) error {
	if err := a.tw.Flush(); err != nil {
		return fmt.Errorf("unable to write archive payload for %s: %w", name, err)
	}
	return nil
}

func archiveFileHeader(name string, size int64) *tar.Header {
	return &tar.Header{
		Name:     filepath.ToSlash(name),
		Mode:     0o644,
		Size:     size,
		Typeflag: tar.TypeReg,
	}
}

// writeFile adds one entry from memory.
func (a *archiveWriter) writeFile(name string, data []byte) error {
	if err := a.admit(name, int64(len(data))); err != nil {
		return err
	}
	return a.writeAdmitted(name, data)
}

// writeAdmitted adds an entry that admit has already counted.
func (a *archiveWriter) writeAdmitted(name string, data []byte) error {
	if err := a.tw.WriteHeader(archiveFileHeader(name, int64(len(data)))); err != nil {
		return fmt.Errorf("unable to write archive header for %s: %w", name, err)
	}
	if _, err := a.tw.Write(data); err != nil {
		return fmt.Errorf("unable to write archive payload for %s: %w", name, err)
	}
	return a.settle(name)
}

// copyFile adds one entry of exactly size bytes read from r.
func (a *archiveWriter) copyFile(name string, size int64, r io.Reader) error {
	if err := a.admit(name, size); err != nil {
		return err
	}
	if err := a.tw.WriteHeader(archiveFileHeader(name, size)); err != nil {
		return fmt.Errorf("unable to write archive header for %s: %w", name, err)
	}
	if _, err := io.CopyN(a.tw, r, size); err != nil {
		return fmt.Errorf("unable to read archive payload %s: %w", name, err)
	}
	return a.settle(name)
}

// close writes the end-of-archive marker.
func (a *archiveWriter) close() error {
	a.closing = true
	if err := a.tw.Close(); err != nil {
		return fmt.Errorf("unable to finalize archive: %w", err)
	}
	return nil
}

// ArchiveStageStats describes an archive that StageArchive accepted.
type ArchiveStageStats struct {
	// Entries is the number of file entries.
	Entries int
	// TarBytes is the size of the staged, uncompressed tar.
	TarBytes int64
	// CompressedBytes is the number of input bytes read. It is zero when the
	// input was a plain tar.
	CompressedBytes int64
	// Nodes is the number of nodes the manifest lists.
	Nodes int
	// IndexBytes is the archive's measure under ArchiveLimits.MaxIndexBytes.
	IndexBytes int64
	// Complete reports that the manifest says the archive carries the whole
	// keg (with_settings, or the legacy with_config spelling). An archive
	// exported with a node selection, a query, or without node 0 is not
	// complete and cannot replace a keg.
	Complete bool
}

// countingReader counts bytes read and fails, naming limit, once more than
// max are read. It reads at most one byte past max.
type countingReader struct {
	r     io.Reader
	n     int64
	max   int64
	limit string
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.max > 0 && c.n > c.max {
		return 0, &ArchiveLimitError{Limit: c.limit, Max: c.max, Actual: c.n}
	}
	if c.max > 0 && int64(len(p)) > c.max-c.n+1 {
		p = p[:c.max-c.n+1]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.max > 0 && c.n > c.max {
		return n, &ArchiveLimitError{Limit: c.limit, Max: c.max, Actual: c.n}
	}
	return n, err
}

// maxArchiveHeaderBytes bounds what the tar reader may read to reach one
// entry: the previous entry's padding, its header block, and any PAX or GNU
// long-name and long-link records before it. A record can otherwise be a
// megabyte, held in memory while it is parsed, and an archive can carry one
// per entry. It leaves ample room for a name of maxArchiveNameBytes.
const maxArchiveHeaderBytes = 64 << 10

// tarHeaderReader sits under the tar reader and, while it is between
// entries, refuses to read past a bound.
type tarHeaderReader struct {
	r *countingReader
	// end is the stream offset reading must not pass, or -1 inside an
	// entry's payload.
	end int64
}

func (h *tarHeaderReader) Read(p []byte) (int, error) {
	if h.end >= 0 {
		if h.r.n >= h.end {
			return 0, fmt.Errorf("archive entry header is over the %d byte limit: %w", maxArchiveHeaderBytes, ErrInvalid)
		}
		if int64(len(p)) > h.end-h.r.n {
			p = p[:h.end-h.r.n]
		}
	}
	return h.r.Read(p)
}

// next advances tr to the next entry, reading at most maxArchiveHeaderBytes
// to get there.
func (h *tarHeaderReader) next(tr *tar.Reader) (*tar.Header, error) {
	h.end = h.r.n + maxArchiveHeaderBytes
	defer func() { h.end = -1 }()
	return tr.Next()
}

// archiveSinkError marks a failure writing to StageArchive's destination, so
// it is not mistaken for an unreadable archive.
type archiveSinkError struct{ err error }

func (e *archiveSinkError) Error() string { return e.err.Error() }
func (e *archiveSinkError) Unwrap() error { return e.err }

// sinkWriter tags every write error from w as an *archiveSinkError.
type sinkWriter struct{ w io.Writer }

func (s sinkWriter) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	if err != nil {
		err = &archiveSinkError{err: err}
	}
	return n, err
}

// sinkFailure returns the destination's own error when err came from writing
// to it.
func sinkFailure(err error) (error, bool) {
	var sinkErr *archiveSinkError
	if errors.As(err, &sinkErr) {
		return sinkErr.Unwrap(), true
	}
	return nil, false
}

// StageArchive reads a keg-archive from r, gzip or plain tar, and writes it to
// w as a normalized plain tar that import can index by offset. Directory
// entries are dropped and names are slash-normalized; every other entry,
// duplicates included, is copied in order, so import resolves duplicates the
// same way it would from the original.
//
// StageArchive rejects unsafe entry names, attachment entries whose name is
// not a valid asset name (wrapping ErrInvalidAssetName), and oversized entry
// headers, and it requires a keg-archive/v3 manifest. It checks lim while
// streaming: MaxTarBytes bounds both every byte read from the uncompressed
// stream, extended headers included, and the staged output, which is checked
// before each write, so w never receives more than MaxTarBytes. Memory use is
// bounded by the manifest, not by the archive. A rejected archive returns an
// error wrapping ErrInvalid or ErrArchiveTooLarge; w may hold a partial tar.
// A failure writing to w is returned as is.
func StageArchive(ctx context.Context, r io.Reader, w io.Writer, lim ArchiveLimits) (ArchiveStageStats, error) {
	var stats ArchiveStageStats
	compressed := &countingReader{r: r, limit: ArchiveLimitCompressedBytes}
	br := bufio.NewReader(compressed)

	var tarStream io.Reader = br
	magic, _ := br.Peek(2)
	gzipped := len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b
	if gzipped {
		compressed.max = lim.MaxCompressedBytes
		gz, err := gzip.NewReader(br)
		if err != nil {
			return stats, archiveStreamError(err)
		}
		defer gz.Close()
		tarStream = gz
	}

	aw := newArchiveWriter(sinkWriter{w: w}, lim)
	// Every byte of the uncompressed stream counts toward MaxTarBytes, not
	// just what staging writes back out: extended headers, padding, and
	// skipped entries are all decompressed and parsed.
	raw := &tarHeaderReader{r: &countingReader{r: tarStream, max: lim.MaxTarBytes, limit: ArchiveLimitTarBytes}, end: -1}
	tr := tar.NewReader(raw)
	var manifest []byte
	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		header, err := raw.next(tr)
		if err == io.EOF {
			break
		}
		if err != nil {
			return stats, archiveStreamError(err)
		}
		if header.FileInfo().IsDir() {
			// Read past any payload here, so the next header's bound
			// covers only headers.
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return stats, archiveStreamError(err)
			}
			continue
		}
		if !safeArchiveEntryName(header.Name) {
			return stats, unsafeArchiveEntryError(header.Name)
		}
		name := filepath.ToSlash(header.Name)
		if err := checkArchiveAssetEntry(name); err != nil {
			return stats, err
		}
		if name == archiveManifestName {
			// The manifest is the one entry held in memory, so it is admitted
			// against the entry-size ceiling before it is buffered.
			if err := aw.admit(name, header.Size); err != nil {
				return stats, err
			}
			var buf bytes.Buffer
			if _, err := io.CopyN(&buf, tr, header.Size); err != nil {
				return stats, archiveStreamError(err)
			}
			manifest = buf.Bytes()
			if err := aw.writeAdmitted(name, manifest); err != nil {
				if sinkErr, ok := sinkFailure(err); ok {
					return stats, sinkErr
				}
				return stats, err
			}
			continue
		}
		if err := aw.copyFile(name, header.Size, tr); err != nil {
			if sinkErr, ok := sinkFailure(err); ok {
				return stats, sinkErr
			}
			var limitErr *ArchiveLimitError
			if errors.As(err, &limitErr) {
				return stats, err
			}
			return stats, archiveStreamError(err)
		}
	}
	if err := aw.close(); err != nil {
		if sinkErr, ok := sinkFailure(err); ok {
			return stats, sinkErr
		}
		return stats, err
	}
	// The tar can end, and the loop with it, before a limit error from the
	// compressed stream surfaces through gzip, so check the count directly.
	if err := checkCompressedSize(compressed.n, ArchiveLimits{MaxCompressedBytes: compressed.max}); err != nil {
		return stats, err
	}

	if manifest == nil {
		return stats, fmt.Errorf("archive manifest missing: %w", ErrInvalid)
	}
	var head struct {
		Format       string            `json:"format"`
		WithSettings bool              `json:"with_settings"`
		WithConfig   bool              `json:"with_config"`
		Nodes        []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(manifest, &head); err != nil {
		return stats, fmt.Errorf("unable to parse archive manifest: %v: %w", err, ErrInvalid)
	}
	if head.Format != kegArchiveFormatV3 {
		return stats, fmt.Errorf("unsupported archive format %q: %w", head.Format, ErrInvalid)
	}

	stats.Entries = aw.entries
	stats.IndexBytes = aw.index
	stats.TarBytes = aw.tar.n
	if gzipped {
		stats.CompressedBytes = compressed.n
	}
	stats.Nodes = len(head.Nodes)
	stats.Complete = head.WithSettings || head.WithConfig
	return stats, nil
}

// archiveStreamError classifies a failure to read the archive stream itself.
// Limit errors pass through; anything else means the input is not a readable
// keg-archive.
func archiveStreamError(err error) error {
	var limitErr *ArchiveLimitError
	if errors.As(err, &limitErr) {
		return err
	}
	return fmt.Errorf("unable to read archive stream: %v: %w", err, ErrInvalid)
}
