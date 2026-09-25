// Package realmoney guards the one-off tools in cmd/ that act on the real
// Angel One account (place, sell or cancel orders). Such a tool must not do
// anything just because someone ran it: the operator has to pass Flag and
// then type ConfirmWord at the prompt.
package realmoney

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Flag must be on the command line of a tool that touches real money.
const Flag = "--i-understand-real-money"

// ConfirmWord must be typed at the prompt to go ahead.
const ConfirmWord = "REAL"

// Confirm stops the program (exit 2) unless it was started with Flag and
// the operator types ConfirmWord. what says what the tool is about to do.
// It returns the command-line arguments without Flag, so a tool reads its
// own positional arguments from the result instead of os.Args.
func Confirm(what string) []string {
	args, err := confirm(os.Args[1:], os.Stdin, os.Stderr, what)
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌", err)
		os.Exit(2)
	}
	return args
}

func confirm(args []string, in io.Reader, out io.Writer, what string) ([]string, error) {
	rest := make([]string, 0, len(args))
	flagged := false
	for _, a := range args {
		if a == Flag {
			flagged = true
			continue
		}
		rest = append(rest, a)
	}
	if !flagged {
		return nil, fmt.Errorf("this tool %s on the REAL Angel One account; re-run with %s to continue", what, Flag)
	}

	fmt.Fprintf(out, "\n⚠️  This tool %s on the REAL Angel One account.\n", what)
	fmt.Fprintf(out, "   Type %s and press Enter to continue, anything else aborts: ", ConfirmWord)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("reading confirmation: %w", err)
	}
	if strings.TrimSpace(line) != ConfirmWord {
		return nil, errors.New("not confirmed — nothing was sent")
	}
	return rest, nil
}
