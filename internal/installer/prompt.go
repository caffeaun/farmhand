package installer

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Prompter reads user input from r (stdin) and writes prompts to w (stderr).
// In tests both can be in-memory buffers; in production they're os.Stdin /
// os.Stderr.
type Prompter struct {
	In     io.Reader
	Out    io.Writer
	Assume bool // when true, all Confirm calls auto-answer yes and all Ask calls return the default
	reader *bufio.Reader
}

// NewPrompter constructs a Prompter. If in is *os.File and is not a terminal
// (i.e. stdin came from a pipe — typical with `curl | sh`), we try to fall
// back to /dev/tty so prompts actually wait for the user. If /dev/tty is
// unavailable too, the prompter still works but every Ask returns its
// default and every Confirm returns defaultYes — same effect as Assume=true.
func NewPrompter(in io.Reader, out io.Writer, assumeYes bool) *Prompter {
	if !assumeYes {
		if reopened, ok := openTTYIfPiped(in); ok {
			in = reopened
		}
	}
	return &Prompter{In: in, Out: out, Assume: assumeYes, reader: bufio.NewReader(in)}
}

// openTTYIfPiped returns /dev/tty when `in` is an *os.File that isn't a
// terminal (e.g. stdin from a pipe). Returns the original reader otherwise.
// The boolean reports whether the swap happened.
func openTTYIfPiped(in io.Reader) (io.Reader, bool) {
	f, ok := in.(*os.File)
	if !ok {
		return in, false
	}
	fi, err := f.Stat()
	if err != nil {
		return in, false
	}
	if fi.Mode()&os.ModeCharDevice != 0 {
		// stdin is already a TTY; no swap needed.
		return in, false
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return in, false
	}
	return tty, true
}

// Ask prompts with a single-line question. If def is non-empty it's shown in
// brackets and used when the user just hits enter. With Assume=true the
// default is returned without reading from r.
func (p *Prompter) Ask(question, def string) (string, error) {
	if p.Assume {
		return def, nil
	}
	if def != "" {
		fmt.Fprintf(p.Out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(p.Out, "%s: ", question)
	}
	line, err := p.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

// Confirm shows a yes/no prompt. defaultYes determines the default (capitalised
// letter). Accepts y/yes/n/no case-insensitive.
func (p *Prompter) Confirm(question string, defaultYes bool) (bool, error) {
	if p.Assume {
		return defaultYes, nil
	}
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Fprintf(p.Out, "%s %s: ", question, suffix)
	line, err := p.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return defaultYes, nil
	}
}
