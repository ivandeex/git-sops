package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

const (
	gitDriver     = "sops"
	gitFlags      = "sops.configured"
	gitSections   = "sops filter.sops diff.sops merge.sops"
	optThreshold  = "shamir-secret-sharing-threshold"
	cacheTextconv = "true"
)

var gitSettings = map[string]string{
	"filter.[driver].clean":       "[program] clean %f",
	"filter.[driver].smudge":      "[program] smudge %f",
	"filter.[driver].required":    "true",
	"merge.[driver].driver":       "[program] merge %P %O %A %B",
	"merge.[driver].name":         "merge driver for secret files",
	"merge.[driver].recursive":    "binary",
	"merge.renormalize":           "true",
	"diff.[driver].textconv":      "[program] textconv",
	"diff.[driver].binary":        "false",
	"diff.[driver].cachetextconv": cacheTextconv,
}

var gitAliases = map[string]string{
	"rawlog": "! [program] rawlog --",
}

func (a *action) setupRepo(force bool, probeFile, probeText string) error {
	configured, _ := a.configGet("", "sops.configured")
	if configured != "" && !force {
		return errors.New("repository is already configured")
	}

	// query previous state
	branch, encrypted, err := a.ensureClean("", false)
	if err != nil {
		return err
	}
	repoOpts, err := a.getOptions()
	if err == nil {
		err = validateAgeRecipients(repoOpts.ageRecipients)
	}
	if err != nil {
		return err
	}

	// validate bare sops repo by probing a file
	shouldDecrypt := false
	if probeFile != "" {
		if probeText == "" {
			return errors.New("--probe-file requires --probe-text")
		}
		fileData, err := getInput(probeFile, false)
		if err != nil {
			return errors.Wrap(err, "read probe file")
		}
		opts := repoOpts.forPath(probeFile)
		opts.inputData = fileData
		data, err := a.sopsDecrypt(opts)
		switch {
		case err == nil:
			encrypted = true
			shouldDecrypt = true
		case isMetaNotFound(err, probeFile):
			data = fileData
		default:
			return errors.Wrap(err, "parse probe file")
		}
		if !strings.Contains(string(data), probeText) {
			return errors.New("probe file validation failed")
		}
	}

	// reset sops settings
	_ = a.teardownRepo(true)
	if err := repoOpts.save(); err != nil {
		return err
	}

	// safety_checks "$force" 'true'

	// determine executable path
	program, err := os.Executable()
	if err != nil {
		return err
	}
	if program, err = filepath.Abs(program); err != nil {
		return err
	}

	// configure git settings
	for key, val := range gitSettings {
		key = strings.ReplaceAll(key, "[driver]", gitDriver)
		val = strings.ReplaceAll(val, "[program]", program)
		if err2 := a.configSet("", key, val); err == nil {
			err = err2
		}
	}
	for _, flag := range strings.Split(gitFlags, " ") {
		if err2 := a.configSet("", flag, "true"); err == nil {
			err = err2
		}
	}
	for alias, command := range gitAliases {
		command = strings.ReplaceAll(command, "[program]", program)
		if err2 := a.configSet("", "alias."+alias, command); err == nil {
			err = err2
		}
	}
	if err != nil {
		return err
	}

	if err = os.Chmod(a.dotGit("config"), permSecret); err != nil {
		return err
	}

	if err = a.purgeCache(); err != nil {
		return err
	}

	// finish worktree setup
	if err := a.markBranch(branch, encrypted, false); err != nil {
		return err
	}
	if shouldDecrypt {
		fmt.Println("decrypting worktree")
		if err := a.checkoutBranch("", true); err != nil {
			return errors.Wrap(err, "decrypt worktree")
		}
	} else {
		if err := a.chmodFiles(nil); err != nil {
			return err
		}
	}
	if err := a.configSet("", "sops.configured", "true"); err != nil {
		return err
	}
	fmt.Println("setup complete")

	// print git status
	_, err = execCommand("git status --short", true, nil)
	return err
}

func (a *action) teardownRepo(quiet bool) error {
	// safety_checks 'ignore_dirty' 'true'

	for _, section := range strings.Split(gitSections, " ") {
		_ = a.removeSection(section)
	}
	for key := range gitSettings {
		key = strings.ReplaceAll(key, "[driver]", gitDriver)
		_ = a.configUnset("", key)
	}
	for alias := range gitAliases {
		_ = a.configUnset("", "alias."+alias)
	}
	for _, flag := range strings.Split(gitFlags, " ") {
		_ = a.configUnset("", flag)
	}

	// forceCheckout
	if !quiet {
		fmt.Println("teardown complete")
	}
	return nil
}

func (a *action) showStatus() error {
	branch, _, encrypted, _ := a.getState()
	configured, _ := a.configGet("", "sops.configured")
	fmt.Printf("directory:  %s\n", a.d)
	fmt.Printf("configured: %v\n", configured == "true")
	fmt.Printf("branch:     %s\n", branch)
	fmt.Printf("encrypted:  %v\n", encrypted)
	return nil
}
