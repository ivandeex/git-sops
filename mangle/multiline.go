package mangle

import (
	"regexp"
	"strconv"
	"strings"
)

var Indent = 2

var (
	rePipeStart = regexp.MustCompile(patKeyItem + `"{{.*[^"]$`)
	rePipeEnd   = regexp.MustCompile(`.*}}"$`)
	rePipeMark  = regexp.MustCompile(`^\s*#` + mangleStart + `\|([0-9|]+)` + mangleEnd)
)

func (ym *mangler) mergeMultilinePipes() {
	encrypting := ym.encrypting
	newLines := make([]string, 0, len(ym.lines))
	newIndent := make([]int, 0, len(ym.indent))
	s := ym.lines
	p := ym.indent
	n := len(s)
	for i := 0; i < n; i++ {
		if rePipeStart.MatchString(s[i]) {
			end := 0
			for j := i; j < n; j++ {
				if j > i && p[j] <= p[i] {
					break
				}
				if rePipeEnd.MatchString(s[j]) {
					end = j
					break
				}
			}
			if end > i {
				comment := ym.padding(i) + "#" + mangleStart
				merged := s[i]
				for k := i + 1; k <= end; k++ {
					comment += "|" + strconv.Itoa(p[k])
					merged += mangleNewLine + strings.TrimSpace(s[k])
				}
				comment += mangleEnd
				if encrypting {
					comment += MangleComment
				}
				newLines = append(newLines, comment, merged)
				newIndent = append(newIndent, p[i], p[i])
				i = end
				continue
			}
		}
		newLines = append(newLines, s[i])
		newIndent = append(newIndent, p[i])
	}
	ym.lines = newLines
	ym.indent = newIndent
}

func (ym *mangler) restoreMultilinePipes() {
	decrypting := !ym.encrypting
	lines := ym.lines
	n := len(lines)
	for i, s := range lines {
		j := i + 1
		if j == n {
			continue
		}
		if decrypting {
			s = strings.TrimSuffix(s, MangleComment)
		}
		m := rePipeMark.FindStringSubmatch(s)
		if m == nil {
			continue
		}

		// parse indent list
		indents := []int{}
		for _, tok := range strings.Split(m[1], "|") {
			p, err := strconv.Atoi(tok)
			if err != nil {
				log.Fatalf("invalid multiline marker %q", s)
			}
			indents = append(indents, p)
		}

		// find merged line and validate
		s = lines[j]
		for s == "" && j < n-1 {
			j++
			s = ym.lines[j]
		}
		if s == "" || s == mangleBlank {
			continue
		}
		if cnt := strings.Count(s, mangleNewLine); cnt != len(indents) {
			log.Fatalf("wrong newline count %d (must be %d): %q", cnt, len(indents), s)
		}

		// restore multiline
		baseIndent := 0
		for baseIndent < len(s) {
			if c := s[baseIndent]; c != ' ' && c != '\t' {
				break
			}
			baseIndent++
		}
		for _, p := range indents {
			if p <= baseIndent {
				p = baseIndent + Indent
			}
			s = strings.Replace(s, mangleNewLine, "\n"+strings.Repeat(" ", p), 1)
		}

		lines[i] = "" // to remove later
		lines[j] = s
	}
}
