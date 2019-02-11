package config

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"reflect"

	"cloud.google.com/go/compute/metadata"
	kms "cloud.google.com/go/kms/apiv1"
	"github.com/mitchellh/mapstructure"
	kmspb "google.golang.org/genproto/googleapis/cloud/kms/v1"
	yaml "gopkg.in/yaml.v2"
)

// RemoteValue is configuration value that can be obtained from remote source
// and optionally be transformed do decrypt/decode it.
type RemoteValue struct {
	origin          Origin
	transformations []Transformation
	resolvedValue   []byte
	isResolved      bool
}

// LiteralString constructs the most trivial kind of remote value - a literal
// string.
func LiteralString(value string) RemoteValue {
	return LiteralStringOrigin{LiteralValue: value}.ToRemoteValue()
}

// LiteralBytes constructs the most trivial kind of remote value - a literal
// byte array.
func LiteralBytes(value []byte) RemoteValue {
	return LiteralBytesOrigin{LiteralValue: value}.ToRemoteValue()
}

// Origin is where remote values come from. (config file, GCE Instance
// Metadata, etc...)
type Origin interface {
	// Obtain retreives remote value from the store.
	Obtain() ([]byte, error)
	// Marshals given origin to YAML in a shortest form possible. Result may be
	// scalar (a literal string, for instance)
	MarshalYAMLShort() (interface{}, error)

	// Marshals given origin to full form. Result must be a dictionary
	MarshalYAMLFull() (yaml.MapSlice, error)
}

// Transformation modifies remote value obtained from the Origin (decodes/decrypts)
//
// Important corner case is that transformations are allowed to have attributes
// that are RemoteValue themselves.
type Transformation interface {
	// Transform performs the transformation of remote value.
	Transform([]byte) ([]byte, error)

	yaml.Marshaler
}

// Value gets an actual value. Resolves it if neccesary.
func (x *RemoteValue) Value() ([]byte, error) {
	if !x.isResolved {
		err := x.Resolve()
		if err != nil {
			return nil, err
		}
	}
	return x.resolvedValue, nil
}

// IsResolved returns true if actual value is ready. If this function returns
// true, Value() method is guaranteed to succeed.
func (x RemoteValue) IsResolved() bool {
	return x.isResolved
}

// Resolve ensures that actual value is ready. If this function returns nil
// (which means success) then IsResolved is guaranteed to return true
func (x *RemoteValue) Resolve() error {
	if x.isResolved {
		return nil
	}

	if x.origin == nil {
		x.resolvedValue = nil
		x.isResolved = true
		return nil
	}

	raw, err := x.origin.Obtain()
	if err != nil {
		return err
	}

	for _, t := range x.transformations {
		raw, err = t.Transform(raw)
		if err != nil {
			return err
		}
	}

	x.resolvedValue = raw
	x.isResolved = true
	return nil
}

// MarshalYAML implements yaml.Marshaler for RemoteValue
func (x RemoteValue) MarshalYAML() (interface{}, error) {
	if x.origin == nil {
		return nil, nil
	}

	if len(x.transformations) == 0 {
		return x.origin.MarshalYAMLShort()
	}

	slice, err := x.origin.MarshalYAMLFull()
	if err != nil {
		return nil, err
	}
	slice = append(slice, yaml.MapItem{
		Key:   "transform",
		Value: x.transformations,
	})
	return slice, nil
}

// LiteralBytesOrigin is an Origin that obtains value by returning the same
// simple literal of type []byte
type LiteralBytesOrigin struct {
	LiteralValue []byte `mapstructure:"value"`
}

// Obtain is an implementation of Origin.Obtain for LiteralBytesOrigin
func (x LiteralBytesOrigin) Obtain() ([]byte, error) { return x.LiteralValue, nil }

// MarshalYAMLShort is an implementation of Origin.MarshalYAMLShort for LiteralBytesOrigin
func (x LiteralBytesOrigin) MarshalYAMLShort() (interface{}, error) {
	return x.LiteralValue, nil
}

