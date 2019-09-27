package git

import (
	"fmt"
	"os"

	"github.com/pkg/errors"
	"go.mozilla.org/sops/v3"
)

type treeWalker struct {
	handleKey func(in string) (out string)
	handleVal func(in interface{}, path []string) (out interface{}, err error)
}

func (w *treeWalker) tree(tree *sops.Tree) error {
	for _, b := range tree.Branches {
		if _, err := w.branch(b, []string{}); err != nil {
			return err
		}
	}
	return nil
}

func (w *treeWalker) branch(branch sops.TreeBranch, path []string) (sops.TreeBranch, error) {
	for i, it := range branch {
		switch key := it.Key.(type) {
		case sops.Comment:
			out, err := w.value(key, path)
			if err == nil {
				branch[i].Key, err = ensureComment(out)
			}
			if err != nil {
				return nil, err
			}
		case string:
			if w.handleKey != nil {
				key = w.handleKey(key)
				branch[i].Key = key
			}
			val, err := w.value(it.Value, append(path, key))
			if err != nil {
				return nil, err
			}
			branch[i].Value = val
		default:
			return nil, fmt.Errorf("tree contains a non-string key %T: %v", key, key)
		}
	}
	return branch, nil
}

func (w *treeWalker) value(in interface{}, path []string) (interface{}, error) {
	switch in := in.(type) {
	case sops.TreeBranch:
		return w.branch(in, path)
	case []interface{}:
		for i, v := range in {
			if out, err := w.value(v, path); err != nil {
				return nil, err
			} else {
				in[i] = out
			}
		}
		return in, nil
	case string, []byte, int, bool, float64, sops.Comment, nil:
		if w.handleVal != nil {
			return w.handleVal(in, nil)
		}
		return in, nil
	default:
		return nil, fmt.Errorf("cannot walk unknown type %T", in)
	}
}

func (a *action) walkDir(dir string, isRemoving bool, handle func(path string, fi os.FileInfo)) error {
	// log.Debugf("walking dir %s ...", "/"+dir)
	fs := a.w.Filesystem
	list, err := fs.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, fi := range list {
		name := fi.Name()
		if name == ".git" {
			continue
		}
		path := fs.Join(dir, name)
		handle(path, fi)
		if !fi.IsDir() {
			continue
		}
		if _, err := os.Stat(path); os.IsNotExist(err) && isRemoving {
			continue
		}
		err = a.walkDir(path, isRemoving, handle)
		if err != nil {
			return errors.Wrapf(err, "walk subdir %s", path)
		}
	}
	return nil
}
