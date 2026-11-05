package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/gkdis6/opsgate/internal/config"
	"golang.org/x/term"
)

// runInit asks for what cannot be defaulted and writes a mode-based
// configuration. Flags answer questions ahead; without a terminal every
// required answer must come from a flag.
func (c *application) runInit(args []string) error {
	fs := c.flags("init")
	out := fs.String("o", "opsgate.yaml", "file to write, or - for stdout")
	force := fs.Bool("force", false, "overwrite an existing file")
	yes := fs.Bool("yes", false, "use defaults for every question not answered by a flag")
	var o config.InitOptions
	fs.StringVar(&o.PublicURL, "public-url", "", "public HTTPS origin, e.g. https://mcp.example.com")
	fs.StringVar(&o.Login, "login", "", "sign-in: passkey, google or github")
	fs.StringVar(&o.User, "user", "", "first user: passkey name, Google email or GitHub login")
	fs.StringVar(&o.ClientID, "client-id", "", "Google or GitHub OAuth client ID")
	fs.StringVar(&o.ClientSecretFile, "client-secret-file", "", "file holding the OAuth client secret")
	fs.BoolVar(&o.Write, "write", false, "also grant ops:write (write tools still need targets)")
	namespaces := fs.String("namespaces", "", "comma-separated namespace globs to allow (* for all)")
	deny := fs.String("deny-namespaces", "", "comma-separated namespace globs to deny")
	fs.BoolVar(&o.PodLogs, "pod-logs", true, "allow reading pod logs")
	fs.StringVar(&o.NoteVault, "note-vault", "", "absolute path of a Markdown/Obsidian vault; empty leaves notes off")
	if err := c.parse(fs, args); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	input, terminal := c.stdin.(*os.File)
	ask := &asker{in: bufio.NewReader(c.stdin), out: c.stderr, interactive: !*yes && terminal && term.IsTerminal(int(input.Fd()))}
	ask.text(set["public-url"], &o.PublicURL, "Public HTTPS URL clients connect to", "")
	ask.choice(set["login"], &o.Login, "Sign-in: passkey, google or github", config.LoginPasskey, config.LoginGoogle, config.LoginGitHub)
	switch o.Login {
	case config.LoginGoogle:
		ask.text(set["user"], &o.User, "Your Google email", "")
	case config.LoginGitHub:
		ask.text(set["user"], &o.User, "Your GitHub login", "")
	default:
		ask.text(set["user"], &o.User, "Your user name for passkey sign-in", "")
	}
	if o.Login == config.LoginGoogle || o.Login == config.LoginGitHub {
		ask.text(set["client-id"], &o.ClientID, "OAuth client ID", "")
		ask.text(set["client-secret-file"], &o.ClientSecretFile, "File holding the OAuth client secret", "/etc/opsgate/client-secret")
	}
	ask.yesNo(set["write"], &o.Write, "Allow this user to approve write actions (ops:write)?", false)
	ask.list(set["namespaces"], namespaces, "Namespaces to allow (comma-separated globs, * for all)", "*")
	ask.list(set["deny-namespaces"], deny, "Namespaces to deny", "kube-system,kube-public,kube-node-lease,opsgate")
	ask.yesNo(set["pod-logs"], &o.PodLogs, "Allow reading pod logs?", true)
	ask.optional(set["note-vault"], &o.NoteVault, "Notes vault path (empty: off)")
	if ask.err != nil {
		return ask.err
	}
	o.Namespaces, o.DenyNamespaces = split(*namespaces), split(*deny)

	data, err := config.Generate(o)
	if err != nil {
		return usageError(err.Error())
	}
	if *out == "-" {
		_, err = c.stdout.Write(data)
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if *force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(*out, flags, 0600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists; pass -force to overwrite", *out)
	} else if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(c.stderr, "wrote %s\n", *out)
	fmt.Fprintf(c.stderr, "Next: put it in the opsgate ConfigMap of deploy/kubernetes/opsgate-deployment.yaml and apply.\n")
	if o.Login == config.LoginPasskey {
		fmt.Fprintf(c.stderr, "On first start the log prints a one-time link to create the passkey for %s.\n", o.Subject())
	}
	return nil
}

type asker struct {
	in          *bufio.Reader
	out         io.Writer
	interactive bool
	err         error
}

// text keeps a flag's answer, asks on a terminal, and otherwise takes the
// default; a question without a default then needs its flag.
func (a *asker) text(answered bool, v *string, question, def string) {
	a.ask(answered, v, question, def, def == "")
}

// optional is text for answers that may stay empty.
func (a *asker) optional(answered bool, v *string, question string) {
	a.ask(answered, v, question, "", false)
}

func (a *asker) ask(answered bool, v *string, question, def string, required bool) {
	if answered || a.err != nil {
		return
	}
	if !a.interactive {
		if required {
			a.err = usageError(fmt.Sprintf("no terminal to ask %q; pass it as a flag", question))
		}
		*v = def
		return
	}
	if def != "" {
		fmt.Fprintf(a.out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(a.out, "%s: ", question)
	}
	line, err := a.in.ReadString('\n')
	if err == io.EOF && line == "" {
		// Input ended: answer the rest like -yes.
		fmt.Fprintln(a.out)
		a.interactive = false
		a.ask(false, v, question, def, required)
		return
	} else if err != nil && err != io.EOF {
		a.err = fmt.Errorf("read answer: %w", err)
		return
	}
	if line = strings.TrimSpace(line); line != "" {
		*v = line
	} else {
		*v = def
	}
}

// choice asks again on a terminal until the answer is one of the options;
// the first option is the default.
func (a *asker) choice(answered bool, v *string, question string, options ...string) {
	for a.err == nil {
		a.text(answered, v, question, options[0])
		if answered || !a.interactive || slices.Contains(options, *v) {
			return
		}
		fmt.Fprintf(a.out, "Please answer %s.\n", strings.Join(options, ", "))
	}
}

func (a *asker) list(answered bool, v *string, question, def string) {
	a.text(answered, v, question, def)
}

func (a *asker) yesNo(answered bool, v *bool, question string, def bool) {
	if answered || a.err != nil {
		return
	}
	answer := ""
	a.text(false, &answer, question+" (y/n)", map[bool]string{true: "y", false: "n"}[def])
	*v = strings.HasPrefix(strings.ToLower(answer), "y")
}

func split(s string) []string {
	var items []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// runConfigShow prints the effective configuration, mode defaults included.
func (c *application) runConfigShow(args []string) error {
	fs := c.flags("config show")
	path := fs.String("config", "", "path to opsgate.yaml")
	if err := c.parse(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	data, err := cfg.Effective()
	if err != nil {
		return err
	}
	_, err = c.stdout.Write(data)
	return err
}
