package git

import (
	"fmt"
	"io/ioutil"

	"github.com/pkg/errors"
	"go.mozilla.org/sops/v3"
)

func (a *action) mergeDriver(path, ancestor, current, other string) error {
	baseOpts, err := a.getOptions()
	if err != nil {
		return err
	}

	// query branch state
	rebase := false
	branch, hash, encrypted, err := a.getState()
	if err == errRebasing {
		rebase = true
	} else if err != nil {
		return err
	}
	log.Debugf("sops merge: %q %s '%s' %s",
		branch, shortHash(hash), filterStatus(encrypted, rebase, false), path)

	// record merge sources
	type source *struct {
		path string
		meta *sops.Metadata
	}
	sources := map[string]source{
		"ancestor": {path: ancestor},
		"current":  {path: current},
		"other":    {path: other},
	}
	log.Debugf("ancestor: %s current: %s other: %s", ancestor, current, other)

	// decrypt merge sources
	for role, s := range sources {
		log.Debugf("merge decrypting %s: %s", role, s.path)
		input, err := ioutil.ReadFile(s.path)
		if err != nil {
			return errors.Wrapf(err, "reading merged %s input from %s", role, s.path)
		}
		if len(input) == 0 {
			continue
		}
		opts := baseOpts.forPath(path)
		opts.inputData = input
		output, err := a.sopsDecrypt(opts)
		if isMetaNotFound(err, path) {
			continue
		}
		if err != nil {
			return errors.Wrapf(err, "decrypting merged %s", role)
		}
		s.meta = &opts.meta
		err = overwriteFile(s.path, output, true)
		if err != nil {
			return errors.Wrapf(err, "writing decrypted %s to %s", role, s.path)
		}
	}

	// perform 3-way merge
	diffOpt := "--no-diff3"
	if mergeStyle, _ := a.configGet("", "merge.conflictstyle"); mergeStyle == "diff3" {
		diffOpt = "--diff3"
	}
	mergeCmd := `git merge-file -L CURRENT -L ANCESTOR -L OTHER %s "%s" "%s" "%s"`
	mergeCmd = fmt.Sprintf(mergeCmd, diffOpt, current, ancestor, other)
	mergeOut, err := execCommand(mergeCmd, true, nil)
	log.Debugf("%q returned %v %q", mergeCmd, err, mergeOut)
	if err != nil {
		return fmt.Errorf("%s: merge-file failed: %v %q", path, errors.Cause(err), mergeOut)
	}
	if !encrypted {
		return nil
	}

	// read merge result
	input, err := ioutil.ReadFile(current)
	if err != nil || len(input) == 0 {
		return nil
	}
	opts := baseOpts.forPath(path)
	opts.inputData = input

	// pull source metadata
	for _, dad := range []string{"current", "ancestor", "other"} {
		if sources[dad] == nil {
			continue
		}
		if meta := sources[dad].meta; meta != nil {
			opts.meta.DataKey = meta.DataKey
			opts.meta.KeyGroups = meta.KeyGroups
			log.Debugf("pulled merge data key from %s", dad)
			break
		}
	}

	// encrypt merge result
	output, err := a.sopsEncrypt(opts)
	if err != nil {
		return errors.Wrapf(err, "encrypting merge result")
	}
	if err = overwriteFile(current, output, true); err != nil {
		return errors.Wrapf(err, "writing merge result")
	}
	return nil
}
