package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// Manifest is the optional mod.json a mod author puts in their archive.
type Manifest struct {
	Format      int    `json:"format"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Game        string `json:"game"`  // "tlou1" or "tlou2"
	Files       string `json:"files"` // folder (inside the archive) that mirrors build\pc\main; default "files"
}

type Mod struct {
	Source   string
	Dir      string // extracted copy
	Manifest Manifest
	HasJSON  bool
	tmp      string
}

func (m *Mod) Close() {
	if m.tmp != "" {
		os.RemoveAll(m.tmp)
	}
}

var idClean = regexp.MustCompile(`[^a-z0-9._-]+`)

// LoadMod unpacks a .zip/.rar/.7z (or takes a single file / folder) into a temp folder.
func LoadMod(src string) (*Mod, error) {
	src = strings.TrimSpace(strings.Trim(src, `"`))
	st, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("can't open the mod file:\n%v", err)
	}
	m := &Mod{Source: src}
	if !st.IsDir() && strings.EqualFold(filepath.Base(src), "mod.json") {
		src = filepath.Dir(src) // an unzipped mod: use its folder
		m.Source = src
		st, _ = os.Stat(src)
	}
	if st.IsDir() {
		m.Dir = src
	} else {
		m.tmp, err = os.MkdirTemp("", "tloumod-")
		if err != nil {
			return nil, err
		}
		m.Dir = m.tmp
		ext := strings.ToLower(filepath.Ext(src))
		switch ext {
		case ".zip":
			err = unzip(src, m.Dir)
		case ".rar":
			err = unrar(src, m.Dir)
		case ".7z", ".tar", ".gz", ".tgz", ".xz":
			err = unSystemArchive(src, m.Dir)
		default:
			err = copyFile(src, filepath.Join(m.Dir, filepath.Base(src)))
		}
		if err != nil {
			m.Close()
			return nil, err
		}
	}
	// manifest
	var mpath string
	filepath.WalkDir(m.Dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(d.Name(), "mod.json") {
			if mpath == "" || len(p) < len(mpath) {
				mpath = p
			}
		}
		return nil
	})
	if mpath != "" {
		b, err := os.ReadFile(mpath)
		if err != nil {
			return nil, err
		}
		b = []byte(strings.TrimPrefix(string(b), "\ufeff"))
		if err := json.Unmarshal(b, &m.Manifest); err != nil {
			m.Close()
			return nil, fmt.Errorf("mod.json has a mistake in it:\n%v", err)
		}
		m.HasJSON = true
		m.Dir = filepath.Dir(mpath)
	}
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	if m.Manifest.Name == "" {
		m.Manifest.Name = base
	}
	id := m.Manifest.ID
	if id == "" {
		id = m.Manifest.Name
	}
	m.Manifest.ID = strings.Trim(idClean.ReplaceAllString(strings.ToLower(id), "-"), "-")
	if m.Manifest.ID == "" {
		m.Manifest.ID = "mod"
	}
	m.Manifest.Game = strings.ToLower(strings.TrimSpace(m.Manifest.Game))
	return m, nil
}

// planned copy: mod file -> game path
type planItem struct {
	Src  string
	Dest string // relative to build\pc\main, forward slashes
}

var ignoreExt = map[string]bool{".txt": true, ".md": true, ".json": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".url": true, ".pdf": true, ".bat": true, ".exe": true, ".ini": true, ".bmp": true, ".html": true, ".tex": true}

// Plan works out where each file in the mod goes.
func (m *Mod) Plan(ix *GameIndex) (items []planItem, skipped []string, err error) {
	root := m.Dir
	if m.HasJSON {
		sub := m.Manifest.Files
		if sub == "" {
			sub = "files"
		}
		if st, e := os.Stat(filepath.Join(m.Dir, sub)); e == nil && st.IsDir() {
			root = filepath.Join(m.Dir, sub)
		}
	}
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		dest := resolveDest(rel, ix)
		if dest == "" {
			if root == m.Dir && (ignoreExt[strings.ToLower(filepath.Ext(p))] || strings.EqualFold(d.Name(), "mod.json")) {
				return nil // readme, screenshots...
			}
			skipped = append(skipped, rel)
			return nil
		}
		if seen[strings.ToLower(dest)] {
			skipped = append(skipped, rel+" (duplicate of another file in the mod)")
			return nil
		}
		seen[strings.ToLower(dest)] = true
		items = append(items, planItem{Src: p, Dest: dest})
		return nil
	})
	return
}

