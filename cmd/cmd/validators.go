package cmd

import "fmt"

func ValidPort(num int) error {
	if num < 0 || num > 65536 {
		return fmt.Errorf("%d: not a valid port number", num)
	}
	return nil
}
