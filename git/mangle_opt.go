package git

import (
	"fmt"
	"sort"
	"strings"
)

var mangleKeyToOpt = map[string]string{
	"-": "stream",
	"_": "blank",
	":": "bare",
	"~": "tilde",
	`"`: "qstr",
	"'": "astr",
	"0": "znum",
	"@": "inval",
	"#": "incom",
	"*": "anchor",
	"|": "pipe",
}

var mangleOptToKey = map[string]string{
	// short options
	"stream": "-",
	"blank":  "_",
	"bare":   ":",
	"tilde":  "~",
	"qstr":   `"`,
	"astr":   "'",
	"znum":   "0",
	"inval":  "@",
	"incom":  "#",
	"anchor": "*",
	"pipe":   "|",
	// long options
	"stream-mark":    "-",
	"blank-line":     "_",
	"bare-key":       ":",
	"tilde-null":     "~",
	"quoted-string":  `"`,
	"apos-string":    "'",
	"zero-number":    "0",
	"inline-value":   "@",
	"inline-comment": "#",
	"multiline-pipe": "|",
}

func newMangleOpts(value string) (mangleOpts, error) {
	mo := mangleOpts{}
	switch value {
	case "all", "true":
		value = mangleAll
	case "none", "false", "":
		return mo, nil
	}
	for _, opt := range strings.Split(value, ",") {
		opt = strings.TrimSpace(opt)
		key := strings.TrimRight(opt, "s") // handle plurals
		if len(key) > 1 {
			key = mangleOptToKey[key]
		}
		if key == "" || !strings.Contains(allMangleKeys, key) {
			return nil, fmt.Errorf("invalid styling option %q", opt)
		}
		mo[key] = true
	}
	return mo, nil
}

func (mo mangleOpts) isNone() bool {
	return mo == nil || len(mo) == 0
}

func (mo mangleOpts) String() string {
	if mo.isNone() {
		return "false"
	}

	options := []string{}
	for key, flag := range mo {
		if !flag {
			continue
		}
		opt := mangleKeyToOpt[key]
		if opt == "" {
			log.Fatalf("invalid mangling key %q", key)
		}
		options = append(options, opt)
	}
	sort.Strings(options)

	value := strings.Join(options, ",")
	switch value {
	case mangleAll:
		value = "true"
	case "":
		value = "false"
	}
	return value
}

func (a *action) getMangleOpts(param string, useGit bool) (mangleOpts, error) {
	value := a.c.String(param)
	if value == "" && useGit {
		var err error
		value, err = a.configGet("", "sops."+param)
		if err != nil {
			return nil, err
		}
	}
	return newMangleOpts(value)
}
