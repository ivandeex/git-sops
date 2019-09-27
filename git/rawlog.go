package git

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/google/shlex"
	"github.com/pkg/errors"
	"gopkg.in/urfave/cli.v1"
)

type skipFilters struct {
	hunkMarks  bool
	metadata   bool
	encrypted  bool
	blankLines bool
	sameLines  bool
	removals   bool
	// state
	yamlMetadata bool
	jsonMetadata bool
}

func newSkipFilters(cli *cli.Context) *skipFilters {
	all := cli.Bool("skip-all")
	return &skipFilters{
		hunkMarks:  all || cli.Bool("skip-hunk-marks"),
		metadata:   all || cli.Bool("skip-metadata"),
		encrypted:  all || cli.Bool("skip-encrypted"),
		blankLines: all || cli.Bool("skip-blank-lines"),
		sameLines:  all || cli.Bool("skip-same-lines"),
		removals:   all || cli.Bool("skip-removals"),
	}
}

func (f *skipFilters) active() bool {
	return (f.hunkMarks ||
		f.metadata ||
		f.encrypted ||
		f.blankLines ||
		f.sameLines ||
		f.removals)
}

var reColorEscape = regexp.MustCompile("\x1b\\[[^m]*m")

func (f *skipFilters) skipLine(line string) (skip bool) {
	s := strings.TrimRight(reColorEscape.ReplaceAllString(line, ""), " \t\r")
	if s == "" {
		return f.blankLines
	}
	switch s[0] {
	case '+':
		if strings.HasPrefix(s, "+++ ") && strings.Contains(s, "/") {
			f.yamlMetadata = false
			f.jsonMetadata = false
			return false
		}
		s = s[1:]
	case '-':
		if strings.HasPrefix(s, "--- ") && strings.Contains(s, "/") {
			f.yamlMetadata = false
			f.jsonMetadata = false
			return false
		}
		if f.removals {
			skip = true
		}
		s = s[1:]
	case ' ':
		if f.sameLines {
			skip = true
		}
		s = s[1:]
	case '@':
		if f.hunkMarks && strings.HasPrefix(s, "@@ ") {
			skip = true
		}
	case 'd':
		if strings.HasPrefix(s, "diff ") {
			f.yamlMetadata = false
			f.jsonMetadata = false
		}
	case 'i':
		if strings.HasPrefix(s, "index ") {
			f.yamlMetadata = false
			f.jsonMetadata = false
		}
	}
	switch s {
	case "":
		if f.blankLines {
			skip = true
		}
	case "sops:":
		f.yamlMetadata = true
	case "...":
		f.yamlMetadata = false
	case "\t\"sops\": {":
		f.jsonMetadata = true
	case "}":
		f.jsonMetadata = false
	default:
		if f.encrypted && strings.Contains(s, "ENC[AES256_GCM,") {
			skip = true
		}
	}
	if f.metadata && (f.yamlMetadata || f.jsonMetadata) {
		skip = true
	}
	return skip
}

func (a *action) rawLog(colorize bool, filters *skipFilters) error {
	envVal, envSet := os.LookupEnv(envFiltering)
	_ = os.Setenv(envFiltering, "false")
	defer func() {
		if envSet {
			_ = os.Setenv(envFiltering, envVal)
		} else {
			_ = os.Unsetenv(envFiltering)
		}
	}()

	var pipeReader *io.PipeReader
	var pipeWriter *io.PipeWriter
	filtering := filters.active()
	if filtering {
		pipeReader, pipeWriter = io.Pipe()
	}

	cmd := "git log --patch --no-textconv"
	if colorize {
		cmd += " --color=always"
	}
	ext := strings.Join(a.c.Args(), " ")
	if ext == "" {
		const fmt = `%C(bold blue)%h%C(reset) - %C(white)%s%C(reset)%C(bold yellow)%d%C(reset)`
		ext = `--abbrev-commit --decorate --date=relative --format="` + fmt + `"`
	}
	cmd = cmd + " " + ext
	log.Debug(cmd)

	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		var output io.Writer
		if filtering {
			output = pipeWriter
		}
		_, err = execCommand(cmd, true, output)
		if filtering {
			_ = pipeWriter.Close()
		}
		wg.Done()
	}()

	if filtering {
		scanner := bufio.NewScanner(pipeReader)
		for scanner.Scan() {
			line := scanner.Text()
			if !filters.skipLine(line) {
				fmt.Println(line)
			}
		}
		defer func() {
			_ = pipeReader.Close()
		}()
	}

	wg.Wait()
	return err
}

func execCommand(command string, interactive bool, stdout io.Writer) (out string, err error) {
	var tokens []string
	if tokens, err = shlex.Split(command); err != nil {
		return
	}
	prog, args := tokens[0], tokens[1:]
	cmd := exec.Command(prog, args...)
	if interactive {
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr
		if stdout == nil {
			stdout = os.Stdout
		}
		cmd.Stdout = stdout
		err = cmd.Run()
	} else {
		var byteOut []byte
		byteOut, err = cmd.CombinedOutput()
		out = string(byteOut)
	}
	if err != nil && out != "" {
		_, _ = fmt.Print(out)
	}
	if err != nil {
		err = errors.Wrapf(err, "%s failed", cmd)
	}
	return
}

func execScript(script string) error {
	for _, cmd := range strings.Split(script, ";") {
		if cmd = strings.TrimSpace(cmd); cmd != "" {
			if _, err := execCommand(cmd, false, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