// resolveDest maps a path inside the mod to a path in build\pc\main.
func resolveDest(rel string, ix *GameIndex) string {
	parts := strings.Split(rel, "/")
	// "anything/build/pc/main/actor97/x.pak" -> "actor97/x.pak"
	for i := 0; i+2 < len(parts); i++ {
		if strings.EqualFold(parts[i], "build") && strings.EqualFold(parts[i+1], "pc") && strings.EqualFold(parts[i+2], "main") {
			if i+3 < len(parts) {
				return strings.Join(parts[i+3:], "/")
			}
		}
	}
	// "My Mod/actor97/x.pak" -> "actor97/x.pak" (a folder the game uses)
	for i := 0; i < len(parts)-1; i++ {
		if ix.topDirs[strings.ToLower(parts[i])] {
			return strings.Join(parts[i:], "/")
		}
	}
	// a loose file: find it in the game by name
	if c := ix.byBase[strings.ToLower(parts[len(parts)-1])]; len(c) == 1 {
		return c[0]
	}
	return ""
}

// ---- archive helpers ----

func safeJoin(dir, name string) (string, error) {
	name = filepath.FromSlash(strings.ReplaceAll(name, "\\", "/"))
	p := filepath.Join(dir, name)
	if !strings.HasPrefix(p, filepath.Clean(dir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe path in archive: %s", name)
	}
	return p, nil
}

func unzip(src, dir string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("can't read the zip file: %v", err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		p, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeAtomic(p, func(w io.Writer) error { _, e := io.Copy(w, rc); return e })
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func unrar(src, dir string) error {
	r, err := rardecode.OpenReader(src)
	if err != nil {
		return fmt.Errorf("can't read the rar file: %v", err)
	}
	defer r.Close()
	for {
		h, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("can't read the rar file: %v", err)
		}
		if h.IsDir {
			continue
		}
		p, err := safeJoin(dir, h.Name)
		if err != nil {
			return err
		}
		if err := writeAtomic(p, func(w io.Writer) error { _, e := io.Copy(w, r); return e }); err != nil {
			return err
		}
	}
}

// 7z & others: Windows' own tar.exe, then 7-Zip if it is installed.
func unSystemArchive(src, dir string) error {
	tries := [][]string{}
	if sys := os.Getenv("SystemRoot"); sys != "" {
		tries = append(tries, []string{filepath.Join(sys, "System32", "tar.exe"), "-xf", src, "-C", dir})
	}
	for _, z := range []string{`C:\Program Files\7-Zip\7z.exe`, `C:\Program Files (x86)\7-Zip\7z.exe`} {
		tries = append(tries, []string{z, "x", "-y", "-o" + dir, src})
	}
	tries = append(tries, []string{"tar", "-xf", src, "-C", dir})
	var last error
	for _, t := range tries {
		if _, err := os.Stat(t[0]); err != nil && strings.Contains(t[0], string(os.PathSeparator)) {
			continue
		}
		cmd := exec.Command(t[0], t[1:]...)
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err == nil {
			ents, _ := os.ReadDir(dir)
			if len(ents) > 0 {
				return nil
			}
		}
		last = fmt.Errorf("%v %s", err, strings.TrimSpace(string(out)))
	}
	return fmt.Errorf("this Windows version can't open %s files by itself.\nInstall 7-Zip (free) or ask the mod author for a .zip.\n(%v)", filepath.Ext(src), last)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeAtomic(dst, func(w io.Writer) error { _, e := io.Copy(w, in); return e })
}
