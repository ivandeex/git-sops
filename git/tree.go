package git

import (
	"fmt"
	"path"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/sirupsen/logrus"
)

func (t *transformer) collectTrees(parentPath string, parentTree *object.Tree) error {
	log.Debugf("src0 tree %q [%s]", parentPath, traceTree(parentTree))

	for _, entry := range parentTree.Entries {
		if entry.Mode != filemode.Dir {
			continue
		}
		childPath := path.Join(parentPath, entry.Name)
		childTree, err := object.GetTree(t.a.s, entry.Hash)
		if err != nil {
			return fmt.Errorf("find tree for path %s", childPath)
		}
		if err := t.collectTrees(childPath, childTree); err != nil {
			return err
		}
		t.treeCache[childPath] = childTree
	}

	return nil
}

func (t *transformer) updateHashes(parentPath string, parentTree *object.Tree) (plumbing.Hash, error) {
	log.Debugf("dst1 tree %q [%s]", parentPath, traceTree(parentTree))

	for i := range parentTree.Entries {
		entry := &parentTree.Entries[i] // force reference to entry, not a copy
		if entry.Mode != filemode.Dir {
			continue
		}
		childPath := path.Join(parentPath, entry.Name)
		childTree := t.treeCache[childPath]
		var err error
		if childTree != nil {
			entry.Hash, err = t.updateHashes(childPath, childTree)
		} else {
			err = fmt.Errorf("no tree to update for path %s", childPath)
		}
		if err != nil {
			return plumbing.ZeroHash, err
		}
	}

	store := t.a.s
	obj := store.NewEncodedObject()
	if err := parentTree.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	hash := obj.Hash()

	parentTree.Hash = hash
	log.Debugf("dst2 tree %q [%s]", parentPath, traceTree(parentTree))
	if store.HasEncodedObject(hash) == nil {
		return hash, nil
	}
	return store.SetEncodedObject(obj)
}

func traceTree(t *object.Tree) string {
	if log.Level < logrus.DebugLevel {
		return ""
	}
	list := []string{".=" + shortLoc(t.Hash.String())}
	for _, e := range t.Entries {
		list = append(list, e.Name+"="+shortLoc(e.Hash.String()))
	}
	return strings.Join(list, " ")
}
