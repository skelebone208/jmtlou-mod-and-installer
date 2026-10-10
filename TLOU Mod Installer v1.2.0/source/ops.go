package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type UI interface {
	Log(string)
	Progress(done, total int64)
	Confirm(title, msg string) bool
}

func gb(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

// prepare checks the game isn't running and returns state.
func (g *Game) prepare(ui UI) (*State, error) {
	if g.Exe != "" && processRunning(g.Exe) {
		return nil, fmt.Errorf("%s is running. Close the game first, then try again.", g.Title)
	}
	s := g.LoadState()
	// record unpacking that was done before this tool existed (e.g. by hand)
	for _, ai := range g.archives() {
		if !ai.Active && !s.hasSetup(ai.Base) {
			s.Setup = append(s.Setup, SetupRec{Archive: ai.Base, Adopted: true})
		}
	}
	return s, nil
}

// Install puts a mod into the game.
func (g *Game) Install(m *Mod, ui UI) error {
	s, err := g.prepare(ui)
	if err != nil {
		return err
	}
	if m.Manifest.Game != "" && m.Manifest.Game != g.ID {
		t := gameTitles[m.Manifest.Game]
		if t == "" {
			t = m.Manifest.Game
		}
		return fmt.Errorf("this mod is made for %s, but the folder you picked is %s.", t, g.Title)
	}
	ui.Log("Reading the game's file list...")
	ix, err := g.Index(ui.Log)
	if err != nil {
		return err
	}
	items, skipped, err := m.Plan(ix)
	if err != nil {
		return err
	}
	for _, sk := range skipped {
		ui.Log("  ! not sure where this goes, skipped: " + sk)
	}
	if len(items) == 0 && len(m.Textures()) == 0 {
		return fmt.Errorf("couldn't find any game files in this mod.\nMod files must have the same names as the game files they replace,\nor sit in the same folders (for example actor97\\joel-head-01-u1-g1-v1.pak).")
	}
	prev := s.Mods[m.Manifest.ID]
	if prev != nil {
		ui.Log("This mod is already installed - updating it.")
	}
	// conflicts
	for _, it := range items {
		if o := s.owner(it.Dest); o != "" && o != m.Manifest.ID {
			return fmt.Errorf("%s is already changed by the mod \"%s\".\nUninstall that mod first.", it.Dest, s.Mods[o].Name)
		}
	}
	// archives that must be unpacked so the game reads loose files
	need := map[string]archiveInfo{}
	for _, it := range items {
		if ai, ok := ix.where[strings.ToLower(it.Dest)]; ok && ai.Active {
			need[ai.Base] = ai
		}
	}
	for _, ai := range sortedArchives(need) {
		if err := g.setupArchive(ai, s, ui); err != nil {
			return err
		}
	}
	// copy files
	rec := &ModRec{ID: m.Manifest.ID, Name: m.Manifest.Name, Version: m.Manifest.Version, Author: m.Manifest.Author}
	if prev != nil {
		rec.Textures = prev.Textures
	}
	old := map[string]FileRec{}
	if prev != nil {
		for _, f := range prev.Files {
			old[strings.ToLower(f.Path)] = f
		}
	}
	backupDir := filepath.Join(g.stateDir(), "backups", rec.ID)
	var total, done int64
	for _, it := range items {
		if st, e := os.Stat(it.Src); e == nil {
			total += st.Size()
		}
	}
	ui.Log(fmt.Sprintf("Installing %d file(s)...", len(items)))
	for _, it := range items {
		dst := filepath.Join(g.Main, filepath.FromSlash(it.Dest))
		fr, had := old[strings.ToLower(it.Dest)]
		if !had {
			fr = FileRec{Path: it.Dest}
			if ai, ok := ix.where[strings.ToLower(it.Dest)]; ok {
				fr.Archive = ai.Base // the untouched original lives in the game's archive
			} else if _, e := os.Stat(dst); e == nil {
				fr.Backup = filepath.ToSlash(filepath.Join("backups", rec.ID, it.Dest))
				if err := copyFile(dst, filepath.Join(backupDir, filepath.FromSlash(it.Dest))); err != nil {
					return fmt.Errorf("couldn't back up %s: %v", it.Dest, err)
				}
			} else {
				fr.Created = true
			}
		}
		delete(old, strings.ToLower(it.Dest))
		rec.Files = append(rec.Files, fr)
		if err := copyFile(it.Src, dst); err != nil {
			// save what we've done so far so it can still be uninstalled
			s.Mods[rec.ID] = rec
			g.SaveState(s)
			return fmt.Errorf("couldn't write %s: %v", it.Dest, err)
		}
		if st, e := os.Stat(it.Src); e == nil {
			done += st.Size()
		}
		ui.Progress(done, total)
		ui.Log("  + " + it.Dest)
	}
	// textures go into the game's texture dictionaries
	s.Mods[rec.ID] = rec
	if err := g.applyTextures(m, rec, s, ix, ui); err != nil {
		g.SaveState(s)
		return err
	}
	// files the previous version had but this one doesn't: put them back
	if len(old) > 0 {
		var leftovers []FileRec
		for _, f := range old {
			leftovers = append(leftovers, f)
		}
		if err := g.restoreFiles(leftovers, s, ui); err != nil {
			return err
		}
	}
	return g.SaveState(s)
}

func sortedArchives(m map[string]archiveInfo) []archiveInfo {
	var out []archiveInfo
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Base < out[j].Base })
	return out
}

