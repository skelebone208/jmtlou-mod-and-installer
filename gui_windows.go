//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const appTitle = "TLOU Mod Installer"
const appVersion = "1.2.0"

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")

	pRegisterClassEx   = user32.NewProc("RegisterClassExW")
	pCreateWindowEx    = user32.NewProc("CreateWindowExW")
	pDefWindowProc     = user32.NewProc("DefWindowProcW")
	pGetMessage        = user32.NewProc("GetMessageW")
	pTranslateMessage  = user32.NewProc("TranslateMessage")
	pDispatchMessage   = user32.NewProc("DispatchMessageW")
	pIsDialogMessage   = user32.NewProc("IsDialogMessageW")
	pPostQuitMessage   = user32.NewProc("PostQuitMessage")
	pSendMessage       = user32.NewProc("SendMessageW")
	pPostMessage       = user32.NewProc("PostMessageW")
	pSetWindowText     = user32.NewProc("SetWindowTextW")
	pGetWindowText     = user32.NewProc("GetWindowTextW")
	pGetWindowTextLen  = user32.NewProc("GetWindowTextLengthW")
	pEnableWindow      = user32.NewProc("EnableWindow")
	pMessageBox        = user32.NewProc("MessageBoxW")
	pLoadCursor        = user32.NewProc("LoadCursorW")
	pLoadIcon          = user32.NewProc("LoadIconW")
	pGetSysColorBrush  = user32.NewProc("GetSysColorBrush")
	pAdjustWindowRect  = user32.NewProc("AdjustWindowRectEx")
	pGetDpiForSystem   = user32.NewProc("GetDpiForSystem")
	pSetProcessDPIAwar = user32.NewProc("SetProcessDPIAware")
	pGetSystemMetrics  = user32.NewProc("GetSystemMetrics")
	pShowWindow        = user32.NewProc("ShowWindow")
	pUpdateWindow      = user32.NewProc("UpdateWindow")
	pCreateFont        = gdi32.NewProc("CreateFontW")
	pSetBkMode         = gdi32.NewProc("SetBkMode")
	pSetTextColor      = gdi32.NewProc("SetTextColor")
	pGetOpenFileName   = comdlg32.NewProc("GetOpenFileNameW")
	pSHBrowseForFolder = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDL  = shell32.NewProc("SHGetPathFromIDListW")
	pDragAcceptFiles   = shell32.NewProc("DragAcceptFiles")
	pDragQueryFile     = shell32.NewProc("DragQueryFileW")
	pDragFinish        = shell32.NewProc("DragFinish")
	pCoInitialize      = ole32.NewProc("CoInitializeEx")
	pCoTaskMemFree     = ole32.NewProc("CoTaskMemFree")
	pInitCommonCtrls   = comctl32.NewProc("InitCommonControlsEx")
)

const (
	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsVScroll      = 0x00200000
	wsBorder       = 0x00800000
	wsExClientEdge = 0x00000200
	esAutoHScroll  = 0x0080
	esMultiline    = 0x0004
	esReadOnly     = 0x0800
	esAutoVScroll  = 0x0040
	bsPushButton   = 0
	bsDefPush      = 1

	wmDestroy        = 0x0002
	wmSetFont        = 0x0030
	wmCommand        = 0x0111
	wmDropFiles      = 0x0233
	wmCtlColorStatic = 0x0138
	wmApp            = 0x8000
	emSetSel         = 0x00B1
	emReplaceSel     = 0x00C2
	emSetLimitText   = 0x00C5
	pbmSetRange32    = 0x0406
	pbmSetPos        = 0x0402

	mbOK          = 0x0
	mbYesNo       = 0x4
	mbIconInfo    = 0x40
	mbIconWarn    = 0x30
	mbIconError   = 0x10
	mbIconQuestion = 0x20
	idYes         = 6
)

const (
	idModEdit = 101 + iota
	idModBrowse
	idGameEdit
	idGameBrowse
	idInstall
	idUninstall
	idList
	idRestore
	idLog
	idStatus
	idProgress
	idHelp
	idModFolder
	idFree
)

type wndClassEx struct {
	Size, Style                        uint32
	WndProc                            uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	IconSm                             uintptr
}
type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      [2]int32
	_       uint32
}
type rect struct{ L, T, R, B int32 }

type openFileName struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	FnHook        uintptr
	TemplateName  *uint16
	PvReserved    uintptr
	DwReserved    uint32
	FlagsEx       uint32
}
type browseInfo struct {
	Owner        uintptr
	Root         uintptr
	DisplayName  *uint16
	Title        *uint16
	Flags        uint32
	Fn           uintptr
	LParam       uintptr
	Image        int32
}

