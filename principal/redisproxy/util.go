package redisproxy

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"

	"github.com/sirupsen/logrus"
)

// startArgoCDRedisEndpointReader reads redis commands from socket from Argo CD, parses them, and then writes them to channel
func startArgoCDRedisEndpointReader(logCtx *logrus.Entry, fromArgoCDRead *bufio.Reader) chan parsedRedisCommand {

	receiverChan := make(chan parsedRedisCommand)

	go func() {
		defer close(receiverChan)
		for {
			// Read from Argo CD server input
			parsedReceived, rawReceived, err := readRedisArray(fromArgoCDRead)
			if err != nil {
				logCtx.WithError(err).Debug("unable to read from Argo CD redis endpoint input, usually due to closed connection")
				// We don't need to send the 'err' back on the channel, as the close() call above will handle informing the channel consumer.
				return
			}
			// Write to channel
			receiverChan <- parsedRedisCommand{parsedReceived: parsedReceived, rawReceived: rawReceived}

		}
	}()

	return receiverChan

}

var ErrInvalidRequest = errors.New("invalid request")

// readRedisArray reads a redis array from reader
//
// Returns:
// - parsed redis fields from input (removing type information)
// - raw redis input (includes type information)
// - error
//
// This function originally from https://github.com/alicebob/miniredis (https://github.com/alicebob/miniredis/commit/e79bddcbd62ee0f3669cbcd43d75bab2a2d726ad), under MIT license
func readRedisArray(rd *bufio.Reader) ([]string, []string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return nil, nil, fmt.Errorf("readArray ReadString error: %v", err)
	}
	if len(line) < 3 {
		return nil, nil, fmt.Errorf("readArray unexpected 'line' length")
	}

	switch line[0] {
	default:
		return nil, nil, fmt.Errorf("readArray unexpected line '%s'", line)
	case '*':
		var receivedRes []string

		l, err := strconv.Atoi(line[1 : len(line)-2])
		if err != nil {
			return nil, nil, err
		}
		receivedRes = append(receivedRes, fmt.Sprintf("*%d", l))

		// l can be -1
		var fields []string
		for ; l > 0; l-- {
			s, received, err := readString(rd)
			if err != nil {
				return nil, nil, err
			}
			fields = append(fields, s)
			receivedRes = append(receivedRes, received...)
		}

		return fields, receivedRes, nil
	}
}

// readString reads redis simple or bulk string
// Returns:
// - parsed redis fields from input (removing type information)
// - raw redis input (includes type information)
// - error
// This function originally from https://github.com/alicebob/miniredis (https://github.com/alicebob/miniredis/commit/e79bddcbd62ee0f3669cbcd43d75bab2a2d726ad), under MIT license
func readString(rd *bufio.Reader) (string, []string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	if len(line) < 3 {
		return "", nil, ErrInvalidRequest
	}

	switch line[0] {
	default:
		return "", nil, ErrInvalidRequest
	case '+', '-', ':':
		// +: simple string
		// -: errors
		// :: integer
		// Simple line based replies.
		return string(line[1 : len(line)-2]), []string{line}, nil // Consider adding a trim on this line
	case '$':
		// bulk strings are: `$5\r\nhello\r\n`
		length, err := strconv.Atoi(line[1 : len(line)-2])
		if err != nil {
			return "", nil, err
		}
		if length < 0 {
			// -1 is a nil response
			return "", []string{fmt.Sprintf("$%d", length)}, nil
		}
		var (
			buf = make([]byte, length+2)
			pos = 0
		)
		for pos < length+2 {
			n, err := rd.Read(buf[pos:])
			if err != nil {
				return "", nil, err
			}
			pos += n
		}
		resStr := string(buf[:length])

		return resStr, []string{fmt.Sprintf("$%d", length), resStr}, nil
	}
}
