package main

import "flag"

// parseInterleaved parses fs while allowing flags and positional arguments
// to be mixed ("briefd search retry policy --top-k 4"), which the standard
// flag package does not do on its own. It returns the positional arguments
// in order. A literal "--" still ends flag parsing.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		// Parse stopped at the first non-flag argument: keep it and resume
		// after it.
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}
