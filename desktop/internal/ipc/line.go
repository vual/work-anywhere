package ipc

import (
	"bufio"
	"errors"
)

func ReadBoundedLine(reader *bufio.Reader, max int) ([]byte, error) {
	var result []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(result)+len(fragment) > max {
			return nil, errors.New("JSONL line exceeds maximum frame size")
		}
		result = append(result, fragment...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return result, err
	}
}
