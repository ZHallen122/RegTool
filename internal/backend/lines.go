package backend

import "strings"

// The helpers in this file edit configuration files one line at a time. Every
// line that the change does not touch is carried over byte for byte, including
// its original line terminator, its comments and its indentation. Only the line
// that actually holds the registry is rewritten.

// physLine is a single source line together with the terminator it was written
// with, so that a file using CRLF stays a CRLF file.
type physLine struct {
	text string // the line without its terminator
	eol  string // "\n", "\r\n" or "" for a final line without a terminator
}

// splitPhysical splits content into lines, remembering each terminator.
func splitPhysical(b []byte) []physLine {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	var out []physLine
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			if s != "" {
				out = append(out, physLine{text: s})
			}
			return out
		}
		text := s[:i]
		eol := "\n"
		if strings.HasSuffix(text, "\r") {
			text = strings.TrimSuffix(text, "\r")
			eol = "\r\n"
		}
		out = append(out, physLine{text: text, eol: eol})
		s = s[i+1:]
	}
}

// joinPhysical is the inverse of splitPhysical.
func joinPhysical(lines []physLine) []byte {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.text)
		sb.WriteString(l.eol)
	}
	return []byte(sb.String())
}

// defaultEOL picks the terminator to use for a line this package adds: the one
// the file already uses, or "\n" for a new or single-line file.
func defaultEOL(lines []physLine) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].eol != "" {
			return lines[i].eol
		}
	}
	return "\n"
}

// isComment reports whether a line is a comment or blank in the ini-like
// formats used by npm, yarn 1 and pip.
func isComment(text string) bool {
	t := strings.TrimSpace(text)
	return t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";")
}

// replaceOrAppend rewrites the effective occurrence of a key. When several
// lines match, the last one wins (that is what every format here does at read
// time), so the last is rewritten and the earlier duplicates are dropped to
// leave the file unambiguous. When nothing matches, newText is appended.
func replaceOrAppend(lines []physLine, match func(text string) bool, newText string) []physLine {
	last := -1
	for i, l := range lines {
		if match(l.text) {
			last = i
		}
	}

	if last < 0 {
		eol := defaultEOL(lines)
		out := make([]physLine, len(lines), len(lines)+1)
		copy(out, lines)
		// A file whose last line has no terminator needs one before the append.
		if n := len(out); n > 0 && out[n-1].eol == "" {
			out[n-1].eol = eol
		}
		return append(out, physLine{text: newText, eol: eol})
	}

	out := make([]physLine, 0, len(lines))
	for i, l := range lines {
		switch {
		case i == last:
			l.text = newText
			if l.eol == "" {
				l.eol = defaultEOL(lines)
			}
			out = append(out, l)
		case match(l.text):
			// drop the shadowed duplicate
		default:
			out = append(out, l)
		}
	}
	return out
}

// lastValue returns the value of the last line accepted by extract, which
// reports whether the line carries the key at all.
func lastValue(lines []physLine, extract func(text string) (string, bool)) string {
	value := ""
	for _, l := range lines {
		if v, ok := extract(l.text); ok {
			value = v
		}
	}
	return value
}

// unquote strips one layer of matching single or double quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// splitKeyValue splits "key<sep>value" on the first separator rune in seps and
// reports whether a separator was present. Key and value are trimmed.
func splitKeyValue(text, seps string) (key, value string, ok bool) {
	i := strings.IndexAny(text, seps)
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:]), true
}
