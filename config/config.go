package config

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/viper"
)

type Validatable interface {
	Validate() error
}

type Source struct {
	File        string
	EnvPrefix   string
	Defaults    map[string]any
	Overrides   map[string]any
	AllowUnused bool
}

func Load[C any](ctx context.Context, source Source) (C, error) {
	var result C
	if err := ctx.Err(); err != nil {
		return result, err
	}

	loader := viper.New()
	loader.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	if source.EnvPrefix != "" {
		loader.SetEnvPrefix(source.EnvPrefix)
	}
	loader.AutomaticEnv()
	for key, value := range source.Defaults {
		loader.SetDefault(key, value)
	}
	for key, value := range source.Overrides {
		loader.Set(key, value)
	}
	if source.File != "" {
		loader.SetConfigFile(source.File)
		if err := loader.ReadInConfig(); err != nil {
			return result, fmt.Errorf("read configuration file: %w", err)
		}
	}
	for _, key := range structKeys(reflect.TypeFor[C](), "") {
		if err := loader.BindEnv(key); err != nil {
			return result, fmt.Errorf("bind environment key %q: %w", key, err)
		}
	}
	var err error
	if source.AllowUnused {
		err = loader.Unmarshal(&result)
	} else {
		err = loader.UnmarshalExact(&result)
	}
	if err != nil {
		return result, fmt.Errorf("decode configuration: %w", err)
	}
	if validatable, ok := any(&result).(Validatable); ok {
		if err := validatable.Validate(); err != nil {
			return result, fmt.Errorf("validate configuration: %w", err)
		}
	} else if validatable, ok := any(result).(Validatable); ok {
		if err := validatable.Validate(); err != nil {
			return result, fmt.Errorf("validate configuration: %w", err)
		}
	}
	return result, nil
}

func structKeys(valueType reflect.Type, prefix string) []string {
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if valueType.Kind() != reflect.Struct {
		return nil
	}
	keys := make([]string, 0, valueType.NumField())
	for index := range valueType.NumField() {
		field := valueType.Field(index)
		if !field.IsExported() {
			continue
		}
		name, inline, skip := fieldName(field)
		if skip {
			continue
		}
		fieldPrefix := prefix
		if !inline {
			fieldPrefix = name
			if prefix != "" {
				fieldPrefix = prefix + "." + name
			}
		}
		nested := field.Type
		for nested.Kind() == reflect.Pointer {
			nested = nested.Elem()
		}
		if nested.Kind() == reflect.Struct && nested != reflect.TypeFor[context.Context]() {
			keys = append(keys, structKeys(nested, fieldPrefix)...)
			continue
		}
		if fieldPrefix != "" {
			keys = append(keys, fieldPrefix)
		}
	}
	return keys
}

func fieldName(field reflect.StructField) (name string, inline bool, skip bool) {
	for _, key := range []string{"mapstructure", "yaml", "json", "toml"} {
		tag := field.Tag.Get(key)
		if tag == "" {
			continue
		}
		parts := strings.Split(tag, ",")
		if parts[0] == "-" {
			return "", false, true
		}
		for _, option := range parts[1:] {
			if option == "squash" || option == "inline" {
				inline = true
			}
		}
		if parts[0] != "" {
			return parts[0], inline, false
		}
	}
	if field.Anonymous {
		return "", true, false
	}
	if field.Name == "" {
		return "", false, true
	}
	return strings.ToLower(field.Name), inline, false
}
