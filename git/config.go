package git

import (
	"fmt"
	"strconv"
	"strings"
)

func (a *action) configGet(branch, name string) (string, error) {
	cfg, err := a.r.Config()
	if err != nil {
		return "", err
	}
	sec, sub, key, err := configLocate(branch, name)
	if err != nil {
		return "", err
	}
	raw := cfg.Raw
	if !raw.HasSection(sec) {
		return "", nil
	}
	s := raw.Section(sec)
	if sub == "" {
		if !s.HasOption(key) {
			return "", nil
		}
		return s.Option(key), nil
	}
	if !s.HasSubsection(sub) {
		return "", nil
	}
	ss := s.Subsection(sub)
	if !ss.HasOption(key) {
		return "", nil
	}
	return ss.Option(key), nil
}

func (a *action) configSet(branch, name, val string) error {
	cfg, err := a.r.Config()
	if err != nil {
		return err
	}
	sec, sub, key, err := configLocate(branch, name)
	if err != nil {
		return err
	}
	_ = cfg.Raw.SetOption(sec, sub, key, val)
	return a.r.SetConfig(cfg)
}

func (a *action) setString(name string, val string) error {
	return a.configSet("", "sops."+name, val)
}

func (a *action) setInt(name string, val int) error {
	return a.setString(name, strconv.Itoa(val))
}

func (a *action) setBool(name string, val bool) error {
	return a.setString(name, strconv.FormatBool(val))
}

func (a *action) configUnset(branch, name string) error {
	cfg, err := a.r.Config()
	if err != nil {
		return err
	}
	sec, sub, key, err := configLocate(branch, name)
	if err != nil {
		return err
	}
	raw := cfg.Raw
	if !raw.HasSection(sec) {
		return nil
	}
	s := raw.Section(sec)
	if sub == "" {
		if !s.HasOption(key) {
			return nil
		}
		_ = s.RemoveOption(key)
	} else {
		if !s.HasSubsection(sub) {
			return nil
		}
		ss := s.Subsection(sub)
		if !ss.HasOption(key) {
			return nil
		}
		_ = ss.RemoveOption(key)
	}
	return a.r.SetConfig(cfg)
}

func (a *action) removeSection(name string) error {
	cfg, err := a.r.Config()
	if err != nil {
		return err
	}
	sec, sub := name, ""
	if dot := strings.Index(name, "."); dot != -1 {
		sec, sub = name[:dot], name[dot+1:]
	}
	raw := cfg.Raw
	if sec == "" || !raw.HasSection(sec) {
		return nil
	}
	if sub != "" && !raw.Section(sec).HasSubsection(sub) {
		return nil
	}
	if sub == "" {
		_ = raw.RemoveSection(sec)
	} else {
		_ = raw.RemoveSubsection(sec, sub)
	}
	return a.r.SetConfig(cfg)
}

func configLocate(branch, name string) (sec string, sub string, key string, err error) {
	err = fmt.Errorf("invalid key %q for branch %q", name, branch)
	key = name
	if dot := strings.Index(key, "."); dot != -1 {
		sec, key = key[:dot], key[dot+1:]
	}
	if dot := strings.Index(key, "."); dot != -1 {
		sub, key = key[:dot], key[dot+1:]
	}
	if branch == "" {
		if sec != "" && key != "" {
			err = nil
		}
		return
	}
	if sec == "" && sub == "" && key != "" {
		sec, sub = "branch", branch
		err = nil
	}
	return
}
