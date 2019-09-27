package git

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitattributes"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/pkg/errors"
)

// TODO Handle .gitattributes in subdirectories
const gitAttrFileName = ".gitattributes"

func (a *action) listFiles(staged bool) error {
	loc := "worktree"
	if staged {
		loc = "index"
	}
	files, err := a.matchFiles(loc)
	if err != nil {
		return err
	}
	for _, f := range files {
		fmt.Println(f)
	}
	return nil
}

func (a *action) chmodFiles(files []string) error {
	var err error
	if files, err = a.matchWorktree(files); err != nil {
		return err
	}
	for _, path := range files {
		path = a.toAbsPath(path)
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		mode := fi.Mode() & 0770 // disable "other" access
		if err = os.Chmod(path, mode); err != nil {
			return err
		}
	}
	return nil
}

func (a *action) smudgeFiles(files []string) error {
	baseOpts, err := a.getOptions()
	if err != nil {
		return err
	}
	if files, err = a.matchWorktree(files); err != nil {
		return err
	}
	for _, path := range files {
		input, err := a.readIndexFile(path)
		output := input
		if err == nil && len(input) > 0 {
			opts := baseOpts.forPath(path)
			opts.inputData = input
			output, err = a.sopsDecrypt(opts)
			if isMetaNotFound(err, path) {
				output = input
				err = nil
			}
		}
		if err == nil {
			err = os.Remove(path)
		}
		if err == nil {
			err = ioutil.WriteFile(path, output, 0644) // FIXME 0640
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *action) matchWorktree(files []string) ([]string, error) {
	if files != nil {
		return files, nil
	}
	return a.matchFiles("worktree")
}

func (a *action) matchFiles(loc string) ([]string, error) {
	data, err := a.readGitFile(gitAttrFileName, loc)
	if errors.Cause(err) == errNotFound {
		log.Debugf("gitattributes not found in %s", shortLoc(loc))
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrapf(err, "read gitattributes from %s", shortLoc(loc))
	}
	text := string(data)

	// FIXME dirty hacks to workaround for lack of "[x-y] [abc]" syntax in go-git
	text = strings.ReplaceAll(text, "[0-9]", "*")
	text = strings.ReplaceAll(text, "[.-]secret", "-secret")
	text = strings.ReplaceAll(text, "secret[.-]", "secret.")
	//log.Debugf("fixed gitattributes:\n%s", text)

	matchAttrs, err := gitattributes.ReadAttributes(strings.NewReader(text), nil, true)
	if err != nil {
		return nil, errors.Wrap(err, "parse gitattributes")
	}

	var patterns []gitattributes.Pattern
	for _, m := range matchAttrs {
		if isOurMatch(m) {
			patterns = append(patterns, m.Pattern)
			//log.Debugf("our pattern: %#v", m.Pattern)
		}
	}
	if len(patterns) == 0 {
		return nil, nil
	}

	var allFiles []string
	switch loc {
	case "index":
		idx, err := a.s.Index()
		if err != nil {
			return nil, errors.Wrap(err, "grab index")
		}
		for _, entry := range idx.Entries {
			if entry.Mode.IsFile() {
				allFiles = append(allFiles, entry.Name)
			}
		}
	case "worktree":
		err = a.walkDir("", false, func(path string, fi os.FileInfo) {
			if !fi.IsDir() {
				allFiles = append(allFiles, path)
			}
		})
		if err != nil {
			return nil, errors.Wrap(err, "walk worktree")
		}
	default: // commit hash
		var tree *object.Tree
		c, err := a.r.CommitObject(plumbing.NewHash(loc))
		if err == nil {
			tree, err = c.Tree()
		}
		if err != nil {
			return nil, errors.Wrapf(err, "grab commit %s", shortLoc(loc))
		}
		walker := object.NewTreeWalker(tree, true, nil)
		defer walker.Close()
		for {
			path, entry, err := walker.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, errors.Wrapf(err, "walk commit %s tree", shortLoc(loc))
			}
			if entry.Mode != filemode.Dir {
				allFiles = append(allFiles, path)
			}
		}
	}
	sort.Strings(allFiles)
	log.Debugf("all files in %s: %s", shortLoc(loc), allFiles)

	var files []string
	for _, path := range allFiles {
		splitPath := strings.Split(path, "/")
		for _, p := range patterns {
			if p.Match(splitPath) {
				files = append(files, path)
				//log.Debugf("%s matched %#v", path, p)
				break
			}
		}
	}
	sort.Strings(files)
	log.Debugf("matching files in %s: %s", shortLoc(loc), files)

	return files, nil
}

func isOurMatch(m gitattributes.MatchAttribute) bool {
	for _, a := range m.Attributes {
		if a.Name() == "filter" && a.Value() == gitDriver {
			return true
		}
	}
	return false
}
