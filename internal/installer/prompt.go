package installer

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Prompter reads user input from r (stdin) and writes prompts to w (stderr).
// In tests both can be in-memory buffers; in production they're os.Stdin /
// os.Stderr.
type Prompter struct {
	In       io.Reader
	Out      io.Writer
	Assume   bool // when true, all Confirm calls auto-answer yes and all Ask calls return the default
	reader   *bufio.Reader
}

func NewPrompter(in io.Reader, out io.Writer, assumeYes bool) *Prompter {
	return &Prompter{In: in, Out: out, Assume: assumeYes, reader: bufio.NewReader(in)}
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