var (
	hwndMain, hModEdit, hGameEdit, hLog, hStatus, hProgress uintptr
	buttons                                                 []uintptr
	busy                                                    int32
	dpi                                                     = 96
	hFont, hFontBold                                        uintptr
)

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func sc(n int) int32      { return int32(n * dpi / 96) }

func sendMsg(h uintptr, m uint32, w, l uintptr) uintptr {
	r, _, _ := pSendMessage.Call(h, uintptr(m), w, l)
	return r
}

func getText(h uintptr) string {
	n, _, _ := pGetWindowTextLen.Call(h)
	buf := make([]uint16, n+1)
	pGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}
func setText(h uintptr, s string) { pSetWindowText.Call(h, uintptr(unsafe.Pointer(u16(s)))) }

func msgBox(text, title string, flags uintptr) int {
	r, _, _ := pMessageBox.Call(hwndMain, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
	return int(r)
}

// ---- UI interface for the install engine (called from a worker goroutine) ----

type winUI struct{}

func (winUI) Log(s string) {
	s = strings.ReplaceAll(s, "\n", "\r\n") + "\r\n"
	sendMsg(hLog, emSetSel, 0xFFFFFFF, 0xFFFFFFF)
	sendMsg(hLog, emReplaceSel, 0, uintptr(unsafe.Pointer(u16(s))))
}
func (winUI) Progress(done, total int64) {
	if total <= 0 {
		return
	}
	sendMsg(hProgress, pbmSetPos, uintptr(done*1000/total), 0)
}
func (winUI) Confirm(title, msg string) bool {
	return msgBox(msg, title, mbYesNo|mbIconQuestion) == idYes
}

func create(class, text string, style uint32, exStyle uint32, x, y, w, h int, id int) uintptr {
	hw, _, _ := pCreateWindowEx.Call(uintptr(exStyle), uintptr(unsafe.Pointer(u16(class))), uintptr(unsafe.Pointer(u16(text))),
		uintptr(style|wsChild|wsVisible), uintptr(sc(x)), uintptr(sc(y)), uintptr(sc(w)), uintptr(sc(h)), hwndMain, uintptr(id), 0, 0)
	sendMsg(hw, wmSetFont, hFont, 1)
	return hw
}

func main() {
	runtime.LockOSThread()
	pSetProcessDPIAwar.Call()
	if pGetDpiForSystem.Find() == nil {
		if d, _, _ := pGetDpiForSystem.Call(); d > 0 {
			dpi = int(d)
		}
	}
	pCoInitialize.Call(0, 2)
	icc := [2]uint32{8, 0x20 | 0x4000} // ICC_PROGRESS_CLASS | ICC_STANDARD_CLASSES
	pInitCommonCtrls.Call(uintptr(unsafe.Pointer(&icc)))

	hFont, _, _ = pCreateFont.Call(uintptr(-sc(15)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16("Segoe UI"))))
	hFontBold, _, _ = pCreateFont.Call(uintptr(-sc(15)), 0, 0, 0, 700, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16("Segoe UI"))))

	cursor, _, _ := pLoadCursor.Call(0, 32512)
	var icon uintptr
	for id := uintptr(1); id < 16 && icon == 0; id++ { // icon group embedded by rsrc
		icon, _, _ = pLoadIcon.Call(getModuleHandle(), id)
	}
	bg, _, _ := pGetSysColorBrush.Call(15)            // COLOR_BTNFACE
	cls := wndClassEx{WndProc: syscall.NewCallback(wndProc), Cursor: cursor, Icon: icon, IconSm: icon, Background: bg, ClassName: u16("TLOUModInstaller")}
	cls.Size = uint32(unsafe.Sizeof(cls))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&cls)))

	style := uint32(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox)
	r := rect{0, 0, sc(640), sc(500)}
	pAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), uintptr(style), 0, 0)
	w, h := r.R-r.L, r.B-r.T
	sw, _, _ := pGetSystemMetrics.Call(0)
	sh, _, _ := pGetSystemMetrics.Call(1)
	hwndMain, _, _ = pCreateWindowEx.Call(0x10 /*WS_EX_ACCEPTFILES*/, uintptr(unsafe.Pointer(cls.ClassName)),
		uintptr(unsafe.Pointer(u16(appTitle+" "+appVersion))), uintptr(style),
		uintptr((int32(sw)-w)/2), uintptr((int32(sh)-h)/2), uintptr(w), uintptr(h), 0, 0, 0, 0)

	l1 := create("STATIC", "1.  Mod  (.zip, .rar, .7z, a game file or a mod folder - you can also drag it onto this window)", 0, 0, 16, 14, 610, 20, 0)
	sendMsg(l1, wmSetFont, hFontBold, 1)
	hModEdit = create("EDIT", "", wsTabStop|esAutoHScroll, wsExClientEdge, 16, 36, 380, 26, idModEdit)
	buttons = append(buttons, create("BUTTON", "File...", wsTabStop, 0, 404, 35, 108, 28, idModBrowse))
	buttons = append(buttons, create("BUTTON", "Folder...", wsTabStop, 0, 516, 35, 108, 28, idModFolder))
	l2 := create("STATIC", "2.  Game folder  (where tlou-i.exe or tlou-ii.exe is)", 0, 0, 16, 76, 610, 20, 0)
	sendMsg(l2, wmSetFont, hFontBold, 1)
	hGameEdit = create("EDIT", "", wsTabStop|esAutoHScroll, wsExClientEdge, 16, 98, 492, 26, idGameEdit)
	buttons = append(buttons, create("BUTTON", "Browse...", wsTabStop, 0, 516, 97, 108, 28, idGameBrowse))
	hStatus = create("STATIC", "", 0, 0, 16, 132, 610, 20, idStatus)
	buttons = append(buttons,
		create("BUTTON", "Install mod", wsTabStop|bsDefPush, 0, 16, 160, 140, 36, idInstall),
		create("BUTTON", "Uninstall mod", wsTabStop, 0, 164, 160, 140, 36, idUninstall),
		create("BUTTON", "Installed mods", wsTabStop, 0, 312, 160, 140, 36, idList),
		create("BUTTON", "Restore original game", wsTabStop, 0, 460, 160, 164, 36, idRestore),
	)
	hProgress = create("msctls_progress32", "", 0, 0, 16, 206, 608, 14, idProgress)
	sendMsg(hProgress, pbmSetRange32, 0, 1000)
	hLog = create("EDIT", "", wsVScroll|esMultiline|esReadOnly|esAutoVScroll, wsExClientEdge, 16, 228, 608, 222, idLog)
	sendMsg(hLog, emSetLimitText, 0, 0)
	buttons = append(buttons, create("BUTTON", "How do I make a mod for this?", wsTabStop, 0, 16, 458, 210, 28, idHelp))
	buttons = append(buttons, create("BUTTON", "Free up space", wsTabStop, 0, 232, 458, 120, 28, idFree))
	create("STATIC", "Free & open source. Back up your saves first.", 0, 0, 364, 464, 270, 20, 0)

	ui := winUI{}
	ui.Log(appTitle + " " + appVersion + " - for The Last of Us Part I and Part II (PC).")
	ui.Log("Pick a mod (or drag it onto this window), pick your game folder, press Install.")
	ui.Log("To remove a mod, press \"Installed mods\", select it and press Uninstall.")
	ui.Log("")

	// remembered / detected game folder
	if last := loadSetting("game"); last != "" {
		setText(hGameEdit, last)
	} else if guesses := SteamGameGuesses(); len(guesses) > 0 {
		if p, ok := guesses["tlou1"]; ok {
			setText(hGameEdit, p)
		} else {
			setText(hGameEdit, guesses["tlou2"])
		}
	}
	refreshStatus()
	if len(os.Args) > 1 { // file dropped onto the .exe
		setText(hModEdit, os.Args[1])
	}

	pShowWindow.Call(hwndMain, 1)
	pUpdateWindow.Call(hwndMain)
	var m msgT
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if hwndList != 0 {
			if d, _, _ := pIsDialogMessage.Call(hwndList, uintptr(unsafe.Pointer(&m))); d != 0 {
				continue
			}
		}
		if d, _, _ := pIsDialogMessage.Call(hwndMain, uintptr(unsafe.Pointer(&m))); d != 0 {
			continue
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func getModuleHandle() uintptr {
	r, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	return r
}

func wndProc(hwnd uintptr, msg uint32, wp, lp uintptr) (ret uintptr) {
	defer func() { // never let an error close the window
		if r := recover(); r != nil {
			winUI{}.Log(fmt.Sprintf("Unexpected error: %v", r))
			ret = 0
		}
	}()
	switch msg {
	case wmCommand:
		id := int(wp & 0xFFFF)
		code := int(wp >> 16)
		if id == idGameEdit && code == 0x0300 /*EN_CHANGE*/ {
			refreshStatus()
			return 0
		}
		if code != 0 {
			break
		}
		switch id {
		case idModBrowse:
			if p := pickFile(); p != "" {
				setText(hModEdit, p)
				autoPickGame(p)
			}
		case idModFolder:
			if p := pickFolderModern("Choose the mod's folder (the one with mod.json in it)", "Select mod folder"); p != "" {
				setText(hModEdit, p)
				autoPickGame(p)
			}
		case idGameBrowse:
			if p := pickFolderModern("Choose the game folder (the one with tlou-i.exe or tlou-ii.exe)", "Select game folder"); p != "" {
				setText(hGameEdit, p)
			}
		case idInstall:
			run(doInstall)
		case idUninstall:
			run(doUninstall)
		case idList:
			if atomic.LoadInt32(&busy) == 0 {
				showInstalledMods()
			}
		case idRestore:
			run(doRestore)
		case idHelp:
			showHelp()
		case idFree:
			run(doFree)
		}
		return 0
	case wmDropFiles:
		buf := make([]uint16, 1024)
		pDragQueryFile.Call(wp, 0, uintptr(unsafe.Pointer(&buf[0])), 1024)
		pDragFinish.Call(wp)
		p := syscall.UTF16ToString(buf)
		if gameAt(p) != nil {
			setText(hGameEdit, p)
		} else {
			setText(hModEdit, p)
			autoPickGame(p)
		}
		return 0
	case wmCtlColorStatic:
		if lp == hStatus {
			pSetBkMode.Call(wp, 1)
			pSetTextColor.Call(wp, 0x00206020)
			r, _, _ := pGetSysColorBrush.Call(15)
			return r
		}
	case wmApp: // worker finished
		atomic.StoreInt32(&busy, 0)
		for _, b := range buttons {
			pEnableWindow.Call(b, 1)
		}
		refreshStatus()
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

func refreshStatus() {
	g, err := FindGame(getText(hGameEdit))
	if err != nil {
		setText(hStatus, "")
		return
	}
	setText(hStatus, g.Describe())
}

// when a mod with a mod.json is picked, switch to the right game if we know where it is
func autoPickGame(modPath string) {
	go autoPickGameAsync(modPath, getText(hGameEdit))
}

func autoPickGameAsync(modPath, current string) {
	m, err := LoadMod(modPath)
	if err != nil {
		return
	}
	defer m.Close()
	if m.Manifest.Game == "" {
		return
	}
	if g, err := FindGame(current); err == nil && g.ID == m.Manifest.Game {
		return
	}
	if p, ok := SteamGameGuesses()[m.Manifest.Game]; ok {
		setText(hGameEdit, p)
	}
}

func run(job func(ui UI) error) {
	if !atomic.CompareAndSwapInt32(&busy, 0, 1) {
		return
	}
	for _, b := range buttons {
		pEnableWindow.Call(b, 0)
	}
	sendMsg(hProgress, pbmSetPos, 0, 0)
	go func() {
		ui := winUI{}
		defer func() {
			if r := recover(); r != nil {
				ui.Log(fmt.Sprintf("Unexpected error: %v", r))
				msgBox(fmt.Sprintf("Unexpected error:\n%v", r), appTitle, mbIconError)
			}
			pPostMessage.Call(hwndMain, wmApp, 0, 0)
		}()
		if err := job(ui); err != nil {
			if err.Error() == "cancelled" {
				ui.Log("Cancelled.\r\n")
				return
			}
			msg := err.Error()
			if strings.Contains(strings.ToLower(msg), "access is denied") {
				msg += "\n\nWindows blocked access to the game folder. Close the game and try again, or right-click the installer and choose \"Run as administrator\"."
			}
			ui.Log("ERROR: " + msg + "\r\n")
			msgBox(msg, appTitle, mbIconError)
		}
	}()
}

func currentGame() (*Game, error) {
	g, err := FindGame(getText(hGameEdit))
	if err != nil {
		return nil, err
	}
	saveSetting("game", g.Root)
	return g, nil
}

func doInstall(ui UI) error {
	g, err := currentGame()
	if err != nil {
		return err
	}
	src := getText(hModEdit)
	if strings.TrimSpace(src) == "" {
		return fmt.Errorf("choose a mod file first (step 1).")
	}
	ui.Log("Opening " + filepath.Base(src) + "...")
	m, err := LoadMod(src)
	if err != nil {
		return err
	}
	defer m.Close()
	title := m.Manifest.Name
	if m.Manifest.Version != "" {
		title += " v" + m.Manifest.Version
	}
	if m.Manifest.Author != "" {
		title += " by " + m.Manifest.Author
	}
	ui.Log("Mod: " + title)
	if err := g.Install(m, ui); err != nil {
		return err
	}
	ui.Progress(1, 1)
	ui.Log("Done! \"" + m.Manifest.Name + "\" is installed. Start the game to play with it.\r\n")
	msgBox("\""+m.Manifest.Name+"\" is installed.\n\nTo remove it later, press \"Installed mods\".", appTitle, mbIconInfo)
	return nil
}

func doUninstall(ui UI) error {
	g, err := currentGame()
	if err != nil {
		return err
	}
	s := g.LoadState()
	src := strings.TrimSpace(getText(hModEdit))
	var ids []string
	if src != "" {
		m, err := LoadMod(src)
		if err != nil {
			return err
		}
		m.Close()
		if s.Mods[m.Manifest.ID] == nil {
			return fmt.Errorf("\"%s\" isn't installed in %s.\n\nPress \"Installed mods\" to see what is installed and remove it from there.", m.Manifest.Name, g.Title)
		}
		ids = []string{m.Manifest.ID}
	} else {
		if len(s.Mods) == 0 {
			return fmt.Errorf("no mods are installed in %s.", g.Title)
		}
		for id, m := range s.Mods {
			if ui.Confirm("Uninstall", "Uninstall \""+m.Name+"\"?") {
				ids = append(ids, id)
			}
		}
	}
	for _, id := range ids {
		name := s.Mods[id].Name
		if err := g.Uninstall(id, ui); err != nil {
			return err
		}
		ui.Log("Done! \"" + name + "\" was removed and the original files are back.\r\n")
	}
	if len(ids) > 0 {
		msgBox("Uninstalled. The original game files are back.", appTitle, mbIconInfo)
	}
	return nil
}

func doList(ui UI) error {
	g, err := currentGame()
	if err != nil {
		return err
	}
	s := g.LoadState()
	ui.Log("Installed in " + g.Title + ":")
	if len(s.Mods) == 0 {
		ui.Log("  (nothing)")
	}
	var ids []string
	for id := range s.Mods {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := s.Mods[id]
		line := "  * " + m.Name
		if m.Version != "" {
			line += " v" + m.Version
		}
		ui.Log(fmt.Sprintf("%s  (%d files)", line, len(m.Files)))
	}
	ui.Log("")
	return nil
}

func doRestore(ui UI) error {
	g, err := currentGame()
	if err != nil {
		return err
	}
	if !ui.Confirm("Restore original game", "This removes ALL installed mods and puts "+g.Title+" back the way Steam installed it.\n\nContinue?") {
		return fmt.Errorf("cancelled")
	}
	if err := g.RestoreGame(ui); err != nil {
		return err
	}
	ui.Progress(1, 1)
	ui.Log("Done! The game is back to its original files.\r\n")
	msgBox("The game is back to its original files.", appTitle, mbIconInfo)
	return nil
}

func doFree(ui UI) error {
	g, err := currentGame()
	if err != nil {
		return err
	}
	if err := g.FreeUnused(ui); err != nil {
		return err
	}
	ui.Progress(1, 1)
	ui.Log("Done.\r\n")
	return nil
}

func showHelp() {
	msgBox(`Making a mod for this installer is easy:

1. Put your changed game files in a folder, using the same folder names the game uses inside build\pc\main.
   Example:   My Mod\files\actor97\joel-head-01-u1-g1-v1.pak

2. (Optional but recommended) add a mod.json next to the "files" folder:
   {
     "format": 1,
     "id": "my-mod",
     "name": "My Mod",
     "version": "1.0",
     "author": "your name",
     "game": "tlou1"        <- "tlou1" or "tlou2"
   }

3. Zip it up and share it.

Without a mod.json the installer still works: it matches files by name.
See MODDING.txt that came with the installer for details.`, appTitle+" - making mods", mbIconInfo)
}

func pickFile() string {
	buf := make([]uint16, 4096)
	filter := "Mod files (*.zip; *.rar; *.7z; *.pak; mod.json)\x00*.zip;*.rar;*.7z;*.pak;mod.json\x00All files (*.*)\x00*.*\x00\x00"
	fu := utf16.Encode([]rune(filter))
	ofn := openFileName{Owner: hwndMain, Filter: &fu[0], File: &buf[0], MaxFile: uint32(len(buf)),
		Title: u16("Choose a mod file (for an unzipped mod, use the Folder... button)"),
		Flags: 0x00080000 | 0x00001000 | 0x00000800 | 0x00000008 | 0x00000004} // EXPLORER|FILEMUSTEXIST|PATHMUSTEXIST|NOCHANGEDIR|HIDEREADONLY
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))
	if r, _, _ := pGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
		return ""
	}
	p := syscall.UTF16ToString(buf)
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".zip" && ext != ".rar" && ext != ".7z" && ext != ".pak" && !strings.EqualFold(filepath.Base(p), "mod.json") {
		// picked a readme/picture inside an unzipped mod: use the mod's folder
		if _, err := os.Stat(filepath.Join(filepath.Dir(p), "mod.json")); err == nil {
			p = filepath.Dir(p)
		}
	}
	return p
}

