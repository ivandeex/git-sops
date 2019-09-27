package git

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/pkg/errors"
)

func (a *action) markBranch(branchName string, encrypted, pushes bool) (err error) {
	if branchName == "" {
		if branchName, _, _, err = a.getState(); err != nil {
			return
		}
	}

	var branchCfg *config.Branch
	if branchCfg, err = a.r.Branch(branchName); err != nil {
		branchCfg = &config.Branch{Name: branchName}
		if err = a.r.CreateBranch(branchCfg); err != nil {
			return
		}
	}

	if err = a.configSet(branchName, "sops-encrypt", strconv.FormatBool(encrypted)); err != nil {
		return
	}

	if !pushes {
		return
	}
	const (
		Remote       = "remote"
		SavedRemote  = "sops-saved-remote"
		PushDisabled = "sops-push-disabled"
	)
	if !encrypted {
		// disable pushes on the branch which is decrypted and unsage
		remote, _ := a.configGet(branchName, Remote)
		if remote != "" && remote != PushDisabled {
			_ = a.configSet(branchName, SavedRemote, remote)
		} else {
			_ = a.configUnset(branchName, SavedRemote)
		}
		branchCfg.Remote = PushDisabled
		_ = a.configSet(branchName, Remote, PushDisabled)
	} else {
		// re-enable pushes on branch when it's encrypted
		remote, _ := a.configGet(branchName, SavedRemote)
		_ = a.configUnset(branchName, SavedRemote)
		if remote != "" && remote != PushDisabled {
			branchCfg.Remote = remote
			_ = a.configSet(branchName, Remote, remote)
		} else {
			branchCfg.Remote = ""
			_ = a.configUnset(branchName, Remote)
		}
	}
	return
}

// wrapper for git checkout that enforces correct index
func (a *action) checkoutWrapper(branchArg string, quiet, force, create bool) error {
	// check that worktree is clean
	rebase := false
	_, wasEncrypted, err := a.ensureClean("", false)
	if err == errIsDirty && force {
		log.Warn("forcing checkout on dirty repository")
		err = nil
	}
	if err == errRebasing && force && !create && branchArg == "" {
		log.Warn("force checkout while rebasing")
		err = nil
		rebase = true
	}
	if err != nil {
		return err
	}

	// run normal git checkout if needed
	if branchArg != "" {
		cmd := "git checkout "
		if quiet {
			cmd += "-q "
		}
		if force {
			cmd += "-f "
		}
		if create {
			cmd += "-b "
		}
		cmd += branchArg
		log.Infof("run command: %s", cmd)
		_, err := execCommand(cmd, true, nil)
		if err != nil {
			return errors.Wrap(err, "git checkout")
		}
	}

	// settle index vs worktree, fix remotes
	branch, _, encrypted, err := a.getState()
	if err == errRebasing && rebase {
		err = nil
	}
	if err != nil {
		return errors.Wrap(err, "get current branch")
	}
	if create && !rebase {
		encrypted = wasEncrypted
		if err := a.markBranch(branch, encrypted, true); err != nil {
			return err
		}
	}
	if rebase {
		branch = ""
	}
	if err = a.checkoutBranch(branch, encrypted); err != nil {
		return err
	}
	return nil
}

func (a *action) switchBranch(hash plumbing.Hash, oldBranch, newBranch string, encrypted bool) error {
	// delete target branch and its config
	if oldBranch != newBranch {
		if err := a.deleteBranch(newBranch, true); err != nil {
			return errors.Wrapf(err, "delete target branch %q", newBranch)
		}
	}

	// point target branch to the target commit
	ref := plumbing.NewBranchReferenceName(newBranch)
	if err := a.s.SetReference(plumbing.NewHashReference(ref, hash)); err != nil {
		return errors.Wrapf(err, "point target branch %q at %s", newBranch, shortHash(hash))
	}

	// copy branch remotes in git config
	if oldBranch != newBranch {
		if branchDesc, err := a.r.Branch(oldBranch); err == nil {
			branchDesc.Name = newBranch
			if err = a.r.CreateBranch(branchDesc); err != nil {
				return errors.Wrapf(err, "copy remotes from %q to %q", oldBranch, newBranch)
			}
		}
	}

	// fix branch remotes in git config
	if err := a.markBranch(newBranch, encrypted, true); err != nil {
		return errors.Wrapf(err, "mark branch %q as encrypted=%v", newBranch, encrypted)
	}

	// checkout the new branch
	if err := a.checkoutBranch(newBranch, true); err != nil {
		return errors.Wrapf(err, "checkout target branch %q", newBranch)
	}
	return nil
}

func (a *action) checkoutBranch(branch string, textconv bool) error {
	// perform forced checkout
	if branch != "" {
		err := a.w.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(branch),
			Force:  true,
		})
		if err != nil {
			return errors.Wrap(err, "checkout branch")
		}
	}
	// just quickly reset worktree if smudge not needed
	if !textconv {
		err := a.w.Reset(&git.ResetOptions{Mode: git.HardReset})
		if err != nil {
			return errors.Wrap(err, "reset worktree")
		}
		return nil
	}
	// need to smudge: decrypt worktree, fix permissions
	files, err := a.matchWorktree(nil)
	if err != nil {
		return errors.Wrap(err, "collect files")
	}
	if err = a.smudgeFiles(files); err != nil {
		return errors.Wrap(err, "decrypt worktree")
	}
	if err = a.chmodFiles(files); err != nil {
		return errors.Wrap(err, "fix permissions after decrypt")
	}
	// settle git index to worktree, fix permissions after it
	if err = execScript("git reset --hard"); err != nil {
		return errors.Wrap(err, "git reset")
	}
	if err = a.chmodFiles(files); err != nil {
		return errors.Wrap(err, "fix permissions after reset")
	}
	return nil
}

func (a *action) createBranch(branchName string) error {
	_ = a.deleteBranch(branchName, true)
	return a.w.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branchName),
		Create: true,
		Keep:   true,
	})
}

func (a *action) tempBranch(baseBranch string) (branch string, err error) {
	branch = baseBranch
	if branch != "" {
		branch += "-"
	}
	suffix := time.Now().UTC().Format("20060102-150405.000000000")
	suffix = strings.ReplaceAll(suffix, ".", "-")
	branch += "SOPS-" + suffix

	baseName := branch
	for i := 1; i < 100; i++ {
		_, err = a.r.Branch(branch)
		if err != nil {
			break
		}
		branch = fmt.Sprintf("%s-%d", baseName, i)
	}
	if err == nil {
		return "", errors.New("cannot make temporary branch")
	}

	if err = a.createBranch(branch); err != nil {
		return "", err
	}
	return
}

func (a *action) deleteBranch(branchName string, force bool) error {
	// check whether branch exists
	refName := plumbing.NewBranchReferenceName(branchName)
	if _, err := a.s.Reference(refName); err != nil {
		return nil // it really does not exist, fine
	}
	if !force {
		return fmt.Errorf("branch already exists: %s", branchName)
	}
	// remove branch reference
	if err := a.s.RemoveReference(refName); err != nil {
		return err
	}
	// remove branch config, if exists
	_ = a.r.DeleteBranch(branchName)
	return nil
}
