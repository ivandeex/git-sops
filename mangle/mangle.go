package mangle

import (
	"regexp"
	"strings"
)

const (
	mangleStart   = "#⋞"
	mangleEnd     = "⋟"
	mangleBlank   = mangleStart + mangleEnd
	mangleNewLine = "⋚⋛"
)

func (opts *Options) Mangle(buf []byte, path string, encrypting bool) []byte {
	if !shouldMangle(opts, path, buf) {
		return buf
	}
	ym := newMangler(buf, opts, encrypting)
	ym.trace("source")
	ym.collectIndent()
	mo := ym.opts.flags
	if mo["#"] {
		ym.splitInlineComments()
	}
	if mo["|"] {
		ym.mergeMultilinePipes()
	}
	ym.markEncryptedComments()
	sopsBlock := false
	for idx := range ym.lines {
		ok := ym.mangleSpecialLine(idx, sopsBlock)
		if ok || sopsBlock {
			continue
		}
		if ym.lines[idx] == "sops:" {
			sopsBlock = true
			continue
		}
		ym.markInlineFeatures(idx)
	}
	ym.trace("mangling")
	return ym.Bytes()
}

// mangle stream markers and blank lines
func (ym *mangler) mangleSpecialLine(idx int, sopsBlock bool) bool {
	line := ym.lines[idx]
	mo := ym.opts.flags
	isSpecial := false
	switch {
	case line == "---" && mo["-"]:
		isSpecial = idx == 0
	case line == "..." && mo["-"]:
		isSpecial = idx == len(ym.lines)-1
	case strings.TrimSpace(line) == "" && !sopsBlock && mo["_"]:
		line = ""
		isSpecial = true
	}
	if isSpecial {
		ym.lines[idx] = ym.padding(idx) + mangleStart + line + mangleEnd
	}
	return isSpecial
}

const (
	patKeyOnly = `^(\s*[a-zA-Z0-9_][a-zA-Z0-9_.-]*:|\s*'[a-zA-Z0-9_.,@%$-]+':)`
	patKeyItem = `^(\s*(?:- )*(?:-|[a-zA-Z0-9_][a-zA-Z0-9_.-]*:|'[a-zA-Z0-9_.,@%$-]+':))\s+`
	patAnchor  = `([A-Za-z_][A-Za-z_0-9]*)`
)

var (
	reKeyBare   = regexp.MustCompile(patKeyOnly + `$`)
	reKeyNull   = regexp.MustCompile(patKeyOnly + `\s+null$`)
	reKeyTilde  = regexp.MustCompile(patKeyOnly + `\s+~$`)
	reQString   = regexp.MustCompile(patKeyItem + `"(.*)"$`)
	reAString   = regexp.MustCompile(patKeyItem + `'(.*)'$`)
	rePureVal   = regexp.MustCompile(patKeyItem + `([^'"].*[^'"])$`)
	reZNumber   = regexp.MustCompile(patKeyItem + `(0[0-9]+)$`)
	reInlineVal = regexp.MustCompile(patKeyItem + `([[{].*[]}])$`)
	reAnyVal    = regexp.MustCompile(patKeyItem + `(.*)$`)
	reAnchor    = regexp.MustCompile(patKeyItem + `&` + patAnchor + `(\s.*|)$`)
	reAlias     = regexp.MustCompile(patKeyItem + `\*` + patAnchor + `$`)
	reMerge     = regexp.MustCompile(`^(\s*(?:-\s+)*<<:)\s+\*` + patAnchor + `$`)
)

func (ym *mangler) markInlineFeatures(idx int) bool {
	line := ym.lines[idx]
	mo := ym.opts.flags
	encryptable := true
	mark := ""
	switch {
	case reKeyBare.MatchString(line) && mo[":"]:
		encryptable = false
		mark = ":"
	case reKeyTilde.MatchString(line) && mo["~"]:
		mark = "~"
	case reQString.MatchString(line) && mo[`"`]:
		mark = `"`
	case reAString.MatchString(line) && mo["'"]:
		mark = "'"
	default:
		if m := reZNumber.FindStringSubmatch(line); m != nil && mo["0"] {
			key, val := m[1], m[2]
			line = key + " '" + val + "'"
			mark = "0"
			break
		}
		if m := reInlineVal.FindStringSubmatch(line); m != nil && mo["@"] {
			key, val := m[1], m[2]
			if val == "{}" || val == "[]" {
				break
			}
			line = key + " '" + stringToA(val) + "'"
			mark = "@"
		}
		if m := reAlias.FindStringSubmatch(line); m != nil && mo["*"] {
			key, anchor := m[1], m[2]
			line = key + " " + anchor
			mark = "*"
		}
		if m := reMerge.FindStringSubmatch(line); m != nil && mo["*"] {
			key, anchor := m[1], m[2]
			line = strings.ReplaceAll(key, "<<:", "___:") + " " + anchor
			mark = "<"
		}
		if m := reAnchor.FindStringSubmatch(line); m != nil && mo["*"] {
			key, anchor, val := m[1], m[2], m[3]
			line = key + " " + val
			if val == "" && thisIsListItem(line) && ym.nextIsInnerMap(idx) {
				// this is a bare anchor without value - prevent liftup of the next item
				// this list item is followed by inner map - prepend dummy inner map
				line = key + " ___: ___"
			}
			mark = "&" + anchor
		}
	}
	if mark == "" {
		return false
	}
	comment := mangleStart + mark + mangleEnd
	if encryptable && ym.encrypting {
		comment += MangleComment
	}
	// prepend comment line before current one
	ym.lines[idx] = ym.padding(idx) + comment + "\n" + line
	return true
}
