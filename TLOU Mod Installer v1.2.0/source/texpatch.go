package main

// Texture-dictionary patching.
//
// Character textures in TLOU Part I stream from big "texture dictionary" paks
// (build\pc\main\texturedict3\*.pak), looked up by a 64-bit texture id. A mod
// can ship replacement textures in a "textures" folder; each file is written
// into the dictionary slot that holds that id, so the game (including the
// Extras viewer) loads the new image under the original id.
//
// .tex file layout (little endian):
//   "NDTX" | u32 version(1) | u64 id | u32 fmt | u32 width | u32 height | u32 mips | data...
// The data is stored exactly as the dictionary expects it and must not be
// larger than the original texture's data.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const texDictItem = 0x36125D3CFB7F3991 // TEXTURE_DICTIONARY

type TexPatch struct {
	Dict   string `json:"dict"`   // e.g. texturedict3/sp-common-dict-2.pak
	ID     string `json:"id"`     // hex texture id
	Backup string `json:"backup"` // backup file in the state dir (descriptor + original data)
}

type modTexture struct {
	Src            string
	ID             uint64
	Fmt, W, H, Mip uint32
	Data           []byte
}

func readModTexture(path string) (*modTexture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 32 || string(b[:4]) != "NDTX" {
		return nil, fmt.Errorf("%s is not a texture file", filepath.Base(path))
	}
	le := binary.LittleEndian
	return &modTexture{Src: path, ID: le.Uint64(b[8:]), Fmt: le.Uint32(b[16:]), W: le.Uint32(b[20:]),
		H: le.Uint32(b[24:]), Mip: le.Uint32(b[28:]), Data: b[32:]}, nil
}

// one texture slot inside a dictionary file
type dictSlot struct {
	Dict    string // relative path
	Desc    int64  // absolute offset of the descriptor
	DataAt  int64  // absolute offset of the data
	Size    uint32 // bytes available
}

// readDictIndex lists every texture id stored in one dictionary pak.
func readDictIndex(path string) (map[uint64]dictSlot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readDictIndexFrom(f)
}

func readDictIndexFrom(f io.ReaderAt) (map[uint64]dictSlot, error) {
	le := binary.LittleEndian
	var h [32]byte
	if _, err := f.ReadAt(h[:], 0); err != nil {
		return nil, err
	}
	hdrSize := le.Uint32(h[4:])
	loginPage, loginOff := le.Uint32(h[8:]), le.Uint32(h[12:])
	pageCt, pageTbl := le.Uint32(h[16:]), le.Uint32(h[20:])
	fixTbl := le.Uint32(h[28:])
	_ = hdrSize
	if pageCt == 0 || pageCt > 4096 {
		return nil, fmt.Errorf("bad page count")
	}
	pt := make([]byte, 12*pageCt)
	if _, err := f.ReadAt(pt, int64(pageTbl)); err != nil {
		return nil, err
	}
	pages := make([][2]uint32, pageCt)
	var headerEnd int64
	for i := range pages {
		pages[i] = [2]uint32{le.Uint32(pt[12*i:]), le.Uint32(pt[12*i+4:])}
		if e := int64(pages[i][0]) + int64(pages[i][1]); e > headerEnd {
			headerEnd = e
		}
	}
	rawStart := int64(pages[pageCt-1][0]) + int64(pages[pageCt-1][1])
	if headerEnd > 64<<20 {
		return nil, fmt.Errorf("header too large")
	}
	d := make([]byte, headerEnd)
	if _, err := f.ReadAt(d, 0); err != nil && err != io.EOF {
		return nil, err
	}
	// pointer fix-ups: address -> target page
	fx := d[fixTbl:]
	dataOff, n := le.Uint32(fx[4:]), le.Uint32(fx[8:])
	fix := make(map[int64]uint16, n)
	for i := uint32(0); i < n; i++ {
		e := d[int64(dataOff)+8*int64(i):]
		p1, p2, off := le.Uint16(e), le.Uint16(e[2:]), le.Uint32(e[4:])
		if int(p1) < len(pages) {
			fix[int64(off)+int64(pages[p1][0])] = p2
		}
	}
	ptr := func(a int64) (int64, bool) {
		pg, ok := fix[a]
		if !ok || int(pg) >= len(pages) {
			return 0, false
		}
		return int64(le.Uint64(d[a:])) + int64(pages[pg][0]), true
	}
	lt := int64(pages[loginPage][0]) + int64(loginOff)
	cnt := le.Uint32(d[lt+48:])
	out := map[uint64]dictSlot{}
	for i := uint32(0); i < cnt; i++ {
		pg, off := le.Uint32(d[lt+56+8*int64(i):]), le.Uint32(d[lt+60+8*int64(i):])
		item := int64(pages[pg][0]) + int64(off)
		if le.Uint64(d[item+32:]) != texDictItem {
			continue
		}
		num := le.Uint32(d[item+48:])
		lst, ok := ptr(item + 48 + 24)
		if !ok {
			continue
		}
		for j := uint32(0); j < num; j++ {
			p, ok := ptr(lst + 8*int64(j))
			if !ok {
				continue
			}
			base := p - 32
			off := le.Uint32(d[base+40:])
			size := le.Uint32(d[base+48:])
			id := le.Uint64(d[base+56:])
			out[id] = dictSlot{Desc: base, DataAt: rawStart + int64(off), Size: size}
		}
	}
	return out, nil
}

