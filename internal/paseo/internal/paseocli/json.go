package paseocli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func decodeAdditiveJSON(output []byte, target any) error {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return ErrEmptyOutput
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(target); err != nil {
		return classifyJSONError(err, len(trimmed))
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return classifyJSONError(err, len(trimmed))
		}
		return ErrUnexpectedJSON
	}
	return nil
}
