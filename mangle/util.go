package mangle

import (
	"bytes"
	"strings"

	"go.mozilla.org/sops/v3/cmd/sops/formats"
	"go.mozilla.org/sops/v3/logging"

	"github.com/sirupsen/logrus"
)

var TraceMangling = false
var log *logrus.Logger

func init() {
	log = logging.NewLogger("GIT")
}

type mangler struct {
	lines      []string
	indent     []int
	opts       *Options
	encrypting bool
}

func newMangler(buf []byte, opts *Options, encrypting bool) *mangler {
	buf = bytes.TrimLeft(buf, "\r\n")
	buf = bytes.TrimRight(buf, "\r\n \t")
	lines := strings.Split(string(buf), "\n")
	return &mangler{lines: lines, opts: opts, encrypting: encrypting}
}

func (ym *mangler) trace(message string) {
	if !TraceMangling {
		return
	}
	action := "decrypt"
	if ym.encrypting {
		action = "encrypt"
	}
	const hr = "~~~~~~~~"
	text := string(ym.Bytes())
	log.Debugf("%s/%s:\n%s\n%s%s", action, message, hr, text, hr)
}

func shouldMangle(opts *Options, path string, buf []byte) bool {
	if opts.isNone() || len(buf) == 0 {
		return false
	}
	fmt := formats.FormatForPathOrString(path, "")
	return fmt == formats.Yaml
}

func thisIsListItem(line string) bool {
	trim := strings.TrimSpace(line)
	return strings.HasPrefix(trim+" ", "- ")
}

// very simplistic check that next line is an inner map item
func (ym *mangler) nextIsInnerMap(idx int) bool {
	numLines := len(ym.lines)
	currIndent := ym.indent[idx]
	nextIndent := currIndent
	nextLine := ""
	for i := idx + 1; i < numLines && nextLine == ""; i++ {
		nextLine = strings.TrimSpace(ym.lines[i])
		nextIndent = ym.indent[i]
		if nextLine != "" && nextLine[0] == '#' {
			nextLine = ""
		}
	}
	if nextLine != "" && nextIndent > currIndent {
		return !strings.HasPrefix(nextLine, "-")
	}
	return false
}

func (ym *mangler) collectIndent() {
	n := len(ym.lines)
	ym.indent = make([]int, n)
	last := 0
	for i := n - 1; i >= 0; i-- {
		str := ym.lines[i]
		len := len(str)
		blank := true
		for pos := 0; pos < len && blank; pos++ {
			switch str[pos] {
			case ' ', '\t':
				continue
			default:
				blank = false
				ym.indent[i] = pos
				last = pos
			}
		}
		if blank {
			ym.indent[i] = last
		}
	}
}

func (ym *mangler) padding(idx int) string {
	if len(ym.indent) == 0 {
		return ""
	}
	return strings.Repeat(" ", ym.indent[idx])
}

func (ym *mangler) handleBlankLines() {
	trim := make([]string, 0, len(ym.lines))
	for _, s := range ym.lines {
		switch s {
		case "":
			continue
		case mangleBlank:
			s = ""
		}
		trim = append(trim, s)
	}
	n := len(trim)
	for n > 0 && trim[n-1] == "" {
		n--
	}
	ym.lines = trim[:n]
}

func (ym *mangler) Bytes() []byte {
	return append([]byte(strings.Join(ym.lines, "\n")), '\n')
}

func stringFromA(s string) string {
	s = strings.ReplaceAll(s, "''", "'")
	return s
}

func stringToA(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

func stringFromQ(s string) string {
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

func stringToQ(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
