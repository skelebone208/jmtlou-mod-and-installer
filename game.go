package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const stateDirName = "_modinstaller"

type Game struct {
	Root  string // folder with the game .exe
	Main  string // Root\build\pc\main
	ID    string // "tlou1" or "tlou2"
	Title string
	Exe   string
	oodle DecompressFunc
}

var gameTitles = map[string]string{
	"tlou1": "The Last of Us Part I",
	"tlou2": "The Last of Us Part II Remastered",
}

// FindGame accepts the game folder or any folder inside / just above it.
func FindGame(start string) (*Game, error) {
	start = strings.TrimSpace(strings.Trim(start, `"`))
	if start == "" {
		return nil, fmt.Errorf("choose the game folder first")
	}
	cands := []string{}
	p := start
	for i := 0; i < 6; i++ {
		cands = append(cands, p)
		np := filepath.Dir(p)
		if np == p {
			break
		}
		p = np
	}
	// also look one level down (e.g. the user picked steamapps\common)
	if ents, err := os.ReadDir(start); err == nil {
		for _, e := range ents {
			if e.IsDir() && strings.Contains(strings.ToLower(e.Name()), "last of us") {
				cands = append(cands, filepath.Join(start, e.Name()))
			}
		}
	}
	for _, dir := range cands {
		if g := gameAt(dir); g != nil {
			return g, nil
		}
	}
	return nil, fmt.Errorf("couldn't find The Last of Us Part I or Part II in\n%s\n\nPick the folder that contains tlou-i.exe or tlou-ii.exe", start)
}

func gameAt(dir string) *Game {
	main := filepath.Join(dir, "build", "pc", "main")
	if st, err := os.Stat(main); err != nil || !st.IsDir() {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var exe string
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if strings.HasPrefix(n, "tlou") && strings.HasSuffix(n, ".exe") {
			exe = e.Name()
			if n == "tlou-i.exe" || n == "tlou-ii.exe" {
				break
			}
		}
	}
	id := ""
	low := strings.ToLower(exe + " " + filepath.Base(dir))
	switch {
	case strings.Contains(strings.ToLower(exe), "tlou-ii") || strings.Contains(low, "part ii"):
		id = "tlou2"
	case exe != "" || strings.Contains(low, "part i"):
		id = "tlou1"
	default:
		return nil
	}
	return &Game{Root: dir, Main: main, ID: id, Title: gameTitles[id], Exe: exe}
}