// texture files shipped by a mod
func (m *Mod) Textures() []string {
	var out []string
	for _, dir := range []string{filepath.Join(m.Dir, "textures"), m.Dir} {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".tex") {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
		if len(out) > 0 {
			break
		}
	}
	sort.Strings(out)
	return out
}

// applyTextures writes the mod's textures into the game's texture dictionaries.
func (g *Game) applyTextures(m *Mod, rec *ModRec, s *State, ix *GameIndex, ui UI) error {
	files := m.Textures()
	if len(files) == 0 {
		return nil
	}
	ui.Log(fmt.Sprintf("Installing %d texture(s) into the game's texture dictionaries...", len(files)))
	texs := make([]*modTexture, 0, len(files))
	want := map[uint64]bool{}
	for _, tf := range files {
		t, err := readModTexture(tf)
		if err != nil {
			return err
		}
		texs = append(texs, t)
		want[t.ID] = true
	}
	// index the dictionaries that are already loose files
	// a texture id can be stored in more than one dictionary; every copy is patched
	index := map[uint64][]dictSlot{}
	addSlot := func(id uint64, sl dictSlot) {
		for _, o := range index[id] {
			if strings.EqualFold(o.Dict, sl.Dict) {
				return
			}
		}
		index[id] = append(index[id], sl)
	}
	loose := map[string]bool{}
	dicts, _ := filepath.Glob(filepath.Join(g.Main, "texturedict3", "*.pak"))
	for _, dp := range dicts {
		rel := filepath.ToSlash(filepath.Join("texturedict3", filepath.Base(dp)))
		loose[strings.ToLower(rel)] = true
		slots, err := readDictIndex(dp)
		if err != nil {
			ui.Log("  (couldn't read " + filepath.Base(dp) + ": " + err.Error() + ")")
			continue
		}
		for id, sl := range slots {
			if want[id] {
				sl.Dict = rel
				addSlot(id, sl)
			}
		}
	}
	// textures still inside archives: peek at those dictionaries' headers, and
	// unpack only the archives that actually hold one of our textures
	missing := 0
	for id := range want {
		if len(index[id]) == 0 {
			missing++
		}
	}
	if missing > 0 {
		need := map[string]archiveInfo{}
		for p, ai := range ix.where {
			if !strings.HasPrefix(p, "texturedict3/") || !ai.Active || loose[p] {
				continue
			}
			a, err := g.open(ai.File)
			if err != nil {
				continue
			}
			head, err := a.Head(p, 64<<20)
			a.Close()
			if err != nil {
				continue
			}
			slots, err := readDictIndexFrom(bytes.NewReader(head))
			if err != nil {
				continue
			}
			for id := range slots {
				if want[id] {
					need[ai.Base] = ai
					break
				}
			}
		}
		for _, ai := range sortedArchives(need) {
			if err := g.setupArchive(ai, s, ui); err != nil {
				return err
			}
		}
		dicts, _ = filepath.Glob(filepath.Join(g.Main, "texturedict3", "*.pak"))
		for _, dp := range dicts {
			rel := filepath.ToSlash(filepath.Join("texturedict3", filepath.Base(dp)))
			if loose[strings.ToLower(rel)] {
				continue
			}
			slots, err := readDictIndex(dp)
			if err != nil {
				continue
			}
			for id, sl := range slots {
				if want[id] {
					sl.Dict = rel
					addSlot(id, sl)
				}
			}
		}
	}
	owned := map[string]string{}
	for id, mr := range s.Mods {
		for _, t := range mr.Textures {
			owned[strings.ToLower(t.ID)] = id
		}
	}
	backupDir := filepath.Join(g.stateDir(), "backups", rec.ID, "textures")
	os.MkdirAll(backupDir, 0755)
	done, copies := 0, 0
	for i, t := range texs {
		idHex := fmt.Sprintf("%016X", t.ID)
		if o := owned[strings.ToLower(idHex)]; o != "" && o != rec.ID {
			return fmt.Errorf("texture %s is already changed by the mod \"%s\".\nUninstall that mod first.", idHex, s.Mods[o].Name)
		}
		slots := index[t.ID]
		if len(slots) == 0 {
			ui.Log("  ! texture " + idHex + " isn't in this game's texture dictionaries, skipped")
			continue
		}
		for _, sl := range slots {
			if err := writeTexture(g, t, sl, idHex, rec, backupDir); err != nil {
				return err
			}
			copies++
		}
		done++
		ui.Progress(int64(i+1), int64(len(files)))
	}
	if copies > done {
		ui.Log(fmt.Sprintf("  (%d texture copies in total: some textures are stored in more than one dictionary)", copies))
	}
	ui.Log(fmt.Sprintf("  %d texture(s) installed", done))
	return nil
}

