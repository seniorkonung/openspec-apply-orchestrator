package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"unicode/utf8"
)

func validateJSONDocument(document []byte) error {
	if !utf8.Valid(document) {
		return ErrInvalidJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := validateJSONValue(decoder, ""); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidJSON
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalidJSON
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return ErrInvalidJSON
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidJSON
			}
			fieldPath := joinPath(path, key)
			if _, duplicate := seen[key]; duplicate {
				return fieldError(fieldPath, ErrDuplicateField)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder, fieldPath); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalidJSON
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder, path); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalidJSON
		}
	default:
		return ErrInvalidJSON
	}
	return nil
}

func decodeObject(raw []byte, path string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' || json.Unmarshal(raw, &object) != nil {
		return nil, fieldError(pathOrDocument(path), ErrInvalidValue)
	}
	return object, nil
}

func validateObjectFields(
	object map[string]json.RawMessage,
	path string,
	allowed []string,
	required []string,
) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedSet[field] = struct{}{}
	}
	keys := make([]string, 0, len(object))
	for field := range object {
		keys = append(keys, field)
	}
	sort.Strings(keys)
	for _, field := range keys {
		if _, ok := allowedSet[field]; !ok {
			return fieldError(joinPath(path, field), ErrUnknownField)
		}
	}
	for _, field := range required {
		if _, ok := object[field]; !ok {
			return fieldError(joinPath(path, field), ErrMissingField)
		}
	}
	return nil
}

func decodeInteger(raw []byte, path string) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, fieldError(path, ErrInvalidValue)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fieldError(path, ErrInvalidValue)
	}
	integer, err := number.Int64()
	if err != nil || int64(int(integer)) != integer {
		return 0, fieldError(path, ErrInvalidValue)
	}
	return int(integer), nil
}

func decodeString(raw []byte, path string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fieldError(path, ErrInvalidValue)
	}
	return value, nil
}

func fieldError(path string, kind error) error {
	return &FieldError{Path: pathOrDocument(path), Kind: kind}
}

func joinPath(parent, field string) string {
	if parent == "" {
		return field
	}
	return parent + "." + field
}

func pathOrDocument(path string) string {
	if path == "" {
		return "<document>"
	}
	return path
}
