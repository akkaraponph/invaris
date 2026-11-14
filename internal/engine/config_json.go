package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// validateJSONValue rejects ambiguous JSON before encoding/json can normalize it.
func validateJSONValue(d *json.Decoder, typ reflect.Type, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("null is not permitted")
	}
	delim, composite := token.(json.Delim)
	if !composite {
		if typ.Kind() == reflect.Struct || typ.Kind() == reflect.Slice {
			return fmt.Errorf("unexpected scalar")
		}
		return nil
	}
	switch delim {
	case '{':
		if typ.Kind() != reflect.Struct {
			return fmt.Errorf("unexpected object")
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag != "" && tag != "-" {
				fields[tag] = f.Type
			}
		}
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok {
				return fmt.Errorf("invalid key")
			}
			child, ok := fields[key]
			if !ok || seen[key] {
				return fmt.Errorf("unknown or duplicate field %q", key)
			}
			seen[key] = true
			if e = validateJSONValue(d, child, depth+1); e != nil {
				return e
			}
		}
	case '[':
		if typ.Kind() != reflect.Slice {
			return fmt.Errorf("unexpected array")
		}
		for d.More() {
			if e := validateJSONValue(d, typ.Elem(), depth+1); e != nil {
				return e
			}
		}
	default:
		return fmt.Errorf("unexpected delimiter")
	}
	_, err = d.Token()
	return err
}
func checkConfigJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("config is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := validateJSONValue(d, reflect.TypeOf(Specification{}), 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("config must contain one object")
	}
	return nil
}
