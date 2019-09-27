package git

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/pkg/errors"
)

var (
	errNotFound = errors.New("the file was not found")
	errIsDirty  = errors.New("please commit all modified files")
	errRebasing = errors.New("please finish rebasing")
	errNoBranch = errors.New("not on branch")
)

var zeroHash = plumbing.ZeroHash

const envFiltering = "SOPS_FILTERING"

// getGit will setup git structures and change directory to the repo root
func (a *action) getGit() error {
	curDir, err := os.Getwd()
	if err != nil {
		return err
	}
	opt := &git.PlainOpenOptions{DetectDotGit: true}
	if a.r, err = git.PlainOpenWithOptions(curDir, opt); err != nil {
		return err
	}
	if a.w, err = a.r.Worktree(); err != nil {
		return err
	}
	var ok bool
	if a.s, ok = a.r.Storer.(*filesystem.Storage); !ok {
		return fmt.Errorf("invalid git repository")
	}
	a.d = a.w.Filesystem.Root()
	return os.Chdir(a.d)
}

func (a *action) dotGit(path ...string) string {
	root := a.s.Filesystem().Root()
	path = append([]string{root}, path...)
	return filepath.Join(path...)
}

func (a *action) getState() (branch string, hash plumbing.Hash, encrypted bool, err error) {
	var head *plumbing.Reference
	if head, err = a.r.Head(); err != nil {
		return
	}
	hash = head.Hash()
	rebasing := false
	ref := head.Name()
	if !ref.IsBranch() {
		buf, err := ioutil.ReadFile(a.dotGit("rebase-merge", "head-name"))
		ref = plumbing.ReferenceName(strings.TrimSpace(string(buf)))
		if err != nil || !ref.IsBranch() {
			return "", zeroHash, false, errNoBranch
		}
		rebasing = true
	}
	branch = strings.TrimPrefix(ref.String(), "refs/heads/")

	var configured string
	if configured, err = a.configGet("", "sops.configured"); err != nil {
		return
	}
	var encrypt string
	if encrypt, err = a.configGet(branch, "sops-encrypt"); err != nil {
		return
	}
	switch os.Getenv(envFiltering) {
	case "1", "true", "encrypt":
		encrypted = true
	case "0", "false", "decrypt":
		encrypted = false
	default:
		encrypted = configured == "true" && encrypt == "true"
	}
	if rebasing {
		err = errRebasing
	}
	return
}

// ensureClean checks that all files are committed
// note: go-git will not honor .gitattributes and consequently
//       can't check status correctly when repository is encrypted
func (a *action) ensureClean(file string, quiet bool) (branch string, encrypted bool, err error) {
	rebase := false
	branch, _, encrypted, err = a.getState()
	if err == errRebasing {
		err = nil
		rebase = true
	}
	if err != nil {
		return branch, encrypted, err
	}
	if !encrypted {
		// can use internal go-git's status
		var status git.Status
		status, err = a.w.Status()
		if err == nil && !status.IsClean() {
			err = errIsDirty
		}
	} else {
		// fallback to normal git status honoring .gitattributes
		var out string
		cmd := "git status --short"
		if file != "" {
			cmd = fmt.Sprintf("%s -- %q", cmd, file)
		}
		out, err = execCommand(cmd, false, nil)
		out = strings.TrimSpace(out)
		if !quiet && err == nil && out != "" {
			fmt.Println(out)
		}
		if err == nil && out != "" {
			err = errIsDirty
		}
	}
	if err == nil && rebase {
		err = errRebasing
	}
	return branch, encrypted, err
}

func (a *action) purgeCache() error {
	ref := plumbing.ReferenceName("refs/notes/textconv/sops")
	return a.s.RemoveReference(ref)
}

func (a *action) toAbsPath(path string) string {
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.d, path)
	}
	return path
}

func (a *action) toRepoPath(path string) (string, error) {
	return filepath.Rel(a.d, a.toAbsPath(path))
}

func (a *action) readIndexFile(path string) ([]byte, error) {
	path, err := a.toRepoPath(path)
	if err != nil {
		return nil, err
	}
	idx, err := a.s.Index()
	if err != nil {
		return nil, err
	}
	entry, err := idx.Entry(path)
	if err == index.ErrEntryNotFound {
		err = errNotFound
	}
	if err != nil {
		return nil, err
	}
	blob, err := a.r.BlobObject(entry.Hash)
	if err != nil {
		return nil, err
	}
	reader, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(reader)
	if err == nil {
		err = reader.Close()
	}
	if err != nil {
		data = nil
	}
	return data, err
}

func (a *action) readCommitFile(hash plumbing.Hash, path string) (content []byte, err error) {
	var (
		commit *object.Commit
		file   *object.File
		text   string
	)
	commit, err = a.r.CommitObject(hash)
	if err == nil {
		file, err = commit.File(path)
	}
	if err == object.ErrFileNotFound {
		err = errNotFound
	}
	if err == nil {
		text, err = file.Contents()
	}
	if err == nil {
		content = []byte(text)
	}
	return
}

func (a *action) readGitFile(path string, loc string) (content []byte, err error) {
	switch loc {
	case "worktree":
		content, err = ioutil.ReadFile(a.toAbsPath(path))
		if os.IsNotExist(err) {
			err = errNotFound
		}
	case "index":
		content, err = a.readIndexFile(path)
	case "worktree,index":
		content, err = a.readGitFile(path, "worktree")
		if err == errNotFound {
			content, err = a.readGitFile(path, "index")
		}
	case "index,worktree":
		content, err = a.readGitFile(path, "index")
		if err == errNotFound {
			content, err = a.readGitFile(path, "worktree")
		}
	default:
		content, err = a.readCommitFile(plumbing.NewHash(loc), path)
	}
	if err != nil {
		err = errors.Wrapf(err, "read git file %s from %s", path, shortLoc(loc))
		content = nil
	}
	return
}

func (a *action) writeGitBlob(data []byte) (plumbing.Hash, error) {
	o := a.s.NewEncodedObject()
	o.SetType(plumbing.BlobObject)
	o.SetSize(int64(len(data)))
	w, err := o.Writer()
	if err == nil {
		_, err = w.Write(data)
	}
	if err == nil {
		err = w.Close()
	}
	if err == nil {
		_, err = a.s.SetEncodedObject(o)
	}
	if err != nil {
		return zeroHash, err
	}
	return o.Hash(), nil
}

func (a *action) commitLog() ([]plumbing.Hash, error) {
	headRef, err := a.r.Head()
	if err != nil {
		return nil, err
	}
	var revCommits []*object.Commit
	branchIter, err := a.r.Log(&git.LogOptions{From: headRef.Hash()})
	if err != nil {
		return nil, err
	}
	err = branchIter.ForEach(func(c *object.Commit) error {
		revCommits = append(revCommits, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	n1 := len(revCommits) - 1
	var hashes []plumbing.Hash
	for i := 0; i <= n1; i++ {
		hashes = append(hashes, revCommits[n1-i].Hash)
	}
	return hashes, nil
}

func (a *action) commitMessage(hash plumbing.Hash, maxLen int) (string, error) {
	msg := "-"
	if hash != zeroHash {
		commit, err := a.r.CommitObject(hash)
		if err != nil {
			return "", err
		}
		msg = strings.TrimSpace(commit.Message)
		msg = strings.TrimSpace(strings.Split(msg, "\n")[0])
	}
	if maxLen > 0 && len(msg) > maxLen {
		maxLen -= 1
		if maxLen < 0 {
			maxLen = 0
		}
		msg = msg[:maxLen] + "…"
	}
	return msg, nil
}
