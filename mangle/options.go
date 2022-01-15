package mangle

import (
	"fmt"
	"sort"
	"strings"
)

type Options struct {
	encryptedCommentPrefix string
	encryptedCommentSuffix string
	flags                  map[string]bool
}

const MangleAll = "anchor,astr,bare,blank,incom,inval,pipe,qstr,stream,tilde,znum"
const allMangleKeys = `-_:~"'0@#*|`

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

func NewOptions(commentPrefix, commentSuffix, flagString string) (*Options, error) {
	mo := &Options{
		encryptedCommentPrefix: commentPrefix,
		encryptedCommentSuffix: commentSuffix,
		flags:                  map[string]bool{},
	}
	switch flagString {
	case "all", "true":
		flagString = MangleAll
	case "none", "false", "":
		return mo, nil
	}
	for _, opt := range strings.Split(flagString, ",") {
		opt = strings.TrimSpace(opt)
		key := strings.TrimRight(opt, "s") // handle plurals
		if len(key) > 1 {
			key = mangleOptToKey[key]
		}
		if key == "" || !strings.Contains(allMangleKeys, key) {
			return nil, fmt.Errorf("invalid styling option %q", opt)
		}
		mo.flags[key] = true
	}
	return mo, nil
}

func (mo *Options) isNone() bool {
	return mo == nil || mo.flags == nil || len(mo.flags) == 0
}

func (mo *Options) FlagString() string {
	if mo.isNone() {
		return "false"
	}

	options := []string{}
	for key, flag := range mo.flags {
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
	case MangleAll:
		value = "true"
	case "":
		value = "false"
	}
	return value
}