// restoreTextures puts the original dictionary bytes back.
func (g *Game) restoreTextures(rec *ModRec, ui UI) error {
	for _, t := range rec.Textures {
		b, err := os.ReadFile(filepath.Join(g.stateDir(), filepath.FromSlash(t.Backup)))
		if err != nil || len(b) < 16+112 {
			ui.Log("  ! missing backup for texture " + t.ID + " (use Restore original game if textures look wrong)")
			continue
		}
		le := binary.LittleEndian
		desc, dataAt := int64(le.Uint64(b)), int64(le.Uint64(b[8:]))
		f, err := os.OpenFile(filepath.Join(g.Main, filepath.FromSlash(t.Dict)), os.O_RDWR, 0)
		if err != nil {
			if os.IsNotExist(err) {
				continue // dictionary already restored with the archive
			}
			return err
		}
		f.WriteAt(b[16:16+112], desc)
		_, err = f.WriteAt(b[16+112:], dataAt)
		f.Close()
		if err != nil {
			return fmt.Errorf("couldn't restore texture %s: %v", t.ID, err)
		}
	}
	if len(rec.Textures) > 0 {
		ui.Log(fmt.Sprintf("  restored %d texture(s)", len(rec.Textures)))
	}
	return nil
}

// mip0Bytes is the size of the largest mip in the game's layout
// (rows padded to 256 bytes, each mip starting on a 512-byte boundary).
func mip0Bytes(fmtID, w, h uint32) int {
	var row, rows uint32
	switch fmtID {
	case 28, 29: // RGBA8
		row, rows = w*4, h
	case 71, 72, 80, 81: // BC1, BC4
		row, rows = (w+3)/4*8, (h+3)/4
	default: // BC2/3/5/7
		row, rows = (w+3)/4*16, (h+3)/4
	}
	pitch := (row + 255) / 256 * 256
	n := pitch * rows
	return int((n + 511) / 512 * 512)
}

// writeTexture puts one mod texture into one dictionary slot, keeping a backup of the original
// bytes the first time this mod touches that slot.
func writeTexture(g *Game, t *modTexture, sl dictSlot, idHex string, rec *ModRec, backupDir string) error {
	data, w, h, mips := t.Data, t.W, t.H, t.Mip
	for uint32(len(data)) > sl.Size && mips > 1 && w > 4 && h > 4 {
		// too big for the game's slot: drop the largest mip level (halves the resolution)
		n := mip0Bytes(t.Fmt, w, h)
		if n <= 0 || n >= len(data) {
			break
		}
		data = data[n:]
		w, h, mips = w/2, h/2, mips-1
	}
	if uint32(len(data)) > sl.Size {
		return fmt.Errorf("texture %s is too big (%d bytes, the game's slot holds %d)", filepath.Base(t.Src), len(data), sl.Size)
	}
	full := filepath.Join(g.Main, filepath.FromSlash(sl.Dict))
	f, err := os.OpenFile(full, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("couldn't open %s: %v", sl.Dict, err)
	}
	defer f.Close()
	already, idTaken := false, false
	for _, p := range rec.Textures {
		if strings.EqualFold(p.ID, idHex) {
			idTaken = true
			if strings.EqualFold(p.Dict, sl.Dict) {
				already = true
			}
		}
	}
	if !already { // keep the original bytes (descriptor + data)
		name := idHex + ".bak"
		if idTaken { // another dictionary's copy of this id already uses the plain name
			name = idHex + "@" + strings.TrimSuffix(filepath.Base(sl.Dict), filepath.Ext(sl.Dict)) + ".bak"
		}
		desc := make([]byte, 112)
		orig := make([]byte, sl.Size)
		f.ReadAt(desc, sl.Desc)
		if _, err := f.ReadAt(orig, sl.DataAt); err != nil {
			return fmt.Errorf("couldn't read %s: %v", sl.Dict, err)
		}
		var b bytes.Buffer
		binary.Write(&b, binary.LittleEndian, sl.Desc)
		binary.Write(&b, binary.LittleEndian, sl.DataAt)
		b.Write(desc)
		b.Write(orig)
		if err := os.WriteFile(filepath.Join(backupDir, name), b.Bytes(), 0644); err != nil {
			return err
		}
		rec.Textures = append(rec.Textures, TexPatch{Dict: sl.Dict, ID: idHex, Backup: filepath.ToSlash(filepath.Join("backups", rec.ID, "textures", name))})
	}
	// data, then the descriptor fields (size, format, mips, width, height)
	if _, err := f.WriteAt(data, sl.DataAt); err != nil {
		return fmt.Errorf("couldn't write %s: %v", sl.Dict, err)
	}
	le := binary.LittleEndian
	put := func(at int64, v uint32) { var x [4]byte; le.PutUint32(x[:], v); f.WriteAt(x[:], at) }
	put(sl.Desc+48, uint32(len(data)))
	put(sl.Desc+72, t.Fmt)
	put(sl.Desc+80, mips)
	put(sl.Desc+84, w)
	put(sl.Desc+88, h)
	return nil
}
