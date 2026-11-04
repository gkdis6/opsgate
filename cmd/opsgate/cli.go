package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/gkdis6/opsgate/internal/buildinfo"
)

// usageError marks an invalid invocation, which exits with status 2.
type usageError string

func (e usageError) Error() string { return string(e) }

// command is a leaf with run set or a group with children.
type command struct {
	name, summary string
	run           func([]string) error
	children      []command
}

// application holds the streams and the server entry point, so tests can
// replace them.
type application struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	server         func(string, string) error
}

// cli runs the command line and returns the process exit status: 0 for success
// and help, 2 for usage errors, 1 for operational errors.
func cli(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &application{stdin: stdin, stdout: stdout, stderr: stderr, server: run}
	return c.execute(args)
}

func (c *application) commands() []command {
	return []command{
		{name: "dev-runner", summary: "Run configured coding agents", run: c.runDevRunner},
		{name: "watch", summary: "Watch Kubernetes and send webhook or email alerts", run: c.runWatch},
		{name: "init", summary: "Write a configuration interactively or with flags", run: c.runInit},
		{name: "config", summary: "Inspect configuration", children: []command{
			{name: "show", summary: "Print the effective configuration", run: c.runConfigShow},
		}},
		{name: "passkey", summary: "Manage passkey registration", children: []command{
			{name: "invite", summary: "Print a one-time registration link", run: c.runPasskeyInvite},
		}},
		{name: "version", summary: "Print version", run: c.runVersion},
		{name: "help", summary: "Show help for a command", run: c.help},
	}
}

func (c *application) execute(args []string) int {
	return c.dispatch(args, c.commands())
}

func (c *application) dispatch(args []string, commands []command) int {
	path := ""
	runner := c.root
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		var found *command
		for i := range commands {
			if commands[i].name == args[0] {
				found = &commands[i]
				break
			}
		}
		if found == nil {
			return c.finish(usageError(fmt.Sprintf("unknown command %q", args[0])), path)
		}
		path = strings.TrimSpace(path + " " + found.name)
		args = args[1:]
		if len(found.children) == 0 {
			runner = found.run
			break
		}
		commands = found.children
		runner = func(args []string) error {
			fs := c.flags(path)
			fs.Usage = func() { c.groupUsage(path, commands) }
			if err := c.parse(fs, args); err != nil {
				return err
			}
			return usageError("missing subcommand")
		}
	}
	return c.finish(runner(args), path)
}

func (c *application) finish(err error, path string) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintf(c.stderr, "opsgate: %s\n", err)
	var usage usageError
	if errors.As(err, &usage) {
		hint := "opsgate"
		if path != "" {
			hint += " " + path
		}
		fmt.Fprintf(c.stderr, "run '%s -h' for help\n", hint)
		return 2
	}
	return 1
}

func (c *application) flags(path string) *flag.FlagSet {
	fs := flag.NewFlagSet(path, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		fmt.Fprintf(c.stdout, "Usage: opsgate %s [flags]\n", path)
		fs.SetOutput(c.stdout)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}
	return fs
}

func (c *application) parse(fs *flag.FlagSet, args []string) error {
	// flag calls Usage on parse errors too; only explicit help prints it.
	usage := fs.Usage
	fs.Usage = func() {}
	err := fs.Parse(args)
	fs.Usage = usage
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage()
			return err
		}
		return usageError(err.Error())
	}
	if fs.NArg() != 0 {
		return usageError(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	return nil
}

func (c *application) groupUsage(path string, commands []command) {
	fmt.Fprintf(c.stdout, "Usage: opsgate %s <command> [flags]\n\nCommands:\n", path)
	for _, cmd := range commands {
		fmt.Fprintf(c.stdout, "  %-12s %s\n", cmd.name, cmd.summary)
	}
}

func (c *application) root(args []string) error {
	fs := c.flags("")
	cfg := fs.String("config", "", "path to opsgate.yaml")
	transport := fs.String("transport", "", "transport: stdio or http (default: http with a mode, otherwise stdio)")
	version := fs.Bool("version", false, "print version and exit")
	token := fs.Bool("gen-token", false, "print a random static token and its sha256, then exit")
	fs.Usage = func() {
		fmt.Fprintln(c.stdout, "Usage: opsgate [flags]\n       opsgate <command> [flags]\n\nCommands:")
		for _, cmd := range c.commands() {
			fmt.Fprintf(c.stdout, "  %-12s %s\n", cmd.name, cmd.summary)
		}
		fmt.Fprintln(c.stdout, "\nFlags:")
		fs.SetOutput(c.stdout)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}
	if err := c.parse(fs, args); err != nil {
		return err
	}
	if *version {
		return c.runVersion(nil)
	}
	if *token {
		return printToken(c.stdout)
	}
	if *transport != "" && *transport != "stdio" && *transport != "http" {
		return usageError(fmt.Sprintf("unknown transport %q", *transport))
	}
	return c.server(*cfg, *transport)
}

func (c *application) runVersion(args []string) error {
	if err := c.parse(c.flags("version"), args); err != nil {
		return err
	}
	_, err := fmt.Fprintln(c.stdout, buildinfo.Version())
	return err
}

func (c *application) help(args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "-help") {
		fmt.Fprintln(c.stdout, "Usage: opsgate help [command [subcommand]]")
		return nil
	}
	commands := c.commands()
	for i, name := range args {
		var found *command
		for j := range commands {
			if commands[j].name == name {
				found = &commands[j]
				break
			}
		}
		if found == nil {
			return usageError(fmt.Sprintf("unknown help topic %q", name))
		}
		if len(found.children) > 0 {
			commands = found.children
			continue
		}
		if i != len(args)-1 {
			return usageError("unexpected arguments after help topic")
		}
	}
	if len(args) == 0 {
		return c.root([]string{"-h"})
	}
	return c.helpCommand(args)
}

func (c *application) helpCommand(path []string) error {
	// Reuse the same routing and flag definitions as explicit -h.
	if code := c.execute(append(append([]string(nil), path...), "-h")); code != 0 {
		return usageError("invalid help topic")
	}
	return nil
}
