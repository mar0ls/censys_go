package cli

import (
	"flag"
	"strings"
)

// splitCommand finds the subcommand name in args and returns it along with the
// remaining arguments in their original order.
//
// It walks the tokens the way flag.Parse would, so a flag's value is never
// mistaken for the command: in `censys --format table host 1.2.3.4`, "table" is
// consumed as the value of --format and "host" is the command. known describes
// the global flags, which are the only ones that may appear before the command.
func splitCommand(known *flag.FlagSet, args []string) (name string, rest []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			// Everything after "--" is positional, so the first token there is
			// the command if we have not found one yet.
			if i+1 < len(args) {
				return args[i+1], append(append([]string{}, args[:i+1]...), args[i+2:]...)
			}
			return "", args
		}

		if !isFlag(arg) {
			return arg, append(append([]string{}, args[:i]...), args[i+1:]...)
		}

		if consumesNext(known, arg) {
			i++
		}
	}
	return "", args
}

// permute reorders args so that every flag precedes every positional argument.
// The standard flag package stops parsing at the first positional, which would
// make `censys host 1.2.3.4 --format csv` silently treat "--format" as a target.
//
// Relative order is preserved within each group, and everything after a literal
// "--" is left untouched as positional.
func permute(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}

		if !isFlag(arg) {
			positional = append(positional, arg)
			continue
		}

		flags = append(flags, arg)
		if consumesNext(fs, arg) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	if len(positional) == 0 {
		return flags
	}
	// The separator stops flag.Parse from reinterpreting a positional that
	// happens to start with a dash.
	return append(append(flags, "--"), positional...)
}

// isFlag reports whether a token looks like a flag rather than an argument.
func isFlag(arg string) bool {
	return len(arg) > 1 && arg[0] == '-'
}

// consumesNext reports whether a flag token takes the following token as its
// value. Boolean flags never do, and neither does a token that already carries
// its value as "-name=value". An unrecognised flag is assumed not to, leaving
// flag.Parse to report it.
func consumesNext(fs *flag.FlagSet, arg string) bool {
	name := strings.TrimLeft(arg, "-")
	if name == "" || strings.Contains(name, "=") {
		return false
	}

	found := fs.Lookup(name)
	if found == nil {
		return false
	}
	boolFlag, ok := found.Value.(interface{ IsBoolFlag() bool })
	return !ok || !boolFlag.IsBoolFlag()
}
