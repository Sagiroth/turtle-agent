package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/pjerra/turtle-agent/internal/capture"
	"github.com/pjerra/turtle-agent/internal/config"
	"github.com/pjerra/turtle-agent/internal/opcode"
)

// scrubCmd fails when a capture contains an account name (upper case, as on the wire)
// or a password from the settings file, in any form capture.Variants lists. Run it on
// every fixture before committing. A secret too short to prove anything is an error.
func scrubCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrub-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", config.DefaultPath(), "settings file whose accounts and passwords must not appear")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: turtle-agent scrub-check [--config FILE] <capture>...")
		return exitUsage
	}
	st, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return exitUsage
	}
	// Three needles per account, in this order: Finding.Needle%3 == 0 is the account name.
	names := make([]string, 0, len(st.Accounts))
	for name := range st.Accounts {
		names = append(names, name)
	}
	sort.Strings(names)
	var needles [][]byte
	for _, name := range names {
		pw := st.Accounts[name]
		needles = append(needles, []byte(strings.ToUpper(name)), []byte(pw), []byte(strings.ToUpper(pw)))
	}
	if len(needles) == 0 {
		fmt.Fprintln(stderr, "scrub-check: the settings file has no accounts, so there is nothing to check against")
		return exitUsage
	}
	code := exitOK
	for _, path := range fs.Args() {
		recs, err := capture.ReadFile(path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		found, err := capture.Scan(recs, needles...)
		if err != nil {
			// The error names a needle index, never the secret.
			fmt.Fprintln(stderr, "scrub-check:", err)
			return exitUsage
		}
		for _, f := range found {
			what := "a password"
			if f.Needle%3 == 0 {
				what = "an account name"
			}
			fmt.Fprintf(stdout, "%s: record %d (%s) contains %s from the settings file\n", path, f.Index, opcode.Name(f.Op), what)
		}
		if len(found) > 0 {
			code = exitScrub
			continue
		}
		fmt.Fprintf(stdout, "%s: clean, %d records\n", path, len(recs))
	}
	return code
}

func statsCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: turtle-agent capture-stats <capture>")
		return exitUsage
	}
	recs, err := capture.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	st := capture.Stats(recs)
	names := make([]string, 0, len(st))
	for n := range st {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(stdout, "%-36s %d\n", n, st[n])
	}
	fmt.Fprintf(stdout, "%-36s %d\n", "TOTAL", len(recs))
	return exitOK
}
