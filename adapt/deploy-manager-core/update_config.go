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
	// Keep the original EnvironmentFile byte-for-byte. Inspect the
	// required assignments only; never source or evaluate its contents.
	counts := map[string]int{"CSQTT_WEB_USER": 0, "CSQTT_WEB_PASS": 0}
	for _, line := range strings.Split(string(existingEnv), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if _, required := counts[key]; required {
			value = strings.TrimSpace(value)
			if value == "" || value == `""` || value == "''" {
				return result, errors.New("пустое обязательное поле WEB-конфигурации")
			}
			counts[key]++
		}
	}
	for _, count := range counts {
		if count != 1 {
			return result, errors.New("обязательные поля WEB-конфигурации отсутствуют или повторяются")
		}
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
