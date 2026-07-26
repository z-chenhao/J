package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxCommandBytes = 1024 * 1024

// RunLoop consumes one JSON command per line and waits for accepted tasks.
func RunLoop(reader io.Reader, rt *Runtime) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxCommandBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		cmd, err := decodeCommand(line)
		if err != nil {
			rt.emitFailure(command{Type: "invalid"}, codeInvalidJSON, err.Error())
			continue
		}
		rt.Handle(cmd)
	}
	rt.Wait()
	return errors.Join(scanner.Err(), rt.Err())
}

func decodeCommand(line []byte) (command, error) {
	var cmd command
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cmd); err != nil {
		return command{}, fmt.Errorf("invalid command: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return command{}, errors.New("invalid command: multiple JSON values")
		}
		return command{}, fmt.Errorf("invalid command: %w", err)
	}
	return cmd, nil
}