// setupArchive unpacks one .psarc into loose files and renames it to .bak
// so the game loads the loose (moddable) files instead.
func (g *Game) setupArchive(ai archiveInfo, s *State, ui UI) error {
	a, err := g.open(ai.File)
	if err != nil {
		return err
	}
	defer a.Close()
	size := a.TotalSize()
	if free := freeSpace(g.Main); free > 0 && free < size+(1<<30) {
		return fmt.Errorf("one-time setup needs %s of free space on this drive to unpack %s, but only %s is free.", gb(size), ai.Base, gb(free))
	}
	if !ui.Confirm("One-time setup", fmt.Sprintf(
		"To load mods, the game's %s archive has to be unpacked once into loose files.\n\n"+
			"This uses about %s of disk space and can take a few minutes.\n"+
			"The original is kept as %s.bak, and \"Restore original game\" undoes it.\n\nContinue?", ai.Base, gb(size), ai.Base)) {
		return fmt.Errorf("cancelled")
	}
	ui.Log(fmt.Sprintf("Unpacking %s (%s) - one-time setup...", ai.Base, gb(size)))
	logPath := filepath.Join(g.stateDir(), "unpacked-"+ai.Base+".txt")
	os.MkdirAll(g.stateDir(), 0755)
	created, err := a.Unpack(g.Main, func(done, total int64, name string) { ui.Progress(done, total) })
	// always remember what was created, even on failure, so it can be cleaned
	appendLines(logPath, created)
	if err != nil {
		return fmt.Errorf("unpacking %s failed: %v", ai.Base, err)
	}
	a.Close()
	if err := os.Rename(filepath.Join(g.Main, ai.File), filepath.Join(g.Main, ai.Base+".bak")); err != nil {
		return fmt.Errorf("couldn't rename %s: %v", ai.File, err)
	}
	s.Setup = append(s.Setup, SetupRec{Archive: ai.Base, Log: filepath.Base(logPath)})
	ui.Log(fmt.Sprintf("  unpacked %d files, %s renamed to %s.bak", len(created), ai.Base, ai.Base))
	return g.SaveState(s)
}

// Uninstall removes a mod and puts the original files back.
func (g *Game) Uninstall(id string, ui UI) error {
	s, err := g.prepare(ui)
	if err != nil {
		return err
	}
	m := s.Mods[id]
	if m == nil {
		return fmt.Errorf("that mod isn't installed in %s.", g.Title)
	}
	ui.Log(fmt.Sprintf("Uninstalling \"%s\" (%d files)...", m.Name, len(m.Files)))
	if err := g.restoreFiles(m.Files, s, ui); err != nil {
		return err
	}
	if err := g.restoreTextures(m, ui); err != nil {
		return err
	}
	delete(s.Mods, id)
	os.RemoveAll(filepath.Join(g.stateDir(), "backups", id))
	return g.SaveState(s)
}

