package git

import (
	"os"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/logging"
	"go.mozilla.org/sops/v3/stores/yaml"

	"github.com/sirupsen/logrus"
	"gopkg.in/urfave/cli.v1"
)

var log *logrus.Logger

func init() {
	log = logging.NewLogger("GIT")
}

const defaultIndent = 2

type action struct {
	c *cli.Context        // cli
	r *git.Repository     // repo
	w *git.Worktree       // worktree
	s *filesystem.Storage // storer
	d string              // repo dir
}

// newAction creates "action" wrapper for cli and repository
func newAction(cli *cli.Context) (*action, error) {
	// setup logging
	fmt := &logrus.TextFormatter{
		ForceColors:     true,
		FullTimestamp:   true,
		TimestampFormat: "15:04:05.000", // FIXME
	}
	for _, log := range logging.Loggers {
		log.SetFormatter(fmt)
	}
	if cli.Bool("verbose") || cli.GlobalBool("verbose") {
		logging.SetLevel(logrus.DebugLevel)
	}
	if cli.Bool("trace") || cli.GlobalBool("trace") {
		logging.SetLevel(logrus.TraceLevel)
		traceMangling = true
	}
	sops.EncryptedCommentSuffix = mangleComment

	// setup git
	if changeDir := cli.String("change-dir"); changeDir != "" {
		if err := os.Chdir(changeDir); err != nil {
			return nil, err
		}
	}
	a := &action{c: cli}
	if err := a.getGit(); err != nil {
		return nil, err
	}

	// setup yaml indent
	indent, _ := a.getInt("indent", true, defaultIndent)
	yaml.Indent = indent

	return a, nil
}
