package git

import (
	"regexp"
	"strconv"
	"strings"
)

var reInComment = regexp.MustCompile(patKeyItem + `(.*\s)(#.*)$`)

func (ym *mangler) splitInlineComments() {
	encrypting := ym.opts.encrypting
	newLines := make([]string, 0, len(ym.lines))
	newIndent := make([]int, 0, len(ym.indent))
	for i, s := range ym.lines {
		p := ym.indent[i]
		if m := reInComment.FindStringSubmatch(s); m != nil {
			key, val, com := m[1], m[2], m[3]
			// check that comment is not a string between quotes
			ok := true
			if com = strings.TrimRight(com, " \t"); com != "" {
				if q := com[len(com)-1:]; q == "'" || q == `"` {
					ok = !strings.Contains(val, q) || strings.HasSuffix(strings.TrimSpace(val), q)
				}
			}
			if ok {
				pos := strconv.Itoa(len(s) - len(com)) // save position
				c := ym.padding(i) + com + mangleStart + pos + mangleEnd
				if encrypting {
					c += mangleComment
				}
				s = key + " " + strings.TrimRight(val, " \t")
				newLines = append(newLines, c, s)
				newIndent = append(newIndent, p, p)
				continue
			}
		}
		newLines = append(newLines, s)
		newIndent = append(newIndent, p)
	}
	ym.lines = newLines
	ym.indent = newIndent
}

func (ym *mangler) mergeInlineComments() {
	// ym.trace("merging")
	decrypting := !ym.opts.encrypting
	n := len(ym.lines)
	newLines := make([]string, n)
	for i, s := range ym.lines {
		newLines[i] = s
		j := i + 1
		if j == n {
			continue
		}
		if decrypting {
			s = strings.TrimSuffix(s, mangleComment)
		}
		if !strings.HasSuffix(s, mangleEnd) {
			continue
		}
		s = strings.TrimSpace(s)
		if s[0] != '#' {
			continue
		}
		idx := strings.LastIndex(s, mangleStart)
		if idx == -1 {
			continue
		}
		mark := s[idx+len(mangleStart) : len(s)-len(mangleEnd)]
		pos, err := strconv.Atoi(mark)
		if err != nil {
			continue
		}
		c := s[:idx]
		s = ym.lines[j]
		for s == "" && j < n-1 {
			j++
			s = ym.lines[j]
		}
		if s == "" || s == mangleBlank {
			continue
		}
		pad := pos - len(s)
		if pad < 1 {
			pad = 1
		}
		newLines[i] = s + strings.Repeat(" ", pad) + c
		ym.lines[j] = "" // to remove later (FIXME)
	}
	ym.lines = newLines
}

var reComment = regexp.MustCompile(`^(\s*#)(.*)$`)

func (ym *mangler) markEncryptedComments() {
	if !ym.opts.encrypting {
		return
	}

	var all bool
	var suffixes []string
	switch ym.opts.encryptedCommentSuffix {
	case "none":
		return
	case "all", "":
		all = true
	default:
		suffixes = strings.Split(ym.opts.encryptedCommentSuffix, ",")
	}
	prefixes := strings.Split(ym.opts.encryptedCommentPrefix, ",")

	for i, s := range ym.lines {
		m := reComment.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		value := strings.TrimSpace(strings.TrimLeft(m[2], "#"))
		if value == "" {
			continue
		}
		if strings.HasSuffix(value, mangleComment) {
			continue // prevent double-mangling
		}
		encrypt := all
		if !encrypt {
			for _, suffix := range suffixes {
				if suffix != "" && strings.HasSuffix(value, suffix) {
					encrypt = true
					break
				}
			}
		}
		if !encrypt {
			for _, prefix := range prefixes {
				if prefix != "" && strings.HasPrefix(value, prefix) {
					encrypt = true
					break
				}
			}
		}
		if encrypt {
			ym.lines[i] = s + mangleComment
		}
	}
}
