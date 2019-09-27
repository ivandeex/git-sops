package git

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/pkg/errors"
	"go.mozilla.org/sops/v3"
)

func (a *action) clean(path string, stdin bool, parentLoc, lastModified string) error {
	rebase := false
	branch, hash, encrypted, err := a.getState()
	if err == errRebasing {
		rebase = true
	} else if err != nil {
		return err
	}
	log.Debugf("sops clean: %q %s '%s' %s",
		branch, shortHash(hash), filterStatus(encrypted, rebase, stdin), path)

	input, err := getInput(path, stdin)
	if err != nil {
		return err
	}
	if len(input) == 0 {
		return nil // preserve empty input
	}
	if !encrypted {
		_, err = os.Stdout.Write(input)
		return err
	}

	baseOpts, err := a.getOptions()
	if err != nil {
		return err
	}
	if stdin {
		baseOpts.fileModtime = false
	}
	opts := baseOpts.forPath(path)
	opts.inputData = input

	var (
		dadData []byte
		dadMeta *sops.Metadata
	)
	if parentLoc != "none" && parentLoc != "" {
		if dadData, err = a.readGitFile(path, parentLoc); err == nil {
			dadMeta, err = extractMetadata(path, dadData, opts)
		}
		if errors.Cause(err) == errNotFound || isMetaNotFound(err, path) {
			err = nil
		}
		if err != nil {
			return err
		}
	}
	if dadMeta != nil {
		log.Debugf("%s: parent data key from %s: '%x'", path, parentLoc, dadMeta.DataKey)
		opts.meta.DataKey = dadMeta.DataKey
		opts.meta.KeyGroups = dadMeta.KeyGroups
	}
	if lastModified != "" {
		const format = "2006-01-02T15:04:05"
		lastModifiedTime, err := time.Parse(format, lastModified)
		if err != nil {
			return fmt.Errorf("cannot parse time %q using format %q", lastModified, format)
		}
		opts.meta.LastModified = lastModifiedTime
	}
	output, err := a.sopsEncrypt(opts)
	if err == errAlreadyEncrypted {
		log.Debugf("%s: already encrypted", path)
		output = input
		err = nil
	} else if err != nil {
		return err
	}
	// file was not encrypted
	if dadData != nil && dadMeta != nil {
		// parent existed and was encrypted
		opts.inputData = dadData
		plainDad, err := a.sopsDecrypt(opts)
		if err != nil {
			return err
		}
		if bytes.Equal(input, plainDad) {
			log.Debugf("%s: equals decrypted parent", path)
			output = dadData
		} else {
			log.Debugf("%s: encrypting anew", path)
		}
	} else {
		log.Debugf("%s: encrypting", path)
	}
	if err == nil {
		_, err = os.Stdout.Write(output)
	}
	return err
}

func (a *action) smudge(path string, stdin bool, force bool) error {
	rebase := false
	branch, hash, encrypted, err := a.getState()
	if err == errRebasing {
		force = true
		rebase = true
	} else if err != nil {
		return err
	}
	log.Debugf("sops smudge: %q %s '%s' %s",
		branch, shortHash(hash), filterStatus(encrypted, rebase, stdin), path)

	input, err := getInput(path, stdin)
	if err != nil {
		return err
	}
	if len(input) == 0 {
		log.Debugf("%s: preserve empty input", path)
		return nil // preserve empty input
	}

	if !encrypted && !force {
		//log.Debugf("%s: branch not encrypted", path)
		_, err = os.Stdout.Write(input)
		return err
	}

	baseOpts, err := a.getOptions()
	if err != nil {
		return err
	}
	if stdin {
		baseOpts.fileModtime = false
	}
	opts := baseOpts.forPath(path)
	opts.inputData = input

	output, err := a.sopsDecrypt(opts)
	switch {
	case isMetaNotFound(err, path):
		log.Debugf("%s: already decrypted", path)
		output = input
		_, err = os.Stdout.Write(input)
	case isMergeConflict(err, input):
		log.Warnf("%s: found merge conflict", path)
		output = input
		_, err = os.Stdout.Write(input)
	case err == nil:
		log.Debugf("%s: decrypting", path)
		_, err = os.Stdout.Write(output)
	}
	if err != nil {
		log.Debugf("file %s error %#v %s", path, err, traceData(input, output, err))
	}
	return err
}

func filterStatus(encrypted, rebase, stdin bool) string {
	status := []byte("---")
	if encrypted {
		status[0] = 'e'
	}
	if rebase {
		status[1] = 'r'
	}
	if !stdin {
		status[2] = 'f'
	}
	return string(status)
}