// pickFolderModern shows Windows' standard "Select Folder" dialog (IFileOpenDialog).
func pickFolderModern(title, okLabel string) string {
	clsid := syscall.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iid := syscall.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	var dlg uintptr
	hr, _, _ := ole32.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&clsid)), 0, 1 /*CLSCTX_INPROC_SERVER*/, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&dlg)))
	if hr != 0 || dlg == 0 {
		return pickFolder() // very old Windows: fall back to the classic dialog
	}
	vt := func(obj uintptr, i int) uintptr { return *(*uintptr)(unsafe.Pointer(*(*uintptr)(unsafe.Pointer(obj)) + uintptr(i)*unsafe.Sizeof(uintptr(0)))) }
	call := func(obj uintptr, i int, args ...uintptr) uintptr {
		r, _, _ := syscall.SyscallN(vt(obj, i), append([]uintptr{obj}, args...)...)
		return r
	}
	defer call(dlg, 2) // Release
	var opts uint32
	call(dlg, 10, uintptr(unsafe.Pointer(&opts)))           // GetOptions
	call(dlg, 9, uintptr(opts|0x20|0x40|0x800))               // SetOptions: PICKFOLDERS | FORCEFILESYSTEM | PATHMUSTEXIST
	call(dlg, 17, uintptr(unsafe.Pointer(u16(title))))        // SetTitle
	call(dlg, 18, uintptr(unsafe.Pointer(u16(okLabel))))      // SetOkButtonLabel
	if hr := call(dlg, 3, hwndMain); hr != 0 {                // Show (cancelled = non-zero)
		return ""
	}
	var item uintptr
	if call(dlg, 20, uintptr(unsafe.Pointer(&item))) != 0 || item == 0 { // GetResult
		return ""
	}
	defer call(item, 2)
	var psz uintptr
	if call(item, 5, 0x80058000, uintptr(unsafe.Pointer(&psz))) != 0 || psz == 0 { // GetDisplayName(SIGDN_FILESYSPATH)
		return ""
	}
	defer pCoTaskMemFree.Call(psz)
	var out []uint16
	for p := psz; ; p += 2 {
		c := *(*uint16)(unsafe.Pointer(p))
		if c == 0 {
			break
		}
		out = append(out, c)
	}
	return string(utf16.Decode(out))
}

