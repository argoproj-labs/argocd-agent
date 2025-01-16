package cmdutil

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func ReadFromTerm(prompt string, maxtries int, valid func(string) bool) (string, error) {
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
		if valid != nil {
			if valid(val) {
				return val, nil
			} else {
				if maxtries == -1 {
					continue
				} else {
					if tries > maxtries {
						return "", fmt.Errorf("%s: invalid value", val)
					}
				}
			}
		} else {
			return val, nil
		}
	}
}
