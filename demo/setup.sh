#!/usr/bin/env bash
# Seeds the sandbox that demo/demo.tape records in.
#
# The tape sources this file with the typing hidden, so nothing here shows up in
# the GIF. Everything it touches lives under $HOME, which the tape points at a
# throwaway directory inside the VHS container: the recording never reads or
# writes the configuration of whoever renders it.

set -u

mkdir -p "$HOME" "$HOME/.config/pip" "${REGTOOL_CONFIG_DIR:-$HOME/.config/regtool}"

# npm: a registry line plus the two kinds of key regtool promises to leave
# alone, so the dry-run diff shows them surviving untouched.
cat >"$HOME/.npmrc" <<'EOF'
registry=https://registry.npmjs.org
//registry.npmjs.org/:_authToken=${NPM_TOKEN}
@acme:registry=https://npm.acme.example
fund=false
EOF

# pip: index-url under [global], with another key in the same section.
cat >"$HOME/.config/pip/pip.conf" <<'EOF'
[global]
index-url = https://pypi.org/simple
timeout = 30
EOF

# gem: :sources: is a YAML list, which is why this backend round-trips the file
# through a YAML parser instead of editing lines.
cat >"$HOME/.gemrc" <<'EOF'
---
:sources:
- https://rubygems.org/
:update_sources: true
gem: --no-document
EOF

# The demo binary is built into this directory by `make demo`.
export PATH="/vhs/demo:$PATH"

# A prompt with no hostname, user or path in it, so the recording carries
# nothing about the machine that rendered it.
export PS1='\[\033[1;36m\]❯\[\033[0m\] '

cd "$HOME" || return 0
