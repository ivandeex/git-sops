package git

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pkg/errors"
	"go.mozilla.org/sops/v3"
)

type replace map[string]string

func renameTreeKeys(tree *sops.Tree, repl replace) error {
	if len(repl) == 0 {
		return nil
	}
	count := 0
	walk := &treeWalker{
		handleKey: func(key string) string {
			if newKey := repl[key]; newKey != "" {
				count++
				return newKey
			}
			return key
		},
	}
	err := walk.tree(tree)
	if count > 0 || err != nil {
		log.Debugf("replaced %d keys by %s (error %v)", count, repl.String(), err)
	}
	return err
}

func (repl replace) String() string {
	if repl == nil || len(repl) == 0 {
		return ""
	}
	list := []string{}
	for src := range repl {
		list = append(list, src)
	}
	sort.Strings(list)
	for i, src := range list {
		list[i] = src + ":" + repl[src]
	}
	return strings.Join(list, ",")
}

func newRepl(value string) (replace, error) {
	repl := map[string]string{}
	for _, token := range strings.Split(value, ",") {
		if token = strings.TrimSpace(token); token == "" {
			continue
		}
		parts := strings.Split(token, ":")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, errors.New("invalid rename-keys parameter")
		}
		repl[parts[0]] = parts[1]
	}
	return repl, nil
}

func (a *action) getRenameKeys(param string, useGit bool) (replace, error) {
	value := a.c.String(param)
	if value == "" && useGit {
		var err error
		value, err = a.configGet("", "sops."+param)
		if err != nil {
			return nil, err
		}
	}
	return newRepl(value)
}

func ensureComment(in interface{}) (sops.Comment, error) {
	switch comment := in.(type) {
	case sops.Comment:
		return comment, nil
	case string:
		return sops.Comment{Value: comment}, nil
	}
	return sops.Comment{}, fmt.Errorf("comment value should be Comment or string, was %T", in)
}
