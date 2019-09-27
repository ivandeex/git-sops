package git

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	"go.mozilla.org/sops/v3/cmd/sops/formats"

	"github.com/pkg/errors"
)

var errAlreadyEncrypted = errors.New("file already encrypted")

func (t *transformer) encryptFile(path string, input []byte) ([]byte, error) {
	opts := t.baseOpts.forPath(path)
	dadsHash := shortHash(t.newDadHash)

	// pull metadata from dad file
	var dadData []byte
	var dadMeta *sops.Metadata
	if t.newDadHash != zeroHash {
		var err error
		dadData, err = t.a.readGitFile(path, t.newDadHash.String())
		if err == nil {
			dadMeta, _ = extractMetadata(path, dadData, opts)
		}
	}
	if dadMeta != nil {
		log.Debugf("%s:%s dad data key: [%x]", dadsHash, path, dadMeta.DataKey)
		opts.meta.DataKey = dadMeta.DataKey
		opts.meta.KeyGroups = dadMeta.KeyGroups
	}

	// encrypt file
	opts.inputData = input
	output, err := t.a.sopsEncrypt(opts)
	if err == errAlreadyEncrypted {
		log.Debugf("%s:%s already encrypted %s",
			t.shortHash, path, traceData(input, nil, nil))
		return input, nil
	}
	if err != nil {
		return nil, err
	}
	if dadData != nil && dadMeta != nil {
		// file was plain, dad was encrypted
		opts.inputData = dadData
		plainDad, err := t.a.sopsDecrypt(opts)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(input, plainDad) {
			log.Debugf("%s:%s plain file equals plain dad (%s) %s",
				t.shortHash, path, dadsHash, traceData(input, dadData, nil))
			return dadData, nil
		}
	}
	log.Debugf("%s:%s encrypting %s", t.shortHash, path, traceData(input, output, nil))
	return output, nil
}

func (a *action) sopsEncrypt(opts *options) ([]byte, error) {
	opts.encrypting = true
	// load the file
	path, err := filepath.Abs(opts.inputPath)
	if err != nil {
		return nil, err
	}
	inputData := opts.inputData
	if inputData == nil {
		fileBytes, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, err
		}
		inputData = fileBytes
	}
	inputData = a.yamlMangle(inputData, opts)
	branches, err := opts.inputStore.LoadPlainFile(inputData)
	if err != nil {
		return nil, err
	}

	// ensure no metadata
	for _, b := range branches[0] {
		if b.Key == "sops" {
			return nil, errAlreadyEncrypted
		}
	}

	tree := &sops.Tree{
		Branches: branches,
		Metadata: opts.meta,
		FilePath: path,
	}
	if err := renameTreeKeys(tree, opts.renameKeys); err != nil {
		return nil, err
	}

	// reuse or generate data key
	if opts.meta.DataKey == nil {
		dataKey, errs := tree.GenerateDataKeyWithKeyServices(opts.keyServices)
		if len(errs) > 0 {
			err = fmt.Errorf("could not generate data key: %s", errs)
			return nil, err
		}
		log.Debugf("generated data key: '%x'", dataKey)
		opts.meta.DataKey = dataKey
		opts.meta.KeyGroups = tree.Metadata.KeyGroups
	}

	// encrypt data
	err = common.EncryptTree(common.EncryptTreeOpts{
		Tree:         tree,
		Cipher:       opts.cipher,
		DataKey:      opts.meta.DataKey,
		LastModified: opts.meta.LastModified,
	})
	if err != nil {
		return nil, err
	}

	output, err := opts.outputStore.EmitEncryptedFile(*tree)
	if err != nil {
		return nil, err
	}
	output = a.yamlDemangle(output, opts)
	return output, nil
}

func (t *transformer) decryptFile(path string, input []byte) ([]byte, error) {
	opts := t.baseOpts.forPath(path)
	opts.inputData = input
	output, err := t.a.sopsDecrypt(opts)
	if isMetaNotFound(err, path) {
		log.Debugf("%s:%s already decrypted %s", t.shortHash, path, traceData(input, nil, nil))
		return input, nil
	}
	if err != nil {
		return nil, err
	}
	log.Debugf("%s:%s decrypting %s", t.shortHash, path, traceData(input, output, nil))
	return output, nil
}

func (a *action) sopsDecrypt(opts *options) ([]byte, error) {
	opts.encrypting = false
	loadOpts := common.GenericDecryptOpts{
		Cipher:      opts.cipher,
		InputStore:  opts.inputStore,
		InputPath:   opts.inputPath,
		IgnoreMAC:   opts.ignoreMac,
		KeyServices: opts.keyServices,
	}
	inputData := a.yamlMangle(opts.inputData, opts)
	tree, err := loadEncryptedFileDataWithBugFixes(loadOpts, inputData)
	if err != nil {
		return nil, err
	}

	dataKey, err := common.DecryptTree(common.DecryptTreeOpts{
		Cipher:      opts.cipher,
		IgnoreMac:   opts.ignoreMac,
		Tree:        tree,
		KeyServices: opts.keyServices,
	})
	if err != nil {
		return nil, err
	}
	log.Debugf("%s: source data key: '%x'", opts.inputPath, dataKey)

	if err := renameTreeKeys(tree, opts.renameKeys); err != nil {
		return nil, err
	}
	output, err := opts.outputStore.EmitPlainFile(tree.Branches)
	if err != nil {
		return nil, err
	}
	output = a.yamlDemangle(output, opts)
	return output, nil
}

func isMetaNotFound(err error, path string) bool {
	const errTextBadJSON = "Error unmarshalling input json"
	if err == sops.MetadataNotFound {
		return true
	}
	if err != nil {
		fmt := formats.FormatForPath(path)
		if fmt == formats.Binary && strings.Contains(err.Error(), errTextBadJSON) {
			return true
		}
	}
	return false
}

func isMergeConflict(err error, input []byte) bool {
	const errTextBadYAML = "Error unmarshalling input yaml"
	if err == nil || !strings.Contains(err.Error(), errTextBadYAML) {
		return false
	}
	var currMark = []byte("<<<<<<< CURRENT")
	var otherMark = []byte(">>>>>>> OTHER")
	return bytes.Contains(input, currMark) && bytes.Contains(input, otherMark)
}
