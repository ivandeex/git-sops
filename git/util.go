package git

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/go-git/go-git/v5/plumbing"

	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/cmd/sops/common"
)

const permSecret = 0600

var errInvalidAgeRecs = errors.New("invalid or absent encryption password")

func getInput(path string, stdin bool) (data []byte, err error) {
	if stdin {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(path)
	}
	return
}

// age recipients must be present as a comment in the age key file
func validateAgeRecipients(ageRecipients string) error {
	if ageRecipients == "" {
		return errInvalidAgeRecs
	}

	path, ok := os.LookupEnv("SOPS_AGE_KEY_FILE")
	if !ok {
		confDir, err := os.UserConfigDir()
		if err != nil {
			return errors.Wrap(err, "cannot determine user config directory")
		}
		path = filepath.Join(confDir, "sops", "age", "keys.txt")
	}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return errors.Wrapf(err, "failed to open file %s")
	}

	if !bytes.Contains(data, []byte(ageRecipients)) {
		return errInvalidAgeRecs
	}
	return nil
}

func extractMetadata(path string, data []byte, opts *options) (*sops.Metadata, error) {
	loadOpts := common.GenericDecryptOpts{
		Cipher:      opts.cipher,
		InputStore:  opts.inputStore,
		InputPath:   path,
		IgnoreMAC:   true,
		KeyServices: opts.keyServices,
	}
	tree, err := loadEncryptedFileDataWithBugFixes(loadOpts, data)
	if err != nil {
		return nil, err
	}
	meta := &tree.Metadata
	dataKey, err := meta.GetDataKeyWithKeyServices(opts.keyServices)
	if err != nil {
		return nil, err
	}
	meta.DataKey = dataKey
	return meta, nil
}

// loadEncryptedFileDataWithBugFixes is a wrapper around loadEncryptedFileData
// which includes check for issue https://github.com/mozilla/sops/pull/435
//
// Note: This copy of common.LoadEncryptedFileWithBugFixes supports direct data bytes
func loadEncryptedFileDataWithBugFixes(opts common.GenericDecryptOpts, inputData []byte) (*sops.Tree, error) {
	tree, err := loadEncryptedFileData(opts.InputStore, opts.InputPath, inputData)
	if err != nil {
		return nil, err
	}

	encCtxBug, err := common.DetectKMSEncryptionContextBug(tree)
	if err != nil {
		return nil, err
	}
	if encCtxBug {
		tree, err = common.FixAWSKMSEncryptionContextBug(opts, tree)
		if err != nil {
			return nil, err
		}
	}

	return tree, nil
}

// loadEncryptedFileData loads an encrypted SOPS file, returning a SOPS tree
//
// Note: This copy of common.LoadEncryptedFile supports direct data bytes
func loadEncryptedFileData(loader sops.EncryptedFileLoader, inputPath string, inputData []byte) (*sops.Tree, error) {
	if inputData == nil {
		fileData, err := ioutil.ReadFile(inputPath)
		if err != nil {
			return nil, err
		}
		inputData = fileData
	}
	path, err := filepath.Abs(inputPath)
	if err != nil {
		return nil, err
	}
	tree, err := loader.LoadEncryptedFile(inputData)
	tree.FilePath = path
	return &tree, err
}

func traceData(in, out []byte, err error) string {
	if log.Level < logrus.TraceLevel && err == nil {
		return ""
	}
	return fmt.Sprintf("\n<<<<<\n%s\n>>>>>\n%s\n~~~~~",
		strings.TrimSpace(string(in)), strings.TrimSpace(string(out)))
}

const shortLen = 8

func shortLoc(loc string) string {
	if len(loc) > shortLen {
		return loc[:shortLen]
	}
	return loc
}

func shortHash(hash plumbing.Hash) string {
	return hash.String()[:shortLen]
}

func overwriteFile(path string, data []byte, keepTimes bool) error {
	path = filepath.FromSlash(path)
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	perm := fi.Mode().Perm()
	if err := ioutil.WriteFile(path, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(path, perm); err != nil {
		return err
	}
	if keepTimes {
		mtime := fi.ModTime()
		if err = os.Chtimes(path, mtime, mtime); err != nil {
			return err
		}
	}
	return nil
}
