package cmdutil

import (
	"bufio"
	"fmt"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"
)

// ReadFromTerm displays a prompt and reads user input from the terminal
//
// It writes prompt to stdout and then reads user input from stdin.
//
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

func parseTag(tag string) map[string]string {
	m := make(map[string]string)
	tt := strings.Split(tag, ",")
	m["name"] = tt[0]
	for i := 1; i < len(tt); i++ {
		if tt[i] == "omitempty" {
			m["omitempty"] = "omitempty"
		}
	}
	return m
}

// StructToTabwriter takes any struct s and produces a formatted text output
// using the tabwriter tw. The fields in struct s to render must be tagged
// properly with a "text" tag, and they must be exported.
//
// This function will not flush the tabwriter's writer, so the caller is
// expected to do that after this function returns.
//
// An error will be returned if the data type passed as s was unexpected.
func StructToTabwriter(s any, tw *tabwriter.Writer) error {
	t := reflect.TypeOf(s)
	v := reflect.ValueOf(s)
	if t.Kind() == reflect.Pointer {
		t = v.Elem().Type()
		v = v.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("expected struct, got %s", t.Kind())
	}
	for i := 0; i < t.NumField(); i++ {
		if !t.Field(i).IsExported() {
			continue
		}
		s := t.Field(i).Tag.Get("text")
		if s == "" {
			continue
		}
		tag := parseTag(s)
		fmt.Fprintf(tw, "%s:\t%v\n", tag["name"], v.Field(i).Interface())
	}
	return nil
}
