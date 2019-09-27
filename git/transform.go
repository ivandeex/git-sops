package git

import (
	"fmt"
	"os"
	"path"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/pkg/errors"
)

type transformer struct {
	a          *action
	encrypt    bool
	progress   bool
	curBranch  string
	tmpBranch  string
	restoreCur bool
	deleteTemp bool
	hashLog    []plumbing.Hash
	curHash    plumbing.Hash
	shortHash  string
	oldDadHash plumbing.Hash
	newDadHash plumbing.Hash
	baseOpts   *options
	startAt    time.Time
	transCache map[plumbing.Hash]plumbing.Hash
	treeCache  map[string]*object.Tree
}

const gitPruneWorkaround = false

func (t *transformer) finalize() {
	if t.restoreCur {
		_ = t.a.w.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(t.curBranch),
			Force:  true,
		})
	}
	if t.deleteTemp {
		_ = t.a.deleteBranch(t.tmpBranch, true)
	}
}

func (a *action) transformBranch(newBranch string, encrypt, force, progress bool) error {
	// check that worktree is clean
	curBranch, wasEncrypted, err := a.ensureClean("", false)
	if err == errIsDirty && force {
		log.Warn("forcing rewrite on dirty repository")
		err = nil
	}
	if err != nil {
		return err
	}

	// check that current branch needs action
	what := "decrypted"
	if encrypt {
		what = "encrypted"
	}
	if encrypt == wasEncrypted && !force {
		log.Warnf("the branch is already %s", what)
		return nil
	}

	// check whether new branch exists
	where := "on current"
	if newBranch == "" {
		newBranch = curBranch
	}
	if newBranch != curBranch {
		where = "to new"
		if _, err := a.r.Branch(newBranch); err == nil && !force {
			return errors.New("target branch already exists")
		}
	}

	if gitPruneWorkaround {
		// use "git prune" to prevent "commit object not found"
		if err := execScript("git reset --hard; git status; git prune"); err != nil {
			return err
		}
		if err := a.getGit(); err != nil { // refresh git structures after prune
			return err
		}
	}

	// obtain and validate the branch commit log on clear repo
	hashLog, err := a.commitLog()
	if err != nil {
		return err
	}
	for i, hash := range hashLog {
		msg, err := a.commitMessage(hash, 80)
		shortHash := shortHash(hash)
		if err != nil {
			return errors.Wrapf(err, "pre-validate commit %s", shortHash)
		}
		log.Debugf("verified commit %d/%d %s %q", i+1, len(hashLog), shortHash, msg)
	}

	// worktree is unused, ignore file modtime
	baseOpts, err := a.getOptions()
	if err != nil {
		return err
	}
	baseOpts.fileModtime = false

	// use temporary branch for conversion
	tmpBranch, err := a.tempBranch(curBranch)
	if err != nil {
		return err
	}
	t := &transformer{
		a:          a,
		baseOpts:   baseOpts,
		encrypt:    encrypt,
		progress:   progress,
		hashLog:    hashLog,
		curBranch:  curBranch,
		tmpBranch:  tmpBranch,
		restoreCur: true,
		deleteTemp: true,
		transCache: map[plumbing.Hash]plumbing.Hash{},
	}
	defer t.finalize()

	// traverse commit log
	t.startAt = time.Now()
	t.oldDadHash = zeroHash
	t.newDadHash = zeroHash
	for i, hash := range t.hashLog {
		t.curHash = hash
		t.shortHash = shortHash(hash)
		t.reportProgress(i + 1)
		newHash, err := t.transformCommit()
		if err != nil {
			return errors.Wrapf(err, "convert commit %s", t.shortHash)
		}
		t.oldDadHash = hash
		t.newDadHash = newHash
		log.Debugf("~ commit transformed: %s -> %s", t.shortHash, shortHash(newHash))
	}
	t.reportProgress(-1)
	newHead := t.newDadHash

	// switch to resulting branch
	log.Debugf("switching branch %s -> %s at %s", tmpBranch, newBranch, shortHash(newHead))
	err = t.a.switchBranch(newHead, t.curBranch, newBranch, t.encrypt)
	if err != nil {
		return err
	}
	t.restoreCur = false
	t.deleteTemp = true
	fmt.Printf("%s %s branch '%s' at %s\n", what, where, newBranch, shortHash(newHead))
	return nil
}