func (g *Game) restoreFiles(files []FileRec, s *State, ui UI) error {
	opened := map[string]*PSARC{}
	defer func() {
		for _, a := range opened {
			a.Close()
		}
	}()
	for i, f := range files {
		dst := filepath.Join(g.Main, filepath.FromSlash(f.Path))
		var err error
		switch {
		case f.Archive != "":
			a := opened[f.Archive]
			if a == nil {
				file := f.Archive + ".bak"
				if _, e := os.Stat(filepath.Join(g.Main, file)); e != nil {
					file = f.Archive
				}
				if a, err = g.open(file); err != nil {
					return err
				}
				opened[f.Archive] = a
			}
			err = a.ExtractTo(f.Path, dst)
		case f.Backup != "":
			err = copyFile(filepath.Join(g.stateDir(), filepath.FromSlash(f.Backup)), dst)
		default:
			err = os.Remove(dst)
			if os.IsNotExist(err) {
				err = nil
			}
			removeEmptyDirs(map[string]bool{filepath.Dir(dst): true}, g.Main)
		}
		if err != nil {
			return fmt.Errorf("couldn't restore %s: %v", f.Path, err)
		}
		ui.Log("  - " + f.Path)
		ui.Progress(int64(i+1), int64(len(files)))
	}
	return nil
}

// RestoreGame removes every mod and undoes the one-time setup.
func (g *Game) RestoreGame(ui UI) error {
	s, err := g.prepare(ui)
	if err != nil {
		return err
	}
	if len(s.Mods) > 0 {
		var names []string
		for _, m := range s.Mods {
			names = append(names, m.Name)
		}
		sort.Strings(names)
		if !ui.Confirm("Restore original game", "These mods will be uninstalled first:\n\n  "+strings.Join(names, "\n  ")+"\n\nContinue?") {
			return fmt.Errorf("cancelled")
		}
		for id := range s.Mods {
			if err := g.Uninstall(id, ui); err != nil {
				return err
			}
		}
		if s, err = g.prepare(ui); err != nil {
			return err
		}
	}
	if len(s.Setup) == 0 {
		ui.Log("The game is already in its original state.")
		return nil
	}
	for _, r := range s.Setup {
		bak := filepath.Join(g.Main, r.Archive+".bak")
		orig := filepath.Join(g.Main, r.Archive)
		var list []string
		if r.Log != "" {
			list = readLines(filepath.Join(g.stateDir(), r.Log))
		} else if a, err := g.open(r.Archive + ".bak"); err == nil {
			list = a.Names() // unpacked by hand earlier: remove what the archive contains
			a.Close()
		}
		ui.Log(fmt.Sprintf("Removing %d unpacked files from %s...", len(list), r.Archive))
		dirs := map[string]bool{}
		for i, p := range list {
			full := filepath.Join(g.Main, filepath.FromSlash(p))
			os.Remove(full)
			dirs[filepath.Dir(full)] = true
			if i%200 == 0 {
				ui.Progress(int64(i), int64(len(list)))
			}
		}
		removeEmptyDirs(dirs, g.Main)
		if _, err := os.Stat(bak); err == nil {
			if _, err := os.Stat(orig); err == nil {
				os.Remove(orig) // e.g. Steam re-downloaded it; the .bak is the same file
			}
			if err := os.Rename(bak, orig); err != nil {
				return fmt.Errorf("couldn't rename %s back: %v", r.Archive, err)
			}
		}
		ui.Log("  restored " + r.Archive)
	}
	s.Setup = nil
	os.RemoveAll(g.stateDir())
	return nil
}

