package main

// Reader for Naughty Dog PSARC archives (v1.4, Oodle or zlib blocks).
// Format notes: big-endian header, 30-byte entries (md5 name hash, first block
// index, 40-bit size, 40-bit offset), then a u16 table of compressed block
// lengths (0 = a full, stored block). Entry 0 is the list of file names.

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DecompressFunc inflates one Oodle block into dst (len(dst) = expected size).
type DecompressFunc func(src, dst []byte) error

type psarcEntry struct {
	Hash   [16]byte
	Block  uint32
	Size   int64
	Offset int64
	Name   string
}

type PSARC struct {
	Path      string
	f         *os.File
	comp      string
	blockSize int64
	entries   []psarcEntry
	lens      []uint32
	oodle     DecompressFunc
	byName    map[string]*psarcEntry
}

func OpenPSARC(path string, oodle DecompressFunc) (*PSARC, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	a := &PSARC{Path: path, f: f, oodle: oodle, byName: map[string]*psarcEntry{}}
	if err := a.readHeader(); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %v", filepath.Base(path), err)
	}
	return a, nil
}

func (a *PSARC) Close() { a.f.Close() }

func (a *PSARC) readHeader() error {
	var h [32]byte
	if _, err := io.ReadFull(a.f, h[:]); err != nil {
		return err
	}
	if string(h[0:4]) != "PSAR" {
		return fmt.Errorf("not a PSARC file")
	}
	be := binary.BigEndian
	if be.Uint16(h[4:]) != 1 || be.Uint16(h[6:]) != 4 {
		return fmt.Errorf("unsupported PSARC version %d.%d", be.Uint16(h[4:]), be.Uint16(h[6:]))
	}
	a.comp = string(h[8:12])
	if a.comp != "oodl" && a.comp != "zlib" {
		return fmt.Errorf("unknown compression %q", a.comp)
	}
	headerSize := int64(be.Uint32(h[12:]))
	entrySize := int64(be.Uint32(h[16:]))
	count := int64(be.Uint32(h[20:]))
	a.blockSize = int64(be.Uint32(h[24:]))
	if a.blockSize == 0 {
		a.blockSize = 65536
	}
	if entrySize < 30 || headerSize < 32+count*entrySize || headerSize > 1<<30 {
		return fmt.Errorf("corrupt header")
	}
	rest := make([]byte, headerSize-32)
	if _, err := io.ReadFull(a.f, rest); err != nil {
		return err
	}
	for i := int64(0); i < count; i++ {
		e := rest[i*entrySize:]
		var en psarcEntry
		copy(en.Hash[:], e[:16])
		en.Block = be.Uint32(e[16:])
		en.Size = int64(e[20])<<32 | int64(be.Uint32(e[21:]))
		en.Offset = int64(e[25])<<32 | int64(be.Uint32(e[26:]))
		a.entries = append(a.entries, en)
	}
	lt := rest[count*entrySize:]
	for i := 0; i+1 < len(lt); i += 2 {
		a.lens = append(a.lens, uint32(be.Uint16(lt[i:])))
	}
	// file names
	if len(a.entries) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := a.extract(&a.entries[0], &buf); err != nil {
		return fmt.Errorf("reading name list: %v", err)
	}
	hashes := map[[16]byte]*psarcEntry{}
	for i := 1; i < len(a.entries); i++ {
		hashes[a.entries[i].Hash] = &a.entries[i]
	}
	a.entries[0].Name = "NamesLookUp.txt"
	for _, raw := range bytes.FieldsFunc(buf.Bytes(), func(r rune) bool { return r == 0 || r == '\n' || r == '\r' }) {
		name := strings.TrimSpace(string(raw))
		if name == "" {
			continue
		}
		for _, cand := range []string{name, "/" + name, strings.ToLower(name), strings.ToUpper(name)} {
			if e, ok := hashes[md5.Sum([]byte(cand))]; ok && e.Name == "" {
				e.Name = strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/")
				break
			}
		}
	}
	for i := range a.entries {
		if a.entries[i].Name != "" {
			a.byName[strings.ToLower(a.entries[i].Name)] = &a.entries[i]
		}
	}
	return nil
}

// Names returns every file path stored in the archive (forward slashes).
func (a *PSARC) Names() []string {
	var out []string
	for i := 1; i < len(a.entries); i++ {
		if a.entries[i].Name != "" {
			out = append(out, a.entries[i].Name)
		}
	}
	return out
}

// TotalSize is the unpacked size of every named file.
func (a *PSARC) TotalSize() int64 {
	var t int64
	for i := 1; i < len(a.entries); i++ {
		t += a.entries[i].Size
	}
	return t
}

