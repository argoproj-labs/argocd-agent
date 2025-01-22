package cmdutil

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ReadFromTerm displays a prompt and reads user input from the terminal
//
// It writes prompt to stdout and then reads user input from stdin.

// If isValid is nil, the string entered by the user will be immediately
// returned. Otherwise, if isValid is non-nil, it will determine the
// validity of the user's input.
//
// maxRetries specifies how often the user is allowed to retry their input.
// If maxRetries is -1, the function will only return once the input is
// considered valid, otherwise the function will return an error after
// maxRetries has been reached.
func ReadFromTerm(prompt string, maxRetries int, isValid func(s string) (valid bool)) (string, error) {
	tries := 0
	for {
		tries += 1
		fmt.Printf("%s: ", prompt)
		reader := bufio.NewReader(os.Stdin)
		val, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		val = strings.TrimSuffix(val, "\n")
		if isValid != nil {
			if isValid(val) {
				return val, nil
			} else {
				if maxRetries == -1 {
					continue
				} else {
					if tries > maxRetries {
						return "", fmt.Errorf("%s: invalid value", val)
					}
				}
			}
		} else {
			return val, nil
		}
	}
}