// MarshalYAMLFull is an implementation of Origin.MarshalYAMLFull for LiteralOrigin
func (x LiteralBytesOrigin) MarshalYAMLFull() (yaml.MapSlice, error) {
	return yaml.MapSlice{
		yaml.MapItem{
			Key:   "origin",
			Value: "literal_bytes",
		},
		yaml.MapItem{
			Key:   "value",
			Value: x.LiteralValue,
		},
	}, nil
}

// ToRemoteValue converts LiteralBytesOrigin to RemoteValue
func (x LiteralBytesOrigin) ToRemoteValue() RemoteValue { return RemoteValue{origin: &x} }

// LiteralStringOrigin is an Origin that obtains value by returning the same
// simple literal of type string
type LiteralStringOrigin struct {
	LiteralValue string `mapstructure:"value"`
}

// Obtain is an implementation of Origin.Obtain for LiteralStringOrigin
func (x LiteralStringOrigin) Obtain() ([]byte, error) { return []byte(x.LiteralValue), nil }

// MarshalYAMLShort is an implementation of Origin.MarshalYAMLShort
func (x LiteralStringOrigin) MarshalYAMLShort() (interface{}, error) {
	return x.LiteralValue, nil
}

// MarshalYAMLFull is an implementation of Origin.MarhsalYAMLFull
func (x LiteralStringOrigin) MarshalYAMLFull() (yaml.MapSlice, error) {
	return yaml.MapSlice{
		yaml.MapItem{
			Key:   "origin",
			Value: "literal",
		},
		yaml.MapItem{
			Key:   "value",
			Value: x.LiteralValue,
		},
	}, nil
}

// ToRemoteValue converts LiteralStringOrigin to RemoteValue
func (x LiteralStringOrigin) ToRemoteValue() RemoteValue { return RemoteValue{origin: &x} }

// GceInstanceMetadataOrigin is an Origin that obtains value by  querying Google
// Compute Instance metadata.
type GceInstanceMetadataOrigin struct {
	Attribute string `mapstructure:"attribute"`
}

// Obtain is an implementation of Origin.Obtain for GceInstanceMetadataOrigin
func (x GceInstanceMetadataOrigin) Obtain() ([]byte, error) {
	c := metadata.NewClient(&http.Client{Transport: userAgentTransport{
		userAgent: "frozy-connector",
		base:      http.DefaultTransport,
	}})

	value, err := c.InstanceAttributeValue(x.Attribute)
	if err != nil {
		return nil, err
	}

	return []byte(value), nil
}

// MarshalYAMLShort is an implementation of Origin.MarshalYAMLShort for GceInstanceMetadataOrigin
func (x GceInstanceMetadataOrigin) MarshalYAMLShort() (interface{}, error) {
	return x.MarshalYAMLFull()
}

// MarshalYAMLFull is an implementation of Origin.MarshalYAMLFull for GceInstanceMetadataOrigin
func (x GceInstanceMetadataOrigin) MarshalYAMLFull() (yaml.MapSlice, error) {
	return yaml.MapSlice{
		yaml.MapItem{
			Key:   "origin",
			Value: "gce_instance_metadata",
		},
		yaml.MapItem{
			Key:   "attribute",
			Value: x.Attribute,
		},
	}, nil
}

// userAgentTransport sets the User-Agent header before calling base.
type userAgentTransport struct {
	userAgent string
	base      http.RoundTripper
}

// RoundTrip implements the http.RoundTripper interface.
func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", t.userAgent)
	return t.base.RoundTrip(req)
}

type genericRemoteValue struct {
	Origin    string           `mapstructure:"origin"`
	Transform []Transformation `mapstructure:"transform"`
}

