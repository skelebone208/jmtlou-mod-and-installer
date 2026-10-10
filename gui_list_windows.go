//go:build windows

package main

// "Installed mods" window: lists every mod installed in the selected game and lets the user
// uninstall one or several of them without having to find the original mod file again.

import (
	"fmt"
	"sort"
	"syscall"
	"unsafe"
)

var (
	pDestroyWindow      = user32.NewProc("DestroyWindow")
	pSetForegroundWin   = user32.NewProc("SetForegroundWindow")
	pGetWindowRect      = user32.NewProc("GetWindowRect")
	hwndList, hListBox  uintptr
	listIDs             []string
	listClassRegistered bool
)

const (
	wsPopup         = 0x80000000
	wmClose         = 0x0010
	lbsNotify       = 0x0001
	lbsNoIntegral   = 0x0100
	lbsExtendedSel  = 0x0800
	lbAddString     = 0x0180
	lbGetSelCount   = 0x0190
	lbGetSelItems   = 0x0191
	lbnDblClk       = 2
	idListBox       = 201
	idListUninstall = 202
	idListClose     = 203
)

func createIn(parent uintptr, class, text string, style, exStyle uint32, x, y, w, h, id int) uintptr {
	hw, _, _ := pCreateWindowEx.Call(uintptr(exStyle), uintptr(unsafe.Pointer(u16(class))), uintptr(unsafe.Pointer(u16(text))),
		uintptr(style|wsChild|wsVisible), uintptr(sc(x)), uintptr(sc(y)), uintptr(sc(w)), uintptr(sc(h)), parent, uintptr(id), 0, 0)
	sendMsg(hw, wmSetFont, hFont, 1)
	return hw
}

func showInstalledMods() {
	if hwndList != 0 {
		pSetForegroundWin.Call(hwndList)
		return
	}
	g, err := FindGame(getText(hGameEdit))
	if err != nil {
		msgBox(err.Error(), appTitle, mbIconError)
		return
	}
	s := g.LoadState()
	if len(s.Mods) == 0 {
		msgBox("No mods are installed in "+g.Title+".", appTitle, mbIconInfo)
		return
	}
	if !listClassRegistered {
		bg, _, _ := pGetSysColorBrush.Call(15)
		cursor, _, _ := pLoadCursor.Call(0, 32512)
		cls := wndClassEx{WndProc: syscall.NewCallback(listProc), Cursor: cursor, Background: bg, ClassName: u16("TLOUModInstallerList")}
		cls.Size = uint32(unsafe.Sizeof(cls))
		pRegisterClassEx.Call(uintptr(unsafe.Pointer(&cls)))
		listClassRegistered = true
	}
	style := uint32(wsOverlapped | wsCaption | wsSysMenu)
	r := rect{0, 0, sc(520), sc(340)}
	pAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), uintptr(style), 0, 0)
	w, h := r.R-r.L, r.B-r.T
	var mr rect
	pGetWindowRect.Call(hwndMain, uintptr(unsafe.Pointer(&mr)))
	x, y := mr.L+(mr.R-mr.L-w)/2, mr.T+(mr.B-mr.T-h)/2
	hwndList, _, _ = pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(u16("TLOUModInstallerList"))),
		uintptr(unsafe.Pointer(u16("Installed mods - "+g.Title))), uintptr(style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), hwndMain, 0, 0, 0)

	lbl := createIn(hwndList, "STATIC", "Select one or more mods (Ctrl/Shift-click) and press Uninstall.", 0, 0, 16, 12, 488, 20, 0)
	sendMsg(lbl, wmSetFont, hFontBold, 1)
	hListBox = createIn(hwndList, "LISTBOX", "", wsTabStop|wsVScroll|lbsNotify|lbsNoIntegral|lbsExtendedSel, wsExClientEdge, 16, 38, 488, 240, idListBox)
	createIn(hwndList, "BUTTON", "Uninstall selected", wsTabStop, 0, 16, 290, 160, 34, idListUninstall)
	createIn(hwndList, "BUTTON", "Close", wsTabStop|bsDefPush, 0, 404, 290, 100, 34, idListClose)

	listIDs = listIDs[:0]
	for id := range s.Mods {
		listIDs = append(listIDs, id)
	}
	sort.Slice(listIDs, func(i, j int) bool { return s.Mods[listIDs[i]].Name < s.Mods[listIDs[j]].Name })
	for _, id := range listIDs {
		m := s.Mods[id]
		line := m.Name
		if m.Version != "" {
			line += "  v" + m.Version
		}
		if m.Author != "" {
			line += "  by " + m.Author
		}
		line += fmt.Sprintf("    (%d files", len(m.Files))
		if len(m.Textures) > 0 {
			line += fmt.Sprintf(", %d textures", len(m.Textures))
		}
		line += ")"
		sendMsg(hListBox, lbAddString, 0, uintptr(unsafe.Pointer(u16(line))))
	}
	pEnableWindow.Call(hwndMain, 0) // behaves like a dialog
	pShowWindow.Call(hwndList, 1)
	pUpdateWindow.Call(hwndList)
}

