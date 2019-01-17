package config

import (
	"testing"

	"gopkg.in/yaml.v2"
)

func TestLiteral(t *testing.T) {
	var rv RemoteValue
	config := "literal"
	err := yaml.Unmarshal([]byte(config), &rv)
	if err != nil {
		t.Errorf("Failed to unmarshal literal value: %v", err)
		return
	}

	lit, ok := rv.origin.(*LiteralStringOrigin)
	if !ok {
		t.Error("Unmarshaled literal origin is not a LiteralStringOrigin")
		return
	}

	if lit.LiteralValue != config {
		t.Error("Unmarshaled literal value doesn't match literal string")
	}
}

func TestLiteralHardWay(t *testing.T) {
	var rv RemoteValue
	config := `
origin: literal
value:  asdfasdf
`
	err := yaml.Unmarshal([]byte(config), &rv)
	if err != nil {
		t.Errorf("Failed to unmarshal value: %v", err)
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
	var rv RemoteValue
	config := `
origin: gce_instance_metadata
attribute: secret
`
	err := yaml.Unmarshal([]byte(config), &rv)
	if err != nil {
		t.Errorf("Failed to unmarshal value: %v", err)
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
	var rv RemoteValue
	config := `
origin: literal
value: asdfasdf
transform:
  - base64_decode
`
	err := yaml.Unmarshal([]byte(config), &rv)
	if err != nil {
		t.Errorf("Failed to unmarshal value: %v", err)
	}

	if len(rv.transformations) != 1 {
		t.Error("Length of transformations isn't 1")
	}

	_, ok := rv.transformations[0].(TransformBase64Decode)
	if !ok {
		t.Error("Transformation is not Base64 decode")
	}
}
