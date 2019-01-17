package config

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestLiteralSecret(t *testing.T) {
	var secret Secret
	config := "literal"
	err := yaml.Unmarshal([]byte(config), &secret)
	if err != nil {
		t.Errorf("Failed to unmarshal literal secret: %v", err)
		return
	}

	lit, ok := secret.origin.(*LiteralOrigin)
	if !ok {
		t.Error("Unmarshaled literal is not a LiteralOrigin")
		return
	}

	if !bytes.Equal(lit.LiteralValue, []byte(config)) {
		t.Error("Unmarshaled literal value doesn't match literal string")
	}
}

func TestLiteralSecretHardWay(t *testing.T) {
	var secret Secret
	config := `
origin: literal
value:  asdfasdf
`
	err := yaml.Unmarshal([]byte(config), &secret)
	if err != nil {
		t.Errorf("Failed to unmarshal secret: %v", err)
		return
	}

	lit, ok := secret.origin.(*LiteralOrigin)
	if !ok {
		t.Error("Unmarshaled value is not a LiteralOrigin")
		return
	}

	if !bytes.Equal(lit.LiteralValue, []byte("asdfasdf")) {
		t.Error("Unmarshaled literal value doesn't match YAML contents")
	}
}

func TestGceInstanceMetadataOrigin(t *testing.T) {
	var secret Secret
	config := `
origin: gce_instance_metadata
attribute: secret
`
	err := yaml.Unmarshal([]byte(config), &secret)
	if err != nil {
		t.Errorf("Failed to unmarshal secret: %v", err)
		return
	}

	gce, ok := secret.origin.(*GceInstanceMetadataOrigin)
	if !ok {
		t.Error("Unmarshaled value is not GceInstanceMetadataOrigin")
		return
	}

	if gce.Attribute != "secret" {
		t.Error("Unmarshaled attribute name doesn't match YAML contents")
	}
}

func TestLiteralWithBase64Decode(t *testing.T) {
	var secret Secret
	config := `
origin: literal
value: asdfasdf
transform:
  - base64_decode
`
	err := yaml.Unmarshal([]byte(config), &secret)
	if err != nil {
		t.Errorf("Failed to unmarshal secret: %v", err)
	}

	if len(secret.transformations) != 1 {
		t.Error("Length of transformations isn't 1")
	}

	_, ok := secret.transformations[0].(TransformBase64Decode)
	if !ok {
		t.Error("Transformation is not Base64 decode")
	}
}