func pickFolder() string {
	name := make([]uint16, 260)
	bi := browseInfo{Owner: hwndMain, DisplayName: &name[0], Title: u16("Choose the game folder (the one with tlou-i.exe or tlou-ii.exe)"),
		Flags: 0x0001 | 0x0040} // RETURNONLYFSDIRS | NEWDIALOGSTYLE
	pidl, _, _ := pSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer pCoTaskMemFree.Call(pidl)
	path := make([]uint16, 1024)
	pSHGetPathFromIDL.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	return syscall.UTF16ToString(path)
}

// ---- settings in %APPDATA%\TLOU Mod Installer ----

func settingsFile() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "TLOU Mod Installer", "settings.txt")
}

func loadSetting(key string) string {
	for _, l := range readLines(settingsFile()) {
		if k, v, ok := strings.Cut(l, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func saveSetting(key, val string) {
	f := settingsFile()
	if f == "" {
		return
	}
	var out []string
	for _, l := range readLines(f) {
		if k, _, ok := strings.Cut(l, "="); ok && k != key {
			out = append(out, l)
		}
	}
	out = append(out, key+"="+val)
	os.MkdirAll(filepath.Dir(f), 0755)
	os.WriteFile(f, []byte(strings.Join(out, "\n")+"\n"), 0644)
}
