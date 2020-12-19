package completer

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"mime"
	"net/mail"
	"os/exec"
	"strings"

	"github.com/google/shlex"
)

// A Completer is used to autocomplete text inputs based on the configured
// completion commands.
type Completer struct {
	// AddressBookCmd is the command to run for completing email addresses. This
	// command must output one completion on each line with fields separated by a
	// tab character. The first field must be the address, and the second field,
	// if present, the contact name. Only the email address field is required.
	// The name field is optional. Additional fields are ignored.
	AddressBookCmd string

	errHandler func(error)
	logger     *log.Logger
}

// A CompleteFunc accepts a string to be completed and returns a slice of
// possible completions.
type CompleteFunc func(string) ([]string, int)

// New creates a new Completer with the specified address book command.
func New(addressBookCmd string, errHandler func(error), logger *log.Logger) *Completer {
	return &Completer{
		AddressBookCmd: addressBookCmd,
		errHandler:     errHandler,
		logger:         logger,
	}
}

// ForHeader returns a CompleteFunc appropriate for the specified mail header. In
// the case of To, From, etc., the completer will get completions from the
// configured address book command. For other headers, a noop completer will be
// returned. If errors arise during completion, the errHandler will be called.
func (c *Completer) ForHeader(h string) CompleteFunc {
	if isAddressHeader(h) {
		if c.AddressBookCmd == "" {
			return nil
		}
		// wrap completeAddress in an error handler
		return func(s string) ([]string, int) {
			completions, chomp, err := c.completeAddress(s)
			if err != nil {
				c.handleErr(err)
				return []string{}, 0
			}
			return completions, chomp
		}
	}
	return nil
}

// isAddressHeader determines whether the address completer should be used for
// header h.
func isAddressHeader(h string) bool {
	switch strings.ToLower(h) {
	case "to", "from", "cc", "bcc":
		return true
	}
	return false
}

// completeAddress uses the configured address book completion command to fetch
// completions for the specified string, returning a slice of completions or an
// error.
func (c *Completer) completeAddress(s string) ([]string, int, error) {

	// Parse the string to extract the last, partial address.
	partialAddr, chomp := parseLastAddress(s)

	cmd, err := c.getAddressCmd(partialAddr)
	if err != nil {
		return nil, 0, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, fmt.Errorf("stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("cmd start: %v", err)
	}
	completions, err := readCompletions(stdout)
	if err != nil {
		return nil, 0, fmt.Errorf("read completions: %v", err)
	}

	// Wait returns an error if the exit status != 0, which some completion
	// programs will do to signal no matches. We don't want to spam the user with
	// spurious error messages, so we'll ignore any errors that arise at this
	// point.
	if err := cmd.Wait(); err != nil {
		c.logger.Printf("completion error: %v", err)
	}

	// If we chomped off part of the string, we need to tack it onto the front
	// of all the completions now
	if chomp > 0 {
		prefix := s[:chomp]
		for i, cp := range completions {
			completions[i] = prefix + cp
		}
	}
	return completions, chomp, nil
}

const (
	stateText = iota
	stateQuote
	stateQuotedPair
	stateComma
)

//
// Finds the last address in the string, and returns that trailing address and
// the index of the address in the string. The function implements a simple
// state machine that searches for commas outside of quoted phrases.
//
func parseLastAddress(s string) (string, int) {
	chomp := 0
	state := stateText

	for i := 0; i < len(s); i++ {
		thisByte := s[i]
		switch state {
		case stateText:
			if thisByte == ',' {
				chomp = i + 1
				state = stateComma
			} else if thisByte == '"' {
				state = stateQuote
			} // otherwise remain in stateText

		case stateQuote:
			if thisByte == '"' {
				state = stateText
			} else if thisByte == '\\' {
				state = stateQuotedPair
			} // otherwise remain in stateQuote

		case stateQuotedPair:
			state = stateQuote

		case stateComma:
			// In this state, we can at least chomp to this point
			chomp = i
			if thisByte == ' ' || thisByte == '\t' {
				// state remains
			} else if thisByte == '"' {
				state = stateQuote
			} else {
				state = stateText
			}
		}
	}
	return s[chomp:], chomp
}

// getAddressCmd constructs an exec.Cmd based on the configured command and
// specified query.
func (c *Completer) getAddressCmd(s string) (*exec.Cmd, error) {
	if strings.TrimSpace(c.AddressBookCmd) == "" {
		return nil, fmt.Errorf("no command configured")
	}
	queryCmd := strings.Replace(c.AddressBookCmd, "%s", s, -1)
	parts, err := shlex.Split(queryCmd)
	if err != nil {
		return nil, fmt.Errorf("could not lex command")
	}
	if len(parts) < 1 {
		return nil, fmt.Errorf("empty command")
	}
	if len(parts) > 1 {
		return exec.Command(parts[0], parts[1:]...), nil
	}
	return exec.Command(parts[0]), nil
}

// readCompletions reads a slice of completions from r line by line. Each line
// must consist of tab-delimited fields. Only the first field (the email
// address field) is required, the second field (the contact name) is optional,
// and subsequent fields are ignored.
func readCompletions(r io.Reader) ([]string, error) {
	buf := bufio.NewReader(r)
	completions := []string{}
	for {
		line, err := buf.ReadString('\n')
		if err == io.EOF {
			return completions, nil
		} else if err != nil {
			return nil, err
		}
		parts := strings.SplitN(line, "\t", 3)
		addr, err := mail.ParseAddress(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, err
		}
		if len(parts) > 1 {
			addr.Name = strings.TrimSpace(parts[1])
		}
		decoded, err := decodeMIME(addr.String())
		if err != nil {
			return nil, fmt.Errorf("could not decode MIME string: %w", err)
		}
		completions = append(completions, decoded)
	}
}

func decodeMIME(s string) (string, error) {
	var d mime.WordDecoder
	return d.DecodeHeader(s)
}

func (c *Completer) handleErr(err error) {
	if c.errHandler != nil {
		c.errHandler(err)
	}
}
