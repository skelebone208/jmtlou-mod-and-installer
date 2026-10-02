TLOU MOD INSTALLER - MAKING MODS
================================
Free to use, share and build on. Works with The Last of Us Part I (PC) and
The Last of Us Part II Remastered (PC).

WHAT THE INSTALLER DOES
-----------------------
The games keep their files inside big .psarc archives in build\pc\main.
The first time a mod needs a file from one of those archives, the installer
unpacks that archive once into loose files and renames it to .psarc.bak, so
the game loads the loose files instead. Then it copies the mod's files in.

Uninstall puts the original files back. They come straight from the game's own
.bak archive, so nothing is lost even if you installed files by hand before.
"Restore original game" removes every mod, deletes the unpacked files and
renames the archives back. Everything the installer records is kept in
build\pc\main\_modinstaller.

If Steam's "Verify integrity of game files" is used, Steam may re-download the
archive. Use "Restore original game" first, or just reinstall your mods after.

Keep the same "id" in new versions: installing a new version over an old one
updates it (and puts back any files the new version no longer changes).

WITHOUT A mod.json
------------------
Mods without a mod.json still install. The installer works out where each
file goes:
  - paths containing build\pc\main\... are used from there,
  - folders the game uses (actor97, texturedict3, pak68, ...) are kept,
  - a single file is matched by name against the game's files.
Readme files and pictures are ignored. Anything it can't place is skipped and
listed in the log. The zip's file name becomes the mod's name, so to uninstall
pick the same file again (or clear the mod box and press Uninstall).

SUPPORTED MOD FILES
-------------------
.zip and .rar work everywhere. .7z uses Windows' built-in tar.exe (newer
Windows 11) or 7-Zip if it's installed - .zip is the safest choice for sharing.
A single .pak file, or an unzipped mod folder (pick its mod.json), also works.

TWO MODS CHANGING THE SAME FILE
-------------------------------
The installer refuses and tells you which mod already changed that file.
Uninstall that one first.