func closeInstalledMods() {
	if hwndList == 0 {
		return
	}
	pEnableWindow.Call(hwndMain, 1)
	h := hwndList
	hwndList = 0
	pDestroyWindow.Call(h)
	pSetForegroundWin.Call(hwndMain)
}

func selectedListIDs() []string {
	n := int(sendMsg(hListBox, lbGetSelCount, 0, 0))
	if n <= 0 {
		return nil
	}
	idx := make([]int32, n)
	sendMsg(hListBox, lbGetSelItems, uintptr(n), uintptr(unsafe.Pointer(&idx[0])))
	var out []string
	for _, i := range idx {
		if int(i) < len(listIDs) {
			out = append(out, listIDs[i])
		}
	}
	return out
}

func listProc(hwnd uintptr, msg uint32, wp, lp uintptr) (ret uintptr) {
	defer func() {
		if r := recover(); r != nil {
			winUI{}.Log(fmt.Sprintf("Unexpected error: %v", r))
			ret = 0
		}
	}()
	switch msg {
	case wmCommand:
		id, code := int(wp&0xFFFF), int(wp>>16)
		switch {
		case id == idListUninstall && code == 0, id == idListBox && code == lbnDblClk:
			ids := selectedListIDs()
			if len(ids) == 0 {
				r, _, _ := pMessageBox.Call(hwnd, uintptr(unsafe.Pointer(u16("Select a mod in the list first."))), uintptr(unsafe.Pointer(u16(appTitle))), mbIconInfo)
				_ = r
				return 0
			}
			q := fmt.Sprintf("Uninstall %d mod(s)? The original game files will be put back.", len(ids))
			if len(ids) == 1 {
				if g, err := FindGame(getText(hGameEdit)); err == nil {
					if m := g.LoadState().Mods[ids[0]]; m != nil {
						q = "Uninstall \"" + m.Name + "\"? The original game files will be put back."
					}
				}
			}
			r, _, _ := pMessageBox.Call(hwnd, uintptr(unsafe.Pointer(u16(q))), uintptr(unsafe.Pointer(u16(appTitle))), mbYesNo|mbIconQuestion)
			if int(r) != idYes {
				return 0
			}
			closeInstalledMods()
			run(func(ui UI) error { return uninstallIDs(ui, ids) })
		case id == idListClose && code == 0:
			closeInstalledMods()
		}
		return 0
	case wmClose:
		closeInstalledMods()
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

// uninstallIDs removes the given mods from the selected game (worker goroutine).
func uninstallIDs(ui UI, ids []string) error {
	g, err := currentGame()
	if err != nil {
		return err
	}
	s := g.LoadState()
	done := 0
	for _, id := range ids {
		m := s.Mods[id]
		if m == nil {
			continue
		}
		if err := g.Uninstall(id, ui); err != nil {
			return err
		}
		ui.Log("Done! \"" + m.Name + "\" was removed and the original files are back.\r\n")
		done++
	}
	if done > 0 {
		ui.Progress(1, 1)
		msgBox("Uninstalled. The original game files are back.", appTitle, mbIconInfo)
	}
	return nil
}
