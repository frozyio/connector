package config

import (
	"testing"

	"github.com/mitchellh/mapstructure"
	yaml "gopkg.in/yaml.v2"
)

func defaultConfig(result interface{}) *mapstructure.DecoderConfig {
	return &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(DecodeHookRemoteValue),
		Result:     result,
	}
}

type TestLiteralData struct {
	Value RemoteValue
}

func TestLiteral(t *testing.T) {
	config := "value: literal"
	var mapsi map[string]interface{}
	err := yaml.Unmarshal([]byte(config), &mapsi)
	if err != nil {
		t.Errorf("Failed to unmarshal literal value: %v", err)
		return
	}

	var data TestLiteralData
	dec, err := mapstructure.NewDecoder(defaultConfig(&data))
	if err != nil {
		t.Errorf("Failed to create decoder: %v", err)
		return
	}
	err = dec.Decode(mapsi)
	if err != nil {
		t.Errorf("Failed to decode: %v", err)
		return
	}

	lit, ok := data.Value.origin.(*LiteralStringOrigin)
	if !ok {
		t.Error("Unmarshaled literal origin is not a LiteralStringOrigin")
		return
	}

	if lit.LiteralValue != "literal" {
		t.Error("Unmarshaled literal value doesn't match literal string")
	}
}

func TestLiteralHardWay(t *testing.T) {
	config := `
origin: literal
value:  asdfasdf
`
	var mapsi map[string]interface{}
	err := yaml.Unmarshal([]byte(config), &mapsi)
	if err != nil {
		t.Errorf("Failed to unmarshal value: %v", err)
		return
	}

	var rv RemoteValue
	dec, err := mapstructure.NewDecoder(defaultConfig(&rv))
	if err != nil {
		t.Errorf("Failed to create decoder: %v", err)
		return
	}
	err = dec.Decode(mapsi)
	if err != nil {
		t.Errorf("Failed to decode: %v", err)
		return
	}

	lit, ok := rv.origin.(*LiteralStringOrigin)
	if !ok {
		t.Error("Unmarshaled value origin is not a LiteralStringOrigin")
		return
	}

	if lit.LiteralValue != "asdfasdf" {
		t.Error("Unmarshaled literal value doesn't match YAML contents")
	}
}

func TestGceInstanceMetadataOrigin(t *testing.T) {
	config := `
origin: gce_instance_metadata
attribute: secret
`
	var mapsi map[string]interface{}
	err := yaml.Unmarshal([]byte(config), &mapsi)
	if err != nil {
		t.Errorf("Failed to unmarshal value: %v", err)
		return
	}

	var rv RemoteValue
	dec, err := mapstructure.NewDecoder(defaultConfig(&rv))
	if err != nil {
		t.Errorf("Failed to create decoder: %v", err)
		return
	}
	err = dec.Decode(mapsi)
	if err != nil {
		t.Errorf("Failed to decode: %v", err)
		return
	}

	gce, ok := rv.origin.(*GceInstanceMetadataOrigin)
	if !ok {
		t.Error("Unmarshaled value origin is not GceInstanceMetadataOrigin")
		return
	}

	if gce.Attribute != "secret" {
		t.Error("Unmarshaled attribute name doesn't match YAML contents")
	}
}

func TestLiteralWithBase64Decode(t *testing.T) {
	config := `
origin: literal
value: asdfasdf
transform:
  - base64_decode
`
	var mapsi map[string]interface{}
	err := yaml.Unmarshal([]byte(config), &mapsi)
	if err != nil {
		t.Errorf("Failed to unmarshal value: %v", err)
	}

	var rv RemoteValue
	dec, err := mapstructure.NewDecoder(defaultConfig(&rv))
	if err != nil {
		t.Errorf("Failed to create decoder: %v", err)
		return
	}
	err = dec.Decode(mapsi)
	if err != nil {
		t.Errorf("Failed to decode: %v", err)
		return
	}

	if len(rv.transformations) != 1 {
		t.Error("Length of transformations isn't 1")
	}

	_, ok := rv.transformations[0].(TransformBase64Decode)
	if !ok {
		t.Error("Transformation is not Base64 decode")
	}
}

func TestLiteralWithBase64DecodeHardWay(t *testing.T) {
	config := `
origin: literal
value: asdfasdf
transform:
  - type: base64_decode
`
	var mapsi map[string]interface{}
	err := yaml.Unmarshal([]byte(config), &mapsi)
	if err != nil {
		t.Errorf("Failed to unmarshal value: %v", err)
	}

	var rv RemoteValue
	dec, err := mapstructure.NewDecoder(defaultConfig(&rv))
	if err != nil {
		t.Errorf("Failed to create decoder: %v", err)
		return
	}
	err = dec.Decode(mapsi)
	if err != nil {
		t.Errorf("Failed to decode: %v", err)
		return
	}

	if len(rv.transformations) != 1 {
		t.Error("Length of transformations isn't 1")
	}

	_, ok := rv.transformations[0].(TransformBase64Decode)
	if !ok {
		t.Error("Transformation is not Base64 decode")
	}
}
