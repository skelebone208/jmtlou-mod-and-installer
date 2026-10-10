//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"os"
)

type cliUI struct{}

func (cliUI) Log(s string)               { fmt.Println(s) }
func (cliUI) Progress(d, t int64)         {}
func (cliUI) Confirm(t, m string) bool    { fmt.Println("[confirm]", t); return true }

// test harness: modinst install|uninstall|restore|list <game> [mod]
func main() {
	_ = bufio.NewReader
	a := os.Args
	if len(a) >= 3 && a[1] == "psarc" {
		p, err := OpenPSARC(a[2], must(loadOodle("")))
		if err != nil { fmt.Println(err); os.Exit(1) }
		n := p.Names(); fmt.Println(len(n), "files", gb(p.TotalSize()))
		if len(a) >= 4 { c, err := p.Unpack(a[3], nil); fmt.Println(len(c), err) }
		return
	}
	g, err := FindGame(a[2])
	if err != nil { fmt.Println(err); os.Exit(1) }
	ui := cliUI{}
	switch a[1] {
	case "install":
		m, err := LoadMod(a[3]); if err != nil { fmt.Println(err); os.Exit(1) }
		defer m.Close()
		err = g.Install(m, ui); fmt.Println("result:", err)
	case "uninstall":
		err = g.Uninstall(a[3], ui); fmt.Println("result:", err)
	case "free":
		err = g.FreeUnused(ui); fmt.Println("result:", err)
	case "restore":
		err = g.RestoreGame(ui); fmt.Println("result:", err)
	}
	fmt.Println(g.Describe())
}

func must(d DecompressFunc, err error) DecompressFunc { if err != nil { panic(err) }; return d }
