package git

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/cmd/sops/formats"
)

type mangleOpts map[string]bool

type mangler struct {
	lines  []string
	indent []int
	opts   *options
}

const (
	mangleAll     = "anchor,astr,bare,blank,incom,inval,pipe,qstr,stream,tilde,znum"
	allMangleKeys = `-_:~"'0@#*|`

	mangleBlank   = mangleStart + mangleEnd
	mangleStart   = "#⋞"
	mangleEnd     = "⋟"
	mangleComment = "∉∌"
	mangleNewLine = "⋚⋛"
)

func newMangler(buf []byte, opts *options) *mangler {
	buf = bytes.TrimLeft(buf, "\r\n")
	buf = bytes.TrimRight(buf, "\r\n \t")
	lines := strings.Split(string(buf), "\n")
	return &mangler{lines: lines, opts: opts}
}

func shouldMangle(opts *options, buf []byte) bool {
	if opts.mangleOpts.isNone() || len(buf) == 0 {
		return false
	}
	fmt := formats.FormatForPathOrString(opts.inputPath, "")
	return fmt == formats.Yaml
}

//=================
//   Mangle
//=================

func (a *action) yamlMangle(buf []byte, opts *options) []byte {
	if !shouldMangle(opts, buf) {
		return buf
	}
	ym := newMangler(buf, opts)
	ym.trace("source")
	ym.collectIndent()
	if opts.mangleOpts["#"] {
		ym.splitInlineComments()
	}
	if opts.mangleOpts["|"] {
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
	mo := ym.opts.mangleOpts
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
	mo := ym.opts.mangleOpts
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
	if encryptable && ym.opts.encrypting {
		comment += mangleComment
	}
	// prepend comment line before current one
	ym.lines[idx] = ym.padding(idx) + comment + "\n" + line
	return true
}

//=================
//   Demangle
//=================

func (a *action) yamlDemangle(buf []byte, opts *options) []byte {
	if !shouldMangle(opts, buf) || !bytes.Contains(buf, []byte(mangleStart)) {
		return buf
	}
	ym := newMangler(buf, opts)
	ym.trace("demangling")

	decrypting := !opts.encrypting
	streamEnd := false
	conv := ""
	anchor := ""
	for idx, line := range ym.lines {
		if decrypting {
			line = strings.TrimSuffix(line, mangleComment)
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

func (a *action) testMangle(path string, mangle bool) error {
	baseOpts, err := a.getOptions()
	if err != nil {
		return err
	}
	opts := baseOpts.forPath(path)
	in, err := getInput(path, false)
	if err != nil {
		return err
	}
	if mangle {
		in = a.yamlMangle(in, opts)
	}
	branches, err := opts.inputStore.LoadPlainFile(in)
	if err != nil {
		return err
	}
	tree := &sops.Tree{
		Branches: branches,
		Metadata: opts.meta,
		FilePath: path,
	}
	out, err := opts.outputStore.EmitEncryptedFile(*tree)
	if err != nil {
		return err
	}
	if mangle {
		out = a.yamlDemangle(out, opts)
	}
	fmt.Print(string(out))
	return nil
}