func removeEmptyDirs(dirs map[string]bool, stop string) {
	var list []string
	for d := range dirs {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, d := range list {
		for d != stop && strings.HasPrefix(d, stop) {
			if os.Remove(d) != nil {
				break
			}
			d = filepath.Dir(d)
		}
	}
}

func appendLines(path string, lines []string) {
	if len(lines) == 0 {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(strings.Join(lines, "\n") + "\n")
}

func readLines(path string) []string {
	b, _ := os.ReadFile(path)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Describe returns a short status for the picked folder.
func (g *Game) Describe() string {
	s := g.LoadState()
	ready := 0
	for _, ai := range g.archives() {
		if !ai.Active {
			ready++
		}
	}
	msg := "Found: " + g.Title
	if len(s.Mods) == 0 {
		msg += "  -  no mods installed"
	} else {
		msg += fmt.Sprintf("  -  %d mod(s) installed", len(s.Mods))
	}
	if ready > 0 {
		msg += "  -  set up for loose files"
	}
	return msg
}

// FreeUnused undoes the one-time unpack of every archive that no installed
// mod needs any more, giving the disk space back.
func (g *Game) FreeUnused(ui UI) error {
	s, err := g.prepare(ui)
	if err != nil {
		return err
	}
	if len(s.Setup) == 0 {
		ui.Log("Nothing to free up: no archives are unpacked.")
		return nil
	}
	ui.Log("Checking which unpacked archives are still needed...")
	ix, err := g.Index(nil)
	if err != nil {
		return err
	}
	need := map[string]bool{}
	for _, m := range s.Mods {
		for _, f := range m.Files {
			if f.Archive != "" {
				need[strings.ToLower(f.Archive)] = true
			}
		}
		for _, t := range m.Textures {
			if ai, ok := ix.where[strings.ToLower(t.Dict)]; ok {
				need[strings.ToLower(ai.Base)] = true
			}
		}
	}
	var drop, keep []SetupRec
	var total int64
	for _, r := range s.Setup {
		if need[strings.ToLower(r.Archive)] {
			keep = append(keep, r)
			continue
		}
		drop = append(drop, r)
		if a, err := g.open(r.Archive + ".bak"); err == nil {
			total += a.TotalSize()
			a.Close()
		}
	}
	if len(drop) == 0 {
		ui.Log("Every unpacked archive is used by an installed mod - nothing to free.")
		return nil
	}
	var names []string
	for _, r := range drop {
		names = append(names, r.Archive)
	}
	if !ui.Confirm("Free up space", fmt.Sprintf("These archives are unpacked but no installed mod needs them:\n\n  %s\n\nRe-pack them to free about %s?", strings.Join(names, "\n  "), gb(total))) {
		return fmt.Errorf("cancelled")
	}
	for _, r := range drop {
		if err := g.undoSetup(r, ui); err != nil {
			return err
		}
	}
	s.Setup = keep
	return g.SaveState(s)
}

// undoSetup deletes the files an archive's unpack created and renames the archive back.
func (g *Game) undoSetup(r SetupRec, ui UI) error {
	bak := filepath.Join(g.Main, r.Archive+".bak")
	orig := filepath.Join(g.Main, r.Archive)
	var list []string
	if r.Log != "" {
		list = readLines(filepath.Join(g.stateDir(), r.Log))
	} else if a, err := g.open(r.Archive + ".bak"); err == nil {
		list = a.Names()
		a.Close()
	}
	ui.Log(fmt.Sprintf("Removing %d unpacked files from %s...", len(list), r.Archive))
	dirs := map[string]bool{}
	for i, p := range list {
		full := filepath.Join(g.Main, filepath.FromSlash(p))
		os.Remove(full)
		dirs[filepath.Dir(full)] = true
		if i%200 == 0 {
			ui.Progress(int64(i), int64(len(list)))
		}
	}
	removeEmptyDirs(dirs, g.Main)
	if _, err := os.Stat(bak); err == nil {
		if _, err := os.Stat(orig); err == nil {
			os.Remove(orig)
		}
		if err := os.Rename(bak, orig); err != nil {
			return fmt.Errorf("couldn't rename %s back: %v", r.Archive, err)
		}
	}
	if r.Log != "" {
		os.Remove(filepath.Join(g.stateDir(), r.Log))
	}
	ui.Log("  re-packed " + r.Archive)
	return nil
}
