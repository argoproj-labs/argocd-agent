package auth

import "encoding/json"

func ParseAuthSubject(subject string) (*AuthSubject, error) {
	var authSub AuthSubject
	err := json.Unmarshal([]byte(subject), &authSub)
	if err != nil {
		return nil, err
	}
	return &authSub, nil
}
