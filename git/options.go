package git

import (
	"os"
	"strconv"
	"time"

	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/aes"
	"go.mozilla.org/sops/v3/age"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	"go.mozilla.org/sops/v3/keys"
	"go.mozilla.org/sops/v3/keyservice"
	"go.mozilla.org/sops/v3/version"
)

type options struct {
	a *action
	// common options
	cipher         sops.Cipher
	inputPath      string
	inputData      []byte
	inputStore     sops.Store
	outputStore    sops.Store
	keyServices    []keyservice.KeyServiceClient
	keyGroups      []sops.KeyGroup
	groupThreshold int
	ageRecipients  string
	indent         int
	fileModtime    bool
	// encrypt-only options
	meta              sops.Metadata
	unencryptedSuffix string
	encryptedSuffix   string
	unencryptedRegex  string
	encryptedRegex    string
	// decrypt-only options
	ignoreMac bool
	// mangling options
	encrypting bool
	mangleOpts mangleOpts
	renameKeys replace
	// comment options
	encryptedCommentSuffix string
	encryptedCommentPrefix string
}

const optUseGit = true

var zeroTime time.Time

func (o *options) forPath(path string) *options {
	copy := *o
	o = &copy
	o.inputPath = path
	o.inputStore = nil
	o.outputStore = nil
	o.meta.LastModified = zeroTime
	if path == "" {
		return o
	}

	getStore := func(typeParam string) sops.Store {
		defType := ""
		if typeParam != "" {
			defType = o.a.getString(typeParam, optUseGit)
		}
		return common.DefaultStoreForPathOrFormat(path, defType)
	}
	o.inputStore = getStore("input-type")
	o.outputStore = getStore("output-type")

	if !o.fileModtime {
		return o
	}
	fi, err := os.Stat(path)
	if err != nil {
		o.meta.LastModified = zeroTime
		return o
	}
	//log.Debugf("lastmodified %s: %v", path, fi.ModTime().UTC())
	o.meta.LastModified = fi.ModTime().UTC()
	return o
}

func (a *action) getOptions() (*options, error) {
	groups, err := a.getKeyGroups(optUseGit)
	if err != nil {
		return nil, err
	}
	threshold, err := a.getInt(optThreshold, optUseGit, 0)
	if err != nil {
		return nil, err
	}
	indent, err := a.getInt("indent", optUseGit, defaultIndent)
	if err != nil {
		return nil, err
	}
	ignoreMac, err := a.getBool("ignore-mac", optUseGit)
	if err != nil {
		return nil, err
	}
	fileModtime, err := a.getBool("file-modtime", optUseGit)
	if err != nil {
		return nil, err
	}
	mangleOpts, err := a.getMangleOpts("keep-formatting", optUseGit)
	if err != nil {
		return nil, err
	}
	renameKeys, err := a.getRenameKeys("rename-keys", optUseGit)
	if err != nil {
		return nil, err
	}

	o := &options{
		a: a,
		// filters
		unencryptedSuffix: a.getString("unencrypted-suffix", optUseGit),
		encryptedSuffix:   a.getString("encrypted-suffix", optUseGit),
		unencryptedRegex:  a.getString("unencrypted-regex", optUseGit),
		encryptedRegex:    a.getString("encrypted-regex", optUseGit),
		// parameters
		cipher:         aes.NewCipher(),
		keyServices:    a.getKeyServices(),
		keyGroups:      groups,
		groupThreshold: threshold,
		ageRecipients:  a.getString("age", optUseGit),
		indent:         indent,
		ignoreMac:      ignoreMac,
		fileModtime:    fileModtime,
		// mangling
		mangleOpts: mangleOpts,
		renameKeys: renameKeys,
		// comments
		encryptedCommentSuffix: a.getString("encrypted-comment-suffix", optUseGit),
		encryptedCommentPrefix: a.getString("encrypted-comment-prefix", optUseGit),
	}
	o.meta = sops.Metadata{
		KeyGroups:         o.keyGroups,
		UnencryptedSuffix: o.unencryptedSuffix,
		EncryptedSuffix:   o.encryptedSuffix,
		UnencryptedRegex:  o.unencryptedRegex,
		EncryptedRegex:    o.encryptedRegex,
		Version:           version.Version,
		ShamirThreshold:   o.groupThreshold,
	}
	return o, nil
}

func (o *options) save() (err error) {
	a := o.a
	if err = a.setString("age", o.ageRecipients); err != nil {
		return err
	}
	if err = a.setInt(optThreshold, o.groupThreshold); err != nil {
		return
	}
	if err = a.setBool("ignore-mac", o.ignoreMac); err != nil {
		return
	}
	if err = a.setBool("file-modtime", o.fileModtime); err != nil {
		return
	}
	if err = a.setInt("indent", o.indent); err != nil {
		return
	}
	if err = a.setString("keep-formatting", o.mangleOpts.String()); err != nil {
		return
	}
	if err = a.setString("rename-keys", o.renameKeys.String()); err != nil {
		return
	}
	if err = a.setString("unencrypted-suffix", o.unencryptedSuffix); err != nil {
		return
	}
	if err = a.setString("encrypted-suffix", o.encryptedSuffix); err != nil {
		return
	}
	if err = a.setString("unencrypted-regex", o.unencryptedRegex); err != nil {
		return
	}
	if err = a.setString("encrypted-regex", o.encryptedRegex); err != nil {
		return
	}
	if err = a.setString("encrypted-comment-suffix", o.encryptedCommentSuffix); err != nil {
		return
	}
	if err = a.setString("encrypted-comment-prefix", o.encryptedCommentPrefix); err != nil {
		return
	}
	return nil
}

func (a *action) getKeyServices() (svcs []keyservice.KeyServiceClient) {
	useLocal := a.c.Bool("enable-local-keyservice")
	if useLocal {
		svcs = append(svcs, keyservice.NewLocalClient())
	}
	return
}

func (a *action) getKeyGroups(useGit bool) ([]sops.KeyGroup, error) {
	var ageMasterKeys []keys.MasterKey
	ageRecipients := a.getString("age", useGit)
	if ageRecipients != "" {
		ageKeys, err := age.MasterKeysFromRecipients(ageRecipients)
		if err != nil {
			return nil, err
		}
		for _, k := range ageKeys {
			ageMasterKeys = append(ageMasterKeys, k)
		}
	}
	var group sops.KeyGroup
	group = append(group, ageMasterKeys...)
	log.Debugf("master keys: %+v", group)
	return []sops.KeyGroup{group}, nil
}

func (a *action) getString(name string, useGit bool) string {
	val := a.c.String(name)
	if val != "" || !useGit {
		return val
	}
	val, _ = a.configGet("", "sops."+name)
	return val
}

func (a *action) getInt(name string, useGit bool, defVal int) (int, error) {
	val := a.c.Int(name)
	if val == 0 && useGit && !a.c.IsSet(name) {
		gitVal, err := a.configGet("", "sops."+name)
		if err != nil {
			return defVal, err
		}
		if gitVal != "" {
			val, err = strconv.Atoi(gitVal)
			if err != nil {
				return defVal, err
			}
		}
	}
	if val == 0 {
		val = defVal
	}
	return val, nil
}

func (a *action) getBool(name string, useGit bool) (bool, error) {
	val := a.c.Bool(name)
	if !val && useGit && !a.c.IsSet(name) {
		gitVal, err := a.configGet("", "sops."+name)
		if err != nil {
			return false, err
		}
		if gitVal != "" {
			val, err = strconv.ParseBool(gitVal)
			if err != nil {
				return false, err
			}
		}
	}
	return val, nil
}
