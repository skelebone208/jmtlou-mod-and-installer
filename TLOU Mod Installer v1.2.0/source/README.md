# TLOU Mod Installer

A small, free and open-source Windows mod installer for **The Last of Us Part I** and **The Last of Us Part II Remastered** (PC).

Pick a mod (`.zip`, `.rar`, `.7z`, a single game file or a mod folder, or drag it onto the window), pick the game folder, press **Install**.
Press **Installed mods** to see everything that's installed and uninstall one or several mods; the original files are put back.
Several mods can be installed at once; if two mods change the same file or texture, the installer refuses and names the other mod.

## What it does

- **Reads the game's `.psarc` archives** to find out where each mod file belongs (`psarc.go`).
  Compressed blocks are decompressed with the game's own `oo2core_9_win64.dll`, loaded from the game folder at runtime. The DLL is not included.
- **One-time setup (`ops.go`):** the first time a mod replaces a file that lives inside an archive, it asks permission.
  It then unpacks that archive into loose files in `build\pc\main` and renames it to `.psarc.bak`, so the game loads the loose files.
  It keeps a list of every file it unpacked.
- **Install:** copies the mod's files into `build\pc\main`.
  If it overwrites a file that isn't in an archive, it backs it up first.
- **Textures (`texpatch.go`):** a mod can ship replacement textures (`textures\*.tex`). Character textures stream from the game's texture dictionaries (`build\pc\main\texturedict3`), so each texture is written into the dictionary slot that holds its id - in every dictionary that has a copy - with the original bytes backed up first. That way the new texture also shows in the Extras skin viewer. A texture that is too big for its slot has its largest mip level dropped until it fits.
- **Uninstall:** puts each original back, either re-extracted from the game's own archive or copied from the backup.
  Files the mod added are deleted.
- **Restore original game:** uninstalls every mod, deletes the unpacked files and renames the archives back.
- **Record keeping:** everything is recorded in `build\pc\main\_modinstaller\state.json`.
  The last-used game folder is saved in `%APPDATA%\TLOU Mod Installer\settings.txt`.

It makes **no network connections**, needs **no admin rights** (runs `asInvoker`), and only writes inside the game's `build\pc\main` folder, the user's temp folder (to extract the mod), and the settings file above.
For `.7z` files it runs Windows' built-in `tar.exe`, or `7z.exe` if 7-Zip is installed.

## Source layout

| File | Purpose |
|---|---|
| `gui_windows.go` | Window, buttons and file/folder pickers (plain Win32 calls, no GUI library) |
| `gui_list_windows.go` | The "Installed mods" window (list + uninstall) |
| `texpatch.go` | Writing mod textures into the game's texture dictionaries, and restoring them |
| `ops.go` | Install / uninstall / restore logic |
| `game.go` | Finding the game folder, indexing game files, saving state |
| `mod.go` | Opening `.zip` / `.rar` / `.7z` / folders, reading `mod.json`, working out where each file goes |
| `psarc.go` | PSARC archive reader |
| `sys_windows.go` | Loads the Oodle DLL, checks whether the game is running, checks free disk space |
| `sys_other.go`, `cli_other.go` | Non-Windows test build (command line), used for testing on Linux |
| `app.manifest`, `icon.ico` | Windows manifest and icon, embedded at build time |
| `icon.ico.b64` | The same icon as base64 text (for copies of the source that can't hold binary files, like a Gist). The build scripts recreate `icon.ico` from it if it's missing |

Dependency: [`github.com/nwaples/rardecode/v2`](https://github.com/nwaples/rardecode) v2.4.1 (BSD-2-Clause) for `.rar` files.
Everything else is the Go standard library.

## Building

Requires [Go](https://go.dev/dl/) **1.24 or newer** (release builds use Go 1.24.7).

**Windows:** run `build.bat`.

**Linux / macOS (cross-compile):** run `./build.sh`.

Or by hand:

```
go mod download
go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -ico icon.ico -arch amd64 -o rsrc_windows_amd64.syso
set GOOS=windows& set GOARCH=amd64& set CGO_ENABLED=0
go build -trimpath -ldflags "-H windowsgui -s -w" -o "TLOU Mod Installer.exe" .
```

The `rsrc` step only embeds the icon and manifest. Skipping it still gives a working `.exe`, just without the icon.

## Making mods for it

See [MODDING.txt](MODDING.txt). In short: zip a `files` folder that mirrors `build\pc\main`, plus an optional `mod.json`:

```json
{ "format": 1, "id": "my-mod", "name": "My Mod", "version": "1.0", "author": "you", "game": "tlou1" }
```

## License

Made by **skelebone0108**. MIT license: free to use, change and share, as long as the copyright and license notice is kept (please credit skelebone0108 if you build on it). See [LICENSE](LICENSE) and [THIRD-PARTY-LICENSES.txt](THIRD-PARTY-LICENSES.txt).
Not affiliated with Naughty Dog or Sony Interactive Entertainment.
