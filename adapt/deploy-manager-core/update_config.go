package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// Local configuration selection derived from CSQTTR 2.5.3
// prepare_uploaded_release. No filesystem access, network calls, or
// deployment action: the caller must provide a verified snapshot.
// databaseVerified means a separate SQLite integrity and preservation
// check has passed; inventory's "present" field alone is insufficient.
type preservedUpdateConfig struct {
	webEnvironment  []byte
	deployOverrides []byte
}

func preparePreservedUpdateConfig(existingEnv, existingOverrides []byte, databaseVerified bool) (preservedUpdateConfig, error) {
	var result preservedUpdateConfig
	if len(existingEnv) == 0 || len(existingEnv) > 65536 || !utf8.Valid(existingEnv) || bytes.IndexByte(existingEnv, 0) >= 0 {
		return result, errors.New("существующая WEB-конфигурация отсутствует или некорректна")
	}
	// Keep the original EnvironmentFile byte-for-byte, but recognize
	// only the single-line format emitted by upstream 2.5.3 plus
	// simple legacy unquoted assignments. Refuse ambiguous syntax
	// rather than treat text inside quotes as another assignment.
	fields, err := validateUpdateEnvironment(existingEnv)
	if err != nil {
		return result, err
	}
	if fields["CSQTT_WEB_USER"] == "" || fields["CSQTT_WEB_PASS"] == "" {
		return result, errors.New("обязательные поля WEB-конфигурации отсутствуют или пусты")
	}
	// A verified existing database is required for a preservation-mode
	// update. This is stricter than upstream's file-existence heuristic.
	if !databaseVerified {
		return result, errors.New("сохранение существующей базы не подтверждено")
	}
	if len(existingOverrides) == 0 {
		// Rust 2.5.3 consumes this file after boot. An empty main_password
		// instructs it to retain the already stored value.
		existingOverrides = []byte("{\"main_password\":\"\"}\n")
	} else {
		if len(existingOverrides) > 65536 || !utf8.Valid(existingOverrides) {
			return result, errors.New("некорректная существующая deploy-конфигурация")
		}
		decoder := json.NewDecoder(bytes.NewReader(existingOverrides))
		start, err := decoder.Token()
		if err != nil || start != json.Delim('{') {
			return result, errors.New("некорректная существующая deploy-конфигурация")
		}
		seen := false
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil || key != "main_password" || seen {
				return result, errors.New("неподдерживаемое или повторное поле deploy-конфигурации")
			}
			seen = true
			var value any
			if err := decoder.Decode(&value); err != nil {
				return result, errors.New("некорректная существующая deploy-конфигурация")
			}
			if _, ok := value.(string); !ok {
				return result, errors.New("неверный тип main_password")
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') || !seen {
			return result, errors.New("некорректная существующая deploy-конфигурация")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return result, errors.New("лишние данные deploy-конфигурации")
		}
	}
	result.webEnvironment = bytes.Clone(existingEnv)
	result.deployOverrides = bytes.Clone(existingOverrides)
	return result, nil
}

// Deliberately not a general systemd EnvironmentFile parser. The accepted
// subset covers the upstream single-line quoted values, including its
// escaped quotes and backslashes. Unsupported multiline/continuation
// syntax fails closed. Never evaluate shell expressions or log values.
func validateUpdateEnvironment(data []byte) (map[string]string, error) {
	fields := map[string]string{}
	invalid := errors.New("неподдерживаемый или неоднозначный формат WEB-конфигурации")
	if !utf8.Valid(data) {
		return nil, invalid
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		for _, r := range raw {
			if r < 32 && r != '\t' || r == 127 || r == 0xfeff {
				return nil, invalid
			}
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, invalid
		}
		for i, r := range key {
			if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
				return nil, invalid
			}
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, invalid
		}
		value = strings.TrimSpace(value)
		if value != "" {
			switch value[0] {
			case '"':
				// Match systemd's double-quoted subset as produced by
				// CSQTTR systemdEnvironmentValue; reject trailing text.
				var decoded strings.Builder
				closed := false
				for i := 1; i < len(value); i++ {
					ch := value[i]
					if ch == '"' {
						if i != len(value)-1 {
							return nil, invalid
						}
						closed = true
						break
					}
					if ch == '\\' {
						i++
						if i == len(value) {
							return nil, invalid
						}
						ch = value[i]
						if ch != '\\' && ch != '"' && ch != '$' && ch != '`' {
							return nil, invalid
						}
					}
					decoded.WriteByte(ch)
				}
				if !closed {
					return nil, invalid
				}
				value = decoded.String()
			default:
				// Only straightforward legacy unquoted assignments.
				if strings.ContainsAny(value, "\\\"'\t ") {
					return nil, invalid
				}
			}
		}
		fields[key] = value
	}
	return fields, nil
}
