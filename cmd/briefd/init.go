package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/ismailperim/briefd/internal/scaffold"
)

func runInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: briefd init [DIR]\n\nCreate a starter knowledge repository layout (domain/, conventions/, projects/) with example documents. Existing files are kept.\n")
	}
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	created, err := scaffold.Write(dir)
	if err != nil {
		return err
	}
	for _, f := range created {
		fmt.Fprintf(stdout, "created %s\n", f)
	}
	if len(created) == 0 {
		fmt.Fprintln(stdout, "nothing to create; all template files already exist")
		return nil
	}
	fmt.Fprintf(stdout, "\nNext: replace the examples with your team's knowledge, commit, then\n  briefd serve --source %s --token <token>\n", dir)
	return nil
}
