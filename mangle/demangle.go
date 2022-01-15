package mangle

import (
	"bytes"
	"strings"
)

func (opts *Options) Demangle(buf []byte, path string, encrypting bool) []byte {
	if !shouldMangle(opts, path, buf) || !bytes.Contains(buf, []byte(mangleStart)) {
		return buf
	}
	ym := newMangler(buf, opts, encrypting)
	ym.trace("demangling")

	decrypting := !encrypting
	streamEnd := false
	conv := ""
	anchor := ""
	for idx, line := range ym.lines {
		if decrypting {
			line = strings.TrimSuffix(line, MangleComment)
		}
		if !strings.HasSuffix(line, mangleEnd) {
			if conv != "" {
				line = ym.demangleLine(line, conv)
				conv = ""
			}
			if anchor != "" {
				line = ym.demangleLine(line, anchor)
				anchor = ""
			}
			ym.lines[idx] = line
			continue
		}
		mark := strings.TrimSpace(line)
		if !strings.HasPrefix(mark, mangleStart) {
			continue
		}
		mark = mark[len(mangleStart) : len(mark)-len(mangleEnd)]
		switch mark {
		case "":
			line = mangleBlank
		case "---":
			line = mark
		case "...":
			streamEnd = true
			line = ""
		case ":", "~", `"`, "'", "0", "@", "*", "<":
			if conv != "" && conv[0] == '&' {
				// anchors can be augmented by another conversion
				anchor = conv
			}
			if conv == "'" && mark == `"` {
				// go-yaml keeps empty strings unencrypted causing superfluous marking
				mark = conv
			}
			conv = mark // for the next line
			line = ""
		default:
			if mark != "" && mark[0] == '&' {
				conv = mark
				line = ""
				break
			}
			log.Fatalf("invalid line mark %q", mark)
		}
		ym.lines[idx] = line
	}

	ym.restoreMultilinePipes()
	ym.mergeInlineComments()
	ym.handleBlankLines()
	if streamEnd {
		ym.lines = append(ym.lines, "...")
	}
	ym.trace("result")
	return ym.Bytes()
}

func (ym *mangler) demangleLine(line string, conv string) string {
	switch conv {
	case ":": // bare key - drop "null"
		if m := reKeyNull.FindStringSubmatch(line); m != nil {
			line = m[1]
		}
	case "~": // tilde as value - replace "null" by "~"
		if m := reKeyNull.FindStringSubmatch(line); m != nil {
			line = m[1] + " ~"
		}
	case `"`: // cast into double-quoted string
		if m := reAString.FindStringSubmatch(line); m != nil {
			key, val := m[1], m[2]
			line = key + ` "` + stringToQ(stringFromA(val)) + `"`
			break
		}
		if m := reAnyVal.FindStringSubmatch(line); m != nil && !reQString.MatchString(line) {
			key, val := m[1], m[2]
			line = key + ` "` + stringToQ(val) + `"`
			break
		}
	case "'": // cast into single-quoted string
		if m := reQString.FindStringSubmatch(line); m != nil {
			key, val := m[1], m[2]
			line = key + " '" + stringToA(stringFromQ(val)) + "'"
			break
		}
		if m := reAnyVal.FindStringSubmatch(line); m != nil && !reAString.MatchString(line) {
			key, val := m[1], m[2]
			line = key + " '" + stringToA(val) + "'"
			break
		}
	case "0", "@": // restore inline map {}, list [], or a number
		// Maybe keep 0-starting numbers quoted?
		if m := reAString.FindStringSubmatch(line); m != nil {
			key, val := m[1], m[2]
			line = key + " " + stringFromA(val)
			break
		}
		if m := reQString.FindStringSubmatch(line); m != nil {
			key, val := m[1], m[2]
			line = key + " " + stringFromQ(val)
			break
		}
		log.Fatalf("invalid marked line %q", line)
	case "*": // restore alias
		if m := rePureVal.FindStringSubmatch(line); m != nil {
			key, val := m[1], m[2]
			line = key + " *" + val
			break
		}
		log.Fatalf("invalid alias line %q", line)
	case "<": // restore merge
		if m := rePureVal.FindStringSubmatch(line); m != nil {
			key, val := m[1], m[2]
			line = strings.ReplaceAll(key, "___:", "<<:") + " *" + val
			break
		}
		log.Fatalf("invalid merge line %q", line)
	case "": // nothing to do
	default:
		if conv[0] == '&' { // restore anchor
			line = demangleAnchorLine(line, conv[1:])
			break
		}
		log.Fatalf("invalid state %q at line %q", conv, line)
	}
	return line
}

func demangleAnchorLine(line, anchor string) string {
	var key, val string
	if m := reAnyVal.FindStringSubmatch(line); m != nil {
		key, val = m[1], m[2]
	} else if m := reKeyBare.FindStringSubmatch(line); m != nil {
		key = m[1]
	} else {
		log.Fatalf("invalid anchor line %q", line)
	}
	if val == "___" {
		// remove dummy item prepended by mangler
		key = strings.TrimSuffix(key, " ___:")
		val = ""
	}
	line = key + " &" + anchor + " " + val
	return strings.TrimRight(line, " ")
}