// SteamGameGuesses returns likely install folders that exist on this PC.
func SteamGameGuesses() map[string]string {
	found := map[string]string{}
	libs := []string{}
	for _, base := range []string{`C:\Program Files (x86)\Steam`, `C:\Program Files\Steam`} {
		libs = append(libs, base)
		if b, err := os.ReadFile(filepath.Join(base, "steamapps", "libraryfolders.vdf")); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				f := strings.Fields(strings.TrimSpace(line))
				if len(f) >= 2 && strings.Trim(f[0], `"`) == "path" {
					p := strings.Trim(strings.Join(f[1:], " "), `"`)
					libs = append(libs, strings.ReplaceAll(p, `\\`, `\`))
				}
			}
		}
	}
	for c := 'C'; c <= 'Z'; c++ {
		libs = append(libs, string(c)+`:\SteamLibrary`)
	}
	for _, lib := range libs {
		for _, name := range []string{"The Last of Us Part I", "The Last of Us Part II", "The Last of Us Part II Remastered"} {
			if g := gameAt(filepath.Join(lib, "steamapps", "common", name)); g != nil {
				if _, ok := found[g.ID]; !ok {
					found[g.ID] = g.Root
				}
			}
		}
	}
	return found
}

func (g *Game) stateDir() string { return filepath.Join(g.Main, stateDirName) }

// ---- archives ----

type archiveInfo struct {
	File   string // file name in Main, e.g. sp-common.psarc or sp-common.psarc.bak
	Base   string // sp-common.psarc
	Active bool   // still being read by the game (not renamed)
}

func (g *Game) archives() []archiveInfo {
	ents, _ := os.ReadDir(g.Main)
	var out []archiveInfo
	for _, e := range ents {
		n := e.Name()
		ln := strings.ToLower(n)
		switch {
		case strings.HasSuffix(ln, ".psarc"):
			out = append(out, archiveInfo{File: n, Base: n, Active: true})
		case strings.HasSuffix(ln, ".psarc.bak"):
			out = append(out, archiveInfo{File: n, Base: n[:len(n)-4], Active: false})
		}
	}
	return out
}

func (g *Game) open(file string) (*PSARC, error) {
	if g.oodle == nil {
		dec, err := loadOodle(g.Root)
		if err != nil {
			return nil, err
		}
		g.oodle = dec
	}
	return OpenPSARC(filepath.Join(g.Main, file), g.oodle)
}

// Index of every game file: archive path -> which archive holds it.
type GameIndex struct {
	where    map[string]archiveInfo // lower path -> archive
	byBase   map[string][]string    // lower basename -> paths
	topDirs  map[string]bool
	archives []archiveInfo
}

func (g *Game) Index(log func(string)) (*GameIndex, error) {
	ix := &GameIndex{where: map[string]archiveInfo{}, byBase: map[string][]string{}, topDirs: map[string]bool{}}
	ix.archives = g.archives()
	// archives: prefer the .bak (already unpacked) entry when both exist
	sort.Slice(ix.archives, func(i, j int) bool { return !ix.archives[i].Active && ix.archives[j].Active })
	for _, ai := range ix.archives {
		a, err := g.open(ai.File)
		if err != nil {
			if log != nil {
				log("  (skipped " + ai.File + ": " + err.Error() + ")")
			}
			continue
		}
		for _, n := range a.Names() {
			ln := strings.ToLower(n)
			if _, dup := ix.where[ln]; !dup {
				ix.where[ln] = ai
				ix.add(n)
			}
		}
		a.Close()
	}
	// loose files already in the game folder
	filepath.WalkDir(g.Main, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == stateDirName {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(g.Main, p)
			rel = filepath.ToSlash(rel)
			if strings.Contains(rel, "/") {
				if _, ok := ix.where[strings.ToLower(rel)]; !ok {
					ix.add(rel)
				}
			}
		}
		return nil
	})
	return ix, nil
}

func (ix *GameIndex) add(p string) {
	lp := strings.ToLower(p)
	b := lp[strings.LastIndex(lp, "/")+1:]
	ix.byBase[b] = append(ix.byBase[b], p)
	if i := strings.Index(lp, "/"); i > 0 {
		ix.topDirs[lp[:i]] = true
	}
}

// ---- state ----

type FileRec struct {
	Path    string `json:"path"`              // relative to build\pc\main, forward slashes
	Archive string `json:"archive,omitempty"` // original can be re-extracted from this archive
	Backup  string `json:"backup,omitempty"`  // or copied back from this backup (in state dir)
	Created bool   `json:"created,omitempty"` // or simply deleted
}

type ModRec struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Version string    `json:"version,omitempty"`
	Author  string    `json:"author,omitempty"`
	Files    []FileRec  `json:"files"`
	Textures []TexPatch `json:"textures,omitempty"`
}

type SetupRec struct {
	Archive string `json:"archive"`           // e.g. sp-common.psarc
	Log     string `json:"log,omitempty"`     // list of files the installer unpacked
	Adopted bool   `json:"adopted,omitempty"` // was already unpacked before this tool
}

type State struct {
	Version int                `json:"version"`
	Mods    map[string]*ModRec `json:"mods"`
	Setup   []SetupRec         `json:"setup"`
}

func (g *Game) LoadState() *State {
	s := &State{Version: 1, Mods: map[string]*ModRec{}}
	if b, err := os.ReadFile(filepath.Join(g.stateDir(), "state.json")); err == nil {
		json.Unmarshal(b, s)
	}
	if s.Mods == nil {
		s.Mods = map[string]*ModRec{}
	}
	return s
}

func (g *Game) SaveState(s *State) error {
	if err := os.MkdirAll(g.stateDir(), 0755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	p := filepath.Join(g.stateDir(), "state.json")
	if err := os.WriteFile(p+".new", b, 0644); err != nil {
		return err
	}
	os.Remove(p)
	return os.Rename(p+".new", p)
}

func (s *State) owner(path string) string {
	lp := strings.ToLower(path)
	for id, m := range s.Mods {
		for _, f := range m.Files {
			if strings.ToLower(f.Path) == lp {
				return id
			}
		}
	}
	return ""
}

func (s *State) hasSetup(base string) bool {
	for _, r := range s.Setup {
		if strings.EqualFold(r.Archive, base) {
			return true
		}
	}
	return false
}
