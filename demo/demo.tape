# The README demo, recorded with VHS (https://github.com/charmbracelet/vhs).
#
#   make demo
#
# builds a linux/amd64 regtool into demo/ and renders this tape in the official
# VHS container, which is why every path below is a container path.
#
# The recording is hermetic: HOME points at a throwaway directory that
# demo/setup.sh seeds with an .npmrc, a pip.conf and a .gemrc, and
# REGTOOL_OFFLINE=1 keeps regtool on the mirror list embedded in the binary, so
# the tape renders the same output with or without a network.

Output assets/demo.gif

Set Shell bash
Set FontSize 16
Set Width 1100
Set Height 620
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 20

Env HOME "/demo"
Env XDG_CONFIG_HOME "/demo/.config"
Env REGTOOL_CONFIG_DIR "/demo/.config/regtool"
Env REGTOOL_OFFLINE "1"

Hide
Type "source /vhs/demo/setup.sh"
Enter
Sleep 1s
Type "clear"
Enter
Sleep 500ms
Show

# What every package manager on this machine points at right now.
Type "regtool status"
Sleep 500ms
Enter
Sleep 3s

Hide
Type "clear"
Enter
Sleep 300ms
Show

# --dry-run writes nothing and prints the diff of every file it would rewrite.
Type "regtool use cn npm gem --dry-run"
Sleep 500ms
Enter
Sleep 6s

Hide
Type "clear"
Enter
Sleep 300ms
Show

# The real thing: every installed backend, snapshotted before it is touched.
Type "regtool use cn"
Sleep 500ms
Enter
Sleep 3s
Type "regtool status"
Sleep 500ms
Enter
Sleep 3s

Hide
Type "clear"
Enter
Sleep 300ms
Show

# Every change left a snapshot, and undo puts the files back byte for byte.
Type "regtool history"
Sleep 500ms
Enter
Sleep 3s
Type "regtool undo"
Sleep 500ms
Enter
Sleep 2s
Type "regtool status"
Sleep 500ms
Enter
Sleep 4s
