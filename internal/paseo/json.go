package paseo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

type requiredValue[T any] struct {
	value   T
	present bool
}

func (field *requiredValue[T]) UnmarshalJSON(data []byte) error {
	field.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("обязательное поле не может быть null")
	}
	return json.Unmarshal(data, &field.value)
}

type requiredNullable[T any] struct {
	value   *T
	present bool
}

func (field *requiredNullable[T]) UnmarshalJSON(data []byte) error {
	field.present = true
	return json.Unmarshal(data, &field.value)
}

func decodeStrictJSON(output []byte, target any) error {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return ErrEmptyOutput
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
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

func classifyJSONError(err error, length int) error {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrTruncatedJSON
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) && syntaxError.Offset >= int64(length) {
		return ErrTruncatedJSON
	}
	return ErrUnexpectedJSON
}

func validOpaqueValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validIdentifierValue(value string) bool {
	return validOpaqueValue(value) && strings.IndexFunc(value, unicode.IsSpace) < 0
}