func (t *transformer) transformCommit() (plumbing.Hash, error) {
	// get source commit
	var tree *object.Tree
	commit, err := t.a.r.CommitObject(t.curHash)
	if err == nil {
		tree, err = commit.Tree()
	}
	if err != nil {
		return zeroHash, errors.Wrapf(err, "get source tree for %s", t.shortHash)
	}
	oldTreeHash := tree.Hash

	// collect source trees
	t.treeCache = map[string]*object.Tree{}
	if err := t.collectTrees("", tree); err != nil {
		return zeroHash, err
	}
	t.treeCache[""] = tree

	// transform tree files
	matchingFiles, err := t.a.matchFiles(t.curHash.String())
	if err != nil {
		return zeroHash, errors.Wrapf(err, "match source files in %s", t.shortHash)
	}
	for _, path := range matchingFiles {
		if err := t.transformFile(path); err != nil {
			return zeroHash, errors.Wrapf(err, "transform file %s in %s", path, t.shortHash)
		}
	}

	// update tree hashes
	newTreeHash, err := t.updateHashes("", tree)
	if err != nil {
		return zeroHash, errors.Wrapf(err, "update tree hashes in %s", t.shortHash)
	}
	log.Debugf("~ tree(%s) %s -> %s", t.shortHash,
		shortLoc(oldTreeHash.String()), shortLoc(newTreeHash.String()))

	// update commit object
	commit.TreeHash = newTreeHash
	commit.ParentHashes = nil
	if t.newDadHash != zeroHash {
		commit.ParentHashes = []plumbing.Hash{t.newDadHash}
	}
	var newHash plumbing.Hash
	store := t.a.s
	obj := store.NewEncodedObject()
	if err = commit.Encode(obj); err == nil {
		newHash, err = store.SetEncodedObject(obj)
	}
	if err != nil {
		return zeroHash, errors.Wrapf(err, "commit changes for %s", t.shortHash)
	}
	return newHash, nil
}

func (t *transformer) transformFile(filePath string) error {
	// get source tree
	parentPath := path.Dir(filePath)
	fileName := path.Base(filePath)
	fileTree := t.treeCache[parentPath]
	if fileTree == nil {
		return errors.New("get source tree")
	}

	// get file entry
	entry, err := fileTree.FindEntry(fileName)
	if err != nil {
		return errors.Wrap(err, "get source hash")
	}
	if !entry.Mode.IsFile() || entry.Mode == filemode.Symlink {
		return errors.New("source is not a file")
	}
	log.Debugf("file1 tree %q [%s]", parentPath, traceTree(fileTree))

	// look up result in the transformation cache
	srcHash := entry.Hash
	dstHash := t.transCache[srcHash]
	if dstHash != zeroHash {
		entry.Hash = dstHash
		log.Debugf("%s:%s cache hit %s -> %s",
			t.shortHash, filePath, shortHash(srcHash), shortHash(dstHash))
		return nil
	}

	// get source data
	var srcText string
	file, err := fileTree.File(fileName)
	if err == nil {
		srcText, err = file.Contents()
	}
	if err != nil {
		return errors.Wrap(err, "read source")
	}
	srcData := []byte(srcText)

	// convert the data
	var dstData []byte
	switch {
	case len(srcData) == 0:
		dstData = srcData
	case t.encrypt:
		dstData, err = t.encryptFile(filePath, srcData)
	default:
		dstData, err = t.decryptFile(filePath, srcData)
	}
	if err != nil {
		return errors.Wrap(err, "transform contents")
	}

	// put result in storage, return its hash and cache it up
	if dstHash, err = t.a.writeGitBlob(dstData); err != nil {
		return errors.Wrapf(err, "write result")
	}
	t.transCache[srcHash] = dstHash
	entry.Hash = dstHash
	// entry.Mode &^= 007 // safety chmod "o-rwx" (dangerous)

	log.Debugf("%s:%s transformed %s -> %s",
		t.shortHash, filePath, shortHash(srcHash), shortHash(dstHash))
	log.Debugf("file2 tree %q [%s]", parentPath, traceTree(fileTree))
	return nil
}

func (t *transformer) reportProgress(step int) {
	elapsed := time.Since(t.startAt)
	total := len(t.hashLog)
	final := false
	if step < 0 {
		step = total
		final = true
	}
	if t.progress && final {
		report := fmt.Sprintf("%d commit(s) done in %v", step, elapsed.Round(time.Second))
		fmt.Fprintf(os.Stderr, "\r%-95s\n", report)
		return
	}
	msg, _ := t.a.commitMessage(t.curHash, 62)
	report := fmt.Sprintf("%d/%d %s %q", step, total, t.shortHash, msg)
	if !final {
		actionGlyph := "<"
		if t.encrypt {
			actionGlyph = ">"
		}
		log.Debugf("%s commit %s", actionGlyph, report)
	}
	if !t.progress {
		return
	}
	const throbChars = `/-\|`
	throbber := throbChars[step%len(throbChars)]
	expected := elapsed.Seconds()
	if total > 0 && step > 0 {
		expected = expected * float64(total) / float64(step)
	}
	timing := fmt.Sprintf("%d/%ds", int(elapsed.Seconds()), int(expected))
	fmt.Fprintf(os.Stderr, "\r> %7s %-81s %c\b", timing, report, throbber)
}
