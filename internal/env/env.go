// Copyright 2024 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package env

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// BoolWithDefault parses the contents of the environment variable referred by
// key into a boolean value and returns it. If the environment is not set, or
// is not a string that can be converted into a boolean, the default value def
// will be returned.
func BoolWithDefault(key string, def bool) bool {
	bv, err := Bool(key)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Print(err)
		}
		return def
	}
	return bv
}

// StringWithDefault gets the contents of the environment variable referred to
// by key. If the validator function is non-nil, it will be called with the
// environment's value as argument. If the validator returns an error or if
// the environment variable is not set, the default value will be returned.
// Otherwise, the verbatim value of the environment variable will be returned.
func StringWithDefault(key string, validator func(string) error, def string) string {
	ev, err := String(key, validator)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Print(err)
		}
		return def
	}
	return ev
}

// NumWithDefault parses the contents of the environment variable referred to by
// key into an integer. If the validator function is non-nil, it will be called
// with the parsed integer as argument. If the validator returns an error or if
// the environment variable was not set, the default value will be returned.
// Otherwise, the environment variable's value will be returned.
func NumWithDefault(key string, validator func(int) error, def int) int {
	ev, err := Num(key, validator)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Print(err)
		}
		return def
	}
	return ev
}

func StringSliceWithDefault(key string, validator func(string) error, def []string) []string {
	ev, err := StringSlice(key, validator)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Print(err)
		}
		return def
	}
	return ev
}

func Bool(key string) (bool, error) {
	ev, ok := os.LookupEnv(key)
	if !ok {
		return false, os.ErrNotExist //fmt.Errorf("environment '%s' not set", key)
	}
	bv, err := strconv.ParseBool(ev)
	if err != nil {
		return false, fmt.Errorf("error parsing environment '%s': %v", key, err)
	}
	return bv, nil
}

func String(key string, validator func(string) error) (string, error) {
	ev, ok := os.LookupEnv(key)
	if !ok {
		return "", os.ErrNotExist //fmt.Errorf("environment '%s' not set", key)
	}
	if validator != nil {
		if err := validator(ev); err != nil {
			return "", fmt.Errorf("error validating environment '%s': %v", key, err)
		}
	}
	return ev, nil
}

func Num(key string, validator func(num int) error) (int, error) {
	ev, ok := os.LookupEnv(key)
	if !ok {
		return 0, os.ErrNotExist //fmt.Errorf("environment '%s' not set", key)
	}
	nv, err := strconv.ParseInt(ev, 0, 32)
	if err != nil {
		return 0, err
	}
	if validator != nil {
		if err := validator(int(nv)); err != nil {
			return 0, fmt.Errorf("error validating environment '%s': %v", key, err)
		}
	}
	return int(nv), nil
}

func StringSlice(key string, validator func(string) error) ([]string, error) {
	ev, ok := os.LookupEnv(key)
	if !ok {
		return []string{}, os.ErrNotExist //fmt.Errorf("environment '%s' not set", key)
	}
	ret := []string{}
	for _, s := range strings.Split(ev, ",") {
		s = strings.TrimSpace(s)
		if validator != nil {
			if err := validator(s); err != nil {
				return []string{}, fmt.Errorf("error validating environment '%s': %v", key, err)
			}
		}
		ret = append(ret, s)
	}
	return ret, nil
}

func Duration(key string, validator func(time.Duration) error) (time.Duration, error) {
	ev, ok := os.LookupEnv(key)
	if !ok {
		return 0, os.ErrNotExist
	}

	d, err := time.ParseDuration(ev)
	if err != nil {
		return 0, fmt.Errorf("error parsing duration '%s': %v", key, err)
	}

	if validator != nil {
		if err := validator(d); err != nil {
			return 0, fmt.Errorf("error validating environment '%s': %v", key, err)
		}
	}
	return d, nil
}

// DurationWithDefault gets the contents of the environment variable referred to
// by key. If the validator function is non-nil, it will be called with the
// environment's value as argument. If the validator returns an error or if
// the environment variable is not set, the default value will be returned.
// Otherwise, the verbatim value of the environment variable will be returned.
func DurationWithDefault(key string, validator func(time.Duration) error, def time.Duration) time.Duration {
	d, err := Duration(key, validator)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Print(err)
		}
		return def
	}
	return d
}
