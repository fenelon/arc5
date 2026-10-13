package telegram

import (
	"encoding/base64"
	"encoding/json"
	"reflect"

	"github.com/gotd/td/tdp"
)

// rawJSON encodes a Telegram object as JSON in Telegram's own terms: each
// object carries its schema name in "_" and its fields by their schema
// names, and absent optional fields are left out. It keeps what arc5 does
// not model yet (reactions, polls, formatting) for later use.
func rawJSON(v tdp.Object) []byte {
	out, err := json.Marshal(rawValue(reflect.ValueOf(v)))
	if err != nil {
		return nil
	}
	return out
}

func rawValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Struct && v.CanAddr() {
		v = v.Addr() // Telegram objects held by value, such as a forward header
	}
	if obj, ok := v.Interface().(tdp.Object); ok && (v.Kind() != reflect.Pointer || !v.IsNil()) {
		typ := obj.TypeInfo()
		if typ.Null {
			return nil
		}
		elem := v
		for elem.Kind() == reflect.Pointer || elem.Kind() == reflect.Interface {
			elem = elem.Elem()
		}
		fields := map[string]any{"_": typ.Name}
		for _, f := range typ.Fields {
			if f.Null {
				continue
			}
			if field := elem.FieldByName(f.Name); field.IsValid() {
				fields[f.SchemaName] = rawValue(field)
			}
		}
		return fields
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return rawValue(v.Elem())
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return base64.StdEncoding.EncodeToString(v.Bytes())
		}
		list := make([]any, v.Len())
		for i := range list {
			list[i] = rawValue(v.Index(i))
		}
		return list
	case reflect.Struct:
		// A struct that is not a Telegram object, such as bin.Fields.
		return nil
	}
	return v.Interface()
}
