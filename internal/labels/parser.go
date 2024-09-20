package labels

import (
	"fmt"
	"strings"
)

// StringsToMap parses key=value pairs in labels and returns a map, where the
// entries are populated from the parsed strings.
func StringsToMap(labels []string) (map[string]string, error) {
	ret := make(map[string]string)
	for _, llent := range labels {
		larr := strings.SplitN(llent, "=", 2)
		if len(larr) != 2 || larr[1] == "" {
			return nil, fmt.Errorf("invalid label '%s': has no value", llent)
		}
		ret[larr[0]] = larr[1]
	}
	return ret, nil
}
