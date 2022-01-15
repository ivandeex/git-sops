package git

import (
	"fmt"

	"go.mozilla.org/sops/v3/cmd/sops/codes"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	"go.mozilla.org/sops/v3/mangle"
	"go.mozilla.org/sops/v3/version"

	"gopkg.in/urfave/cli.v1"
)

// Version represents the current semantic version
var Version = "0.0.0-dev"

var (
	errExitNoFile    = common.NewExitError("Error: no file specified", codes.NoFileSpecified)
	errExitExtraArgs = common.NewExitError("Error: extra arguments", codes.ErrorGeneric)
)

func Commands() []cli.Command {
	keyServiceFlags := []cli.Flag{
		cli.BoolTFlag{
			Name:  "enable-local-keyservice",
			Usage: "use local key service",
		},
		cli.StringSliceFlag{
			Name:  "keyservice",
			Usage: "Specify the key services to use in addition to the local one. Can be specified more than once. Syntax: protocol://address. Example: tcp://myserver.com:5000",
		},
	}

	gitFlags := append(
		keyServiceFlags,
		cli.IntFlag{
			Name:  optThreshold,
			Usage: "The number of master keys required to retrieve the data key with shamir",
		},
		cli.StringFlag{
			Name:   "age, a",
			Usage:  "Age recipient",
			EnvVar: "SOPS_AGE",
		},
		cli.IntFlag{
			Name:   "indent",
			Usage:  "Set default YAML indent",
			EnvVar: "SOPS_INDENT",
		},
		cli.StringFlag{
			Name:   "rename-keys",
			Usage:  "Rename YAML keys from1:to1,from2:to2,...",
			EnvVar: "SOPS_RENAME_KEYS",
		},
		cli.StringFlag{
			Name:   "encrypted-comment-suffix",
			Usage:  `Also encrypt comments with given suffix (all if "all" or unset, none if "none")`,
			EnvVar: "SOPS_ENCRYPTED_COMMENT_SUFFIX",
		},
		cli.StringFlag{
			Name:   "encrypted-comment-prefix",
			Usage:  `Also encrypt comments with given prefix`,
			EnvVar: "SOPS_ENCRYPTED_COMMENT_PREFIX",
		},
		cli.StringFlag{
			Name:   "keep-formatting",
			Usage:  "Keep YAML formatting: " + mangle.MangleAll,
			EnvVar: "SOPS_KEEP_FORMATTING",
		},
		cli.BoolFlag{
			Name:   "ignore-mac",
			Usage:  "Ignore MAC mismatch",
			EnvVar: "SOPS_IGNORE_MAC",
		},
		cli.BoolFlag{
			Name:   "file-modtime",
			Usage:  "Use file modtime as metadata lastmodified",
			EnvVar: "SOPS_FILE_MODTIME",
		},
		cli.StringFlag{
			Name:   "change-dir, C",
			Usage:  "Run as if started in given path instead of current directory",
			EnvVar: "SOPS_CHANGE_DIR",
		},
		cli.BoolFlag{
			Name:   "verbose, v",
			Usage:  "Enable verbose logging output",
			EnvVar: "SOPS_VERBOSE",
		},
		cli.BoolFlag{
			Name:   "trace",
			Usage:  "Enable trace logging output",
			EnvVar: "SOPS_TRACE",
		},
	)

	gitCommands := []cli.Command{
		{
			Name:      "clean",
			Usage:     `encrypt file data from stdin to stdout for given path`,
			ArgsUsage: `path`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:  "read-file",
					Usage: `read fata directly from given file (by default from stdin)`,
				},
				cli.StringFlag{
					Name:  "parent",
					Usage: `parent file location, e.g. commit hash, "none", "worktree" or "index" (default)`,
				},
				cli.StringFlag{
					Name:  "last-modified",
					Usage: `use fixed modtime formatted as YYYY-MM-DDThh:mm:ss UTC (by default use file modtime)`,
				},
			),
			Action: func(cli *cli.Context) error {
				if cli.NArg() != 1 {
					return errExitNoFile
				}
				parent := cli.String("parent")
				if parent == "" {
					parent = "index"
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.clean(cli.Args()[0], !cli.Bool("read-file"),
						parent, cli.String("last-modified"))
				}
				return err
			},
		},
		{
			Name:      "smudge",
			Usage:     `decrypt file data from stdin to stdout for given path`,
			ArgsUsage: `path`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:  "read-file",
					Usage: `read fata directly from given file (by default from stdin)`,
				},
			),
			Action: func(cli *cli.Context) error {
				if cli.NArg() != 1 {
					return errExitNoFile
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.smudge(cli.Args()[0], !cli.Bool("read-file"), false)
				}
				return err
			},
		},
		{
			Name:      "textconv",
			Usage:     `decrypt data from given file to stdout`,
			ArgsUsage: `file`,
			Flags:     gitFlags,
			Action: func(cli *cli.Context) error {
				if cli.NArg() != 1 {
					return errExitNoFile
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.smudge(cli.Args()[0], false, true)
				}
				return err
			},
		},
		{
			Name:      "merge",
			Usage:     `merge encrypted branches`,
			ArgsUsage: `file ancestor current other`,
			Flags:     gitFlags,
			Action: func(cli *cli.Context) error {
				if cli.NArg() != 4 {
					return errExitNoFile
				}
				args := cli.Args()
				a, err := newAction(cli)
				if err == nil {
					err = a.mergeDriver(args[0], args[1], args[2], args[3])
				}
				return err
			},
		},
		{
			Name:      "encrypt",
			Usage:     `encrypt current branch history`,
			ArgsUsage: `[branch]`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:  "force, f",
					Usage: "delete target branch if it exists",
				},
				cli.BoolFlag{
					Name:  "progress, P",
					Usage: "print progress",
				},
			),
			Action: func(cli *cli.Context) error {
				var branch string
				switch cli.NArg() {
				case 0:
					branch = ""
				case 1:
					branch = cli.Args()[0]
				default:
					return errExitExtraArgs
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.transformBranch(branch, true, cli.Bool("force"), cli.Bool("progress"))
				}
				return err
			},
		},
		{
			Name:      "decrypt",
			Usage:     `decrypt current branch history`,
			ArgsUsage: `[branch]`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:  "force, f",
					Usage: "delete target branch if it exists",
				},
				cli.BoolFlag{
					Name:  "progress, P",
					Usage: "print progress",
				},
			),
			Action: func(cli *cli.Context) error {
				var branch string
				switch cli.NArg() {
				case 0:
					branch = ""
				case 1:
					branch = cli.Args()[0]
				default:
					return errExitExtraArgs
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.transformBranch(branch, false, cli.Bool("force"), cli.Bool("progress"))
				}
				return err
			},
		},
		{
			Name:      "set-encrypted",
			Usage:     `mark current branch as encrypted, re-enable push`,
			ArgsUsage: `[branch]`,
			Flags:     gitFlags,
			Action: func(cli *cli.Context) error {
				branch := ""
				if cli.NArg() > 0 {
					branch = cli.Args()[0]
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.markBranch(branch, true, true)
				}
				return err
			},
		},
		{
			Name:      "set-decrypted",
			Usage:     `mark current branch as decrypted, disable push`,
			ArgsUsage: `[branch]`,
			Flags:     gitFlags,
			Action: func(cli *cli.Context) error {
				branch := ""
				if cli.NArg() > 0 {
					branch = cli.Args()[0]
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.markBranch(branch, false, true)
				}
				return err
			},
		},
		{
			Name:  "setup",
			Usage: `setup git repository for SOPS encryption`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:   "force, f",
					Usage:  "force action if repository is already setup",
					EnvVar: "SOPS_FORCE",
				},
				cli.StringFlag{
					Name:   "probe-file",
					Usage:  "setup bare repository by probing a file",
					EnvVar: "SOPS_PROBE_FILE",
				},
				cli.StringFlag{
					Name:   "probe-text",
					Usage:  "expected contents of the probed file",
					EnvVar: "SOPS_PROBE_TEXT",
				},
			),
			Action: func(cli *cli.Context) error {
				a, err := newAction(cli)
				if err == nil {
					err = a.setupRepo(cli.Bool("force"), cli.String("probe-file"), cli.String("probe-text"))
				}
				return err
			},
		},
		{
			Name:  "teardown",
			Usage: `remove SOPS settings from git repository`,
			Flags: gitFlags,
			Action: func(cli *cli.Context) error {
				a, err := newAction(cli)
				if err == nil {
					err = a.teardownRepo(false)
				}
				return err
			},
		},
		{
			Name:  "status",
			Usage: `show SOPS encryption status for current branch`,
			Flags: gitFlags,
			Action: func(cli *cli.Context) error {
				a, err := newAction(cli)
				if err == nil {
					err = a.showStatus()
				}
				return err
			},
		},
		{
			Name:  "ls",
			Usage: `list secret files eligible for encryption`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:  "staged",
					Usage: "Walk index instead of worktree",
				},
			),
			Action: func(cli *cli.Context) error {
				a, err := newAction(cli)
				if err == nil {
					err = a.listFiles(cli.Bool("staged"))
				}
				return err
			},
		},
		{
			Name:  "chmod",
			Usage: `change permissions on secret files to prevent others access`,
			Flags: gitFlags,
			Action: func(cli *cli.Context) error {
				a, err := newAction(cli)
				if err == nil {
					err = a.chmodFiles(nil)
				}
				return err
			},
		},
		{
			Name:  "checkout",
			Usage: `perform git checkout and settle index`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:  "quiet, q",
					Usage: "be quiet",
				},
				cli.BoolFlag{
					Name:  "force, f",
					Usage: "force checkout if worktree is dirty",
				},
				cli.BoolFlag{
					Name:  "branch, b",
					Usage: "create a new branch",
				},
			),
			Action: func(cli *cli.Context) error {
				branch := ""
				if cli.NArg() > 0 {
					branch = cli.Args()[0]
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.checkoutWrapper(branch,
						cli.Bool("quiet"), cli.Bool("force"), cli.Bool("branch"))
				}
				return err
			},
		},
		{
			Name:  "rawlog",
			Usage: `show git log with raw encrypted blobs`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:   "colorize, c",
					Usage:  `Show colored diff`,
					EnvVar: "SOPS_COLORIZE",
				},
				cli.BoolFlag{
					Name:   "skip-all, A",
					Usage:  `Skip all supported patterns i.e. same as -H -M -E -B -S -R together`,
					EnvVar: "SOPS_SKIP_ALL",
				},
				cli.BoolFlag{
					Name:   "skip-hunk-marks, H",
					Usage:  `Skip hunk marks e.g. "@@ -9,4 +15,4"`,
					EnvVar: "SOPS_SKIP_HUNK_MARKS",
				},
				cli.BoolFlag{
					Name:   "skip-metadata, M",
					Usage:  `Skip metadata section`,
					EnvVar: "SOPS_SKIP_METADATA",
				},
				cli.BoolFlag{
					Name:   "skip-encrypted, E",
					Usage:  `Skip encrypted keys`,
					EnvVar: "SOPS_SKIP_ENCRYPTED",
				},
				cli.BoolFlag{
					Name:   "skip-blank-lines, B",
					Usage:  `Skip blank lines`,
					EnvVar: "SOPS_SKIP_BLANK_LINES",
				},
				cli.BoolFlag{
					Name:   "skip-same-lines, S",
					Usage:  `Skip same lines`,
					EnvVar: "SOPS_SKIP_SAME_LINES",
				},
				cli.BoolFlag{
					Name:   "skip-removals, R",
					Usage:  `Skip removed lines`,
					EnvVar: "SOPS_SKIP_REMOVALS",
				},
			),
			Action: func(cli *cli.Context) error {
				a, err := newAction(cli)
				if err == nil {
					err = a.rawLog(cli.Bool("colorize"), newSkipFilters(cli))
				}
				return err
			},
		},
		{
			Name:  "test-mangle",
			Usage: `test-mangle`,
			Flags: append(
				gitFlags,
				cli.BoolFlag{
					Name:  "mangle",
					Usage: "mangle/demangle input yaml",
				},
			),
			Action: func(cli *cli.Context) error {
				path := ""
				if cli.NArg() > 0 {
					path = cli.Args()[0]
				}
				a, err := newAction(cli)
				if err == nil {
					err = a.testMangle(path, cli.Bool("mangle"))
				}
				return err
			},
		},
		{
			Name:  "version",
			Usage: `version`,
			Flags: gitFlags,
			Action: func(cli *cli.Context) error {
				fmt.Printf("%s (sops %s)\n", Version, version.Version)
				return nil
			},
		},
	}

	return gitCommands
}