// DecodeHookRemoteValue is mapstructure hook that enables decoding of RemoteValue
// structure members
func DecodeHookRemoteValue(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
	if to != reflect.TypeOf(RemoteValue{}) {
		return data, nil
	}

	if from == reflect.TypeOf("") {
		literal := data.(string)
		return LiteralString(literal), nil
	}

	x := RemoteValue{}
	var gen genericRemoteValue
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(DecodeHookRemoteValue, decodeHookTransformation),
		Result:     &gen,
	})
	if err != nil {
		return nil, fmt.Errorf("Cannot create decoder: %v", err)
	}
	err = dec.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("Cannot decode generic RemoteValue: %v", err)
	}

	x.transformations = gen.Transform

	switch gen.Origin {
	case "literal_bytes":
		var lit LiteralBytesOrigin
		err = mapstructure.Decode(data, &lit)
		if err != nil {
			return nil, fmt.Errorf("Cannot decode literal bytes: %v", err)
		}
		x.origin = &lit
	case "literal":
		var lit LiteralStringOrigin
		err = mapstructure.Decode(data, &lit)
		if err != nil {
			return nil, fmt.Errorf("Cannot decode literal string: %v", err)
		}
		x.origin = &lit
	case "gce_instance_metadata":
		var gceInstanceMetadataOrigin GceInstanceMetadataOrigin
		err = mapstructure.Decode(data, &gceInstanceMetadataOrigin)
		if err != nil {
			return nil, fmt.Errorf("Cannot decode GCE Instance Metadata Origin: %v", err)
		}
		x.origin = &gceInstanceMetadataOrigin
	default:
		return nil, fmt.Errorf("Unknown origin for remote value: %q", gen.Origin)
	}

	return x, nil
}

type genericTransform struct {
	Type string `mapstructure:"type"`
}

func decodeHookTransformation(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
	if to != reflect.TypeOf((*Transformation)(nil)).Elem() {
		return data, nil
	}

	if from == reflect.TypeOf("") {
		simple := data.(string)
		switch simple {
		case "base64_decode":
			return TransformBase64Decode{}, nil
		default:
			return nil, fmt.Errorf("Unknown simple transform: %q", simple)
		}
	}

	var gen genericTransform
	err := mapstructure.Decode(data, &gen)
	if err != nil {
		return nil, fmt.Errorf("Cannot decode generic transform: %v", err)
	}

	decode := func(input interface{}, result interface{}) error {
		dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(DecodeHookRemoteValue, decodeHookTransformation),
			Result:     result,
		})
		if err != nil {
			return fmt.Errorf("Cannot create decoder: %v", err)
		}
		return dec.Decode(input)
	}

	switch gen.Type {
	case "base64_decode":
		return TransformBase64Decode{}, nil
	case "google_kms_decrypt":
		var kmsDec TransformGoogleKMSDecrypt
		err := decode(data, &kmsDec)
		if err != nil {
			return nil, fmt.Errorf("Failed to unmarshal google_kms_decrypt transform: %v", err)
		}
		return kmsDec, nil
	default:
		return nil, fmt.Errorf("Unknown transform type: %q", gen.Type)
	}
}

// TransformBase64Decode is a Transformation that base64-decodes its input
type TransformBase64Decode struct{}

// Transform implements Transformation.Transform for TransformBase64Decode
func (x TransformBase64Decode) Transform(in []byte) ([]byte, error) {
	result := make([]byte, base64.StdEncoding.DecodedLen(len(in)))
	_, err := base64.StdEncoding.Decode(result, in)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// MarshalYAML implements yaml.Marshaler for TransformBase64Decode
func (x TransformBase64Decode) MarshalYAML() (interface{}, error) {
	return "base64_decode", nil
}

// TransformGoogleKMSDecrypt is a Transformation that decrypts its input with
// Google KMS key with given properties
type TransformGoogleKMSDecrypt struct {
	Key RemoteValue `mapstructure:"key"`
}

// Transform implements Transformation.Transform for TransformGoogleKMSDecrypt
func (x TransformGoogleKMSDecrypt) Transform(in []byte) ([]byte, error) {
	ctx := context.Background()
	c, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return nil, err
	}

	var keyBytes []byte
	keyBytes, err = x.Key.Value()
	if err != nil {
		return nil, err
	}

	req := &kmspb.DecryptRequest{
		Name:       string(keyBytes),
		Ciphertext: in,
	}

	resp, err := c.Decrypt(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.Plaintext, nil
}

// MarshalYAML implements yaml.Marshaler for TransformGoogleKMSDecrypt
func (x TransformGoogleKMSDecrypt) MarshalYAML() (interface{}, error) {
	return yaml.MapSlice{
		yaml.MapItem{
			Key:   "type",
			Value: "google_kms_decrypt",
		},
		yaml.MapItem{
			Key:   "key",
			Value: x.Key,
		},
	}, nil
}