func (a *PSARC) Has(name string) bool { _, ok := a.byName[strings.ToLower(name)]; return ok }

type capWriter struct {
	b   []byte
	max int
}

var errEnough = fmt.Errorf("enough")

func (c *capWriter) Write(p []byte) (int, error) {
	room := c.max - len(c.b)
	if len(p) >= room {
		c.b = append(c.b, p[:room]...)
		return room, errEnough
	}
	c.b = append(c.b, p...)
	return len(p), nil
}

// Head returns the first n bytes of a stored file (without unpacking all of it).
func (a *PSARC) Head(name string, n int) ([]byte, error) {
	e, ok := a.byName[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("%s is not in %s", name, filepath.Base(a.Path))
	}
	if int64(n) > e.Size {
		n = int(e.Size)
	}
	w := &capWriter{max: n}
	if err := a.extract(e, w); err != nil && err != errEnough {
		return nil, err
	}
	return w.b, nil
}

// ExtractTo writes one stored file to dst.
func (a *PSARC) ExtractTo(name, dst string) error {
	e, ok := a.byName[strings.ToLower(name)]
	if !ok {
		return fmt.Errorf("%s is not in %s", name, filepath.Base(a.Path))
	}
	return writeAtomic(dst, func(w io.Writer) error { return a.extract(e, w) })
}

func (a *PSARC) extract(e *psarcEntry, w io.Writer) error {
	if e.Size == 0 {
		return nil
	}
	var magic [2]byte
	if _, err := a.f.ReadAt(magic[:], e.Offset); err != nil {
		return err
	}
	m := binary.LittleEndian.Uint16(magic[:])
	if m != 0x068C && m != 0xDA78 && m&0xFF != 0x8C && m&0xFF != 0xCC && m&0xFF != 0x78 {
		// stored without compression
		_, err := io.Copy(w, io.NewSectionReader(a.f, e.Offset, e.Size))
		return err
	}
	off := e.Offset
	remain := e.Size
	blk := int64(e.Block)
	src := make([]byte, a.blockSize)
	dst := make([]byte, a.blockSize)
	for remain > 0 {
		if blk >= int64(len(a.lens)) {
			return fmt.Errorf("block table overrun")
		}
		clen := int64(a.lens[blk])
		if clen == 0 {
			clen = a.blockSize
		}
		want := a.blockSize
		if remain < want {
			want = remain
		}
		if _, err := a.f.ReadAt(src[:clen], off); err != nil {
			return err
		}
		if clen == want {
			if _, err := w.Write(src[:clen]); err != nil {
				return err
			}
		} else {
			if err := a.inflate(src[:clen], dst[:want]); err != nil {
				return fmt.Errorf("%s block %d: %v", e.Name, blk, err)
			}
			if _, err := w.Write(dst[:want]); err != nil {
				return err
			}
		}
		off += clen
		remain -= want
		blk++
	}
	return nil
}

func (a *PSARC) inflate(src, dst []byte) error {
	if len(src) >= 2 && src[0] == 0x78 && (a.comp == "zlib" || a.oodle == nil) {
		r, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			return err
		}
		defer r.Close()
		_, err = io.ReadFull(r, dst)
		return err
	}
	if a.oodle == nil {
		return fmt.Errorf("Oodle decompressor not available")
	}
	return a.oodle(src, dst)
}

// Unpack writes every file into dir. Existing files are left alone.
// It returns the list of files it created. progress gets bytes done/total.
func (a *PSARC) Unpack(dir string, progress func(done, total int64, name string)) ([]string, error) {
	total := a.TotalSize()
	var done int64
	var created []string
	for i := 0; i < len(a.entries); i++ { // entry 0 = NamesLookUp.txt, written only if missing
		e := &a.entries[i]
		if e.Name == "" {
			done += e.Size
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(e.Name))
		if _, err := os.Stat(dst); err == nil {
			done += e.Size
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return created, err
		}
		err := writeAtomic(dst, func(w io.Writer) error { return a.extract(e, w) })
		if err != nil {
			return created, err
		}
		created = append(created, e.Name)
		done += e.Size
		if progress != nil {
			progress(done, total, e.Name)
		}
	}
	return created, nil
}

// writeAtomic writes through a temp file and renames it into place.
func writeAtomic(dst string, fill func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	tmp := dst + ".tmp-modinst"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	err = fill(bw)
	if err == nil {
		err = bw.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(dst)
	return os.Rename(tmp, dst)
}
