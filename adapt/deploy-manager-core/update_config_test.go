package main

import (
	"bytes"
	"testing"
)

func Test253PreservedUpdateConfig(t *testing.T) {
	env := []byte("# fixture only\nCSQTT_WEB_USER=example\nCSQTT_WEB_PASS=\"fixture pass\"\nCSQTT_WEB_SECRET_PATH=/fixture\n")
	pending := []byte("{\"main_password\":\"fixture-only\"}\n")
	result, err := preparePreservedUpdateConfig(env, pending, true)
	if err != nil || !bytes.Equal(result.webEnvironment, env) || !bytes.Equal(result.deployOverrides, pending) {
		t.Fatal("existing configuration not preserved")
	}
	env[0] = '!'
	pending[0] = '!'
	if result.webEnvironment[0] != '#' || result.deployOverrides[0] != '{' {
		t.Fatal("prepared data aliases caller bytes")
	}
	env[0] = '#'
	pending[0] = '{'
	result, err = preparePreservedUpdateConfig(env, nil, true)
	if err != nil || string(result.deployOverrides) != "{\"main_password\":\"\"}\n" {
		t.Fatal("consumed overrides not reconstructed for preserved database")
	}
	for _, tc := range []struct {
		name           string
		env, overrides []byte
		db             bool
	}{
		{"no verified DB", env, nil, false},
		{"pending override no verified DB", env, pending, false},
		{"empty environment", nil, pending, true},
		{"missing password", []byte("CSQTT_WEB_USER=example\n"), pending, true},
		{"empty password", []byte("CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=\"\"\n"), pending, true},
		{"duplicate password", append(bytes.Clone(env), []byte("CSQTT_WEB_PASS=other\n")...), pending, true},
		{"invalid JSON", env, []byte("{"), true},
		{"duplicate JSON", env, []byte("{\"main_password\":\"a\",\"main_password\":\"b\"}"), true},
		{"wrong type", env, []byte("{\"main_password\":null}"), true},
		{"unknown setting", env, []byte("{\"main_password\":\"a\",\"unknown\":1}"), true},
		{"trailing JSON", env, append(bytes.Clone(pending), []byte("{}")...), true},
		{"oversize", bytes.Repeat([]byte("x"), 65537), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := preparePreservedUpdateConfig(tc.env, tc.overrides, tc.db)
			if err == nil || len(got.webEnvironment) != 0 || len(got.deployOverrides) != 0 {
				t.Fatal("unsafe update input accepted")
			}
		})
	}
}

func Test253UpdateEnvironmentSubset(t *testing.T) {
	for _, tc := range []struct{ name, env, user, pass string }{
		{"legacy", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=fixture\n", "example", "fixture"},
		{"upstream quoted", "CSQTT_WEB_USER=\"example\"\nCSQTT_WEB_PASS=\"a b#\\\"c\\\\d\"\n", "example", "a b#\"c\\d"},
		{"unicode", "CSQTT_WEB_USER=\"example\"\nCSQTT_WEB_PASS=\"тест\"\n", "example", "тест"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateUpdateEnvironment([]byte(tc.env))
			if err != nil || got["CSQTT_WEB_USER"] != tc.user || got["CSQTT_WEB_PASS"] != tc.pass {
				t.Fatal("valid single-line fixture rejected")
			}
			result, err := preparePreservedUpdateConfig([]byte(tc.env), nil, true)
			if err != nil || !bytes.Equal(result.webEnvironment, []byte(tc.env)) {
				t.Fatal("original bytes not preserved")
			}
		})
	}
	for _, tc := range []struct{ name, env string }{
		{"fake required keys inside quote", "OTHER=\"begin\nCSQTT_WEB_USER=example\nCSQTT_WEB_PASS=fixture\n\"\n"},
		{"continuation", "OTHER=abc\\\nCSQTT_WEB_USER=example\nCSQTT_WEB_PASS=fixture\n"},
		{"unterminated quote", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=\"fixture\n"},
		{"trailing data", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=\"fixture\"extra\n"},
		{"duplicate unknown", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=fixture\nOTHER=x\nOTHER=y\n"},
		{"export alias", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=fixture\nexport CSQTT_WEB_PASS=other\n"},
		{"invalid key", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=fixture\n1OTHER=x\n"},
		{"escaped end", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=\"fixture\\\"\n"},
		{"control", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=fix\x01ture\n"},
		{"bad escape", "CSQTT_WEB_USER=example\nCSQTT_WEB_PASS=\"fix\\qture\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := preparePreservedUpdateConfig([]byte(tc.env), nil, true)
			if err == nil || len(result.webEnvironment) != 0 || len(result.deployOverrides) != 0 {
				t.Fatal("ambiguous config accepted")
			}
		})
	}
}
