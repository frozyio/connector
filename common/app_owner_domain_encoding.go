package common

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/satori/go.uuid"
)

// StructuredApplicationName describes parsed application name structure
// Name and DomainList strings is in hostname/dns format
// Owner is non encoded string in format that is not compatable with hostname/dns strings
type StructuredApplicationName struct {
	Name       string
	DomainList []string
	Owner      string
}

// ShortAppName returns app name builded from app.name and app.domainList fields
func (f *StructuredApplicationName) ShortAppName() string {
	// construct result string
	result := []string{f.Name}
	result = append(result, f.DomainList...)

	return strings.Join(result, ".")
}

// LongAppName returns app name builded from app.name, app.domainList and owner fields
func (f *StructuredApplicationName) LongAppName() string {
	// construct result string
	result := []string{f.Name}
	result = append(result, f.DomainList...)
	result = append(result, f.Owner)

	return strings.Join(result, ".")
}

// ApplicationNameString stores encoded application name where all parts may be splitted by '.'
// and owner (last) part is encoded into hostname/dns compatable format
type ApplicationNameString string

// ApplicationIdentity stores full application identification data
// this includes Name data startcu that can be encoded into string and
// application owner UUID
type ApplicationIdentity struct {
	Name       string
	DomainList []string
	Owner      uuid.UUID
}

// ShortAppName returns app name builded from app.name and app.domainList fields
func (f *ApplicationIdentity) ShortAppName() string {
	// construct result string
	result := []string{f.Name}
	result = append(result, f.DomainList...)

	return strings.Join(result, ".")
}

// ApplicationActionRequestAuthInfo stores data about consume/provide request originator
// in simple case it is User with their AccessToken
type ApplicationActionRequestAuthInfo struct {
	AuthType    RequestAuthType
	AccessToken string
}

// RequestAuthType decribes type of authentication used in request
// it may be Trust User or something else in the future
type RequestAuthType int

// Reuest type enumerator
const (
	TrustUserAuthType RequestAuthType = 1
)

var requestAuthTypeNames = map[RequestAuthType]string{
	TrustUserAuthType: "trust user",
}

// RequestAuthTypeNameGet returns name of consumer auth type
// If there are no requested type in storage, function will return error
func RequestAuthTypeNameGet(tp RequestAuthType) (string, error) {
	name, ok := requestAuthTypeNames[tp]
	if !ok {
		return "", errors.New("Unknown auth type")
	}

	return name, nil
}

func IsSpecialNameUsed(s string) bool {
	return strings.ToLower(s) == "self"
}

// Regular expression used to validate RFC1035 hostnames
var hostnameAlphaRegex = regexp.MustCompile(`^([[:alnum:]][[:alnum:]\-]{0,61}[[:alnum:]]|[[:alpha:]])$`)
var hostnameDigitRegex = regexp.MustCompile(`^.*[[:^digit:]].*$`)

// Regexp for unencoded Owner string validation
var validOwnerRegex = regexp.MustCompile(`[[:alnum:]!#$%&` + "`" + `*+\-/=?^_'".{\|}~]{0,62}`)

// Regexp for encoded Owner field validation for insert additioanl 'z' symbol at start of Owner field
var startEncodedRegex = regexp.MustCompile(`^[[:digit:]|\-]`)

type replacerData struct {
	or string
	ch string
}

var replacerContent = []replacerData{
	replacerData{or: "!", ch: "-e"},
	replacerData{or: "#", ch: "-h"},
	replacerData{or: "$", ch: "-b"},
	replacerData{or: "%", ch: "-p"},
	replacerData{or: "&", ch: "-m"},
	replacerData{or: "`", ch: "-u"},
	replacerData{or: "*", ch: "-s"},
	replacerData{or: "+", ch: "-l"},
	replacerData{or: "-", ch: "-i"},
	replacerData{or: "/", ch: "-c"},
	replacerData{or: "=", ch: "-y"},
	replacerData{or: "?", ch: "-n"},
	replacerData{or: "^", ch: "-f"},
	replacerData{or: "_", ch: "-g"},
	replacerData{or: "'", ch: "-v"},
	replacerData{or: `"`, ch: "-q"},
	replacerData{or: ".", ch: "-d"},
	replacerData{or: "{", ch: "-j"},
	replacerData{or: "|", ch: "-k"},
	replacerData{or: "}", ch: "-r"},
	replacerData{or: "@", ch: "-a"},
	replacerData{or: "~", ch: "-t"},
}

func encodeReplacer() *strings.Replacer {
	var replData []string

	for _, val := range replacerContent {
		replData = append(replData, val.or)
		replData = append(replData, val.ch)
	}

	return strings.NewReplacer(replData...)
}

func decodeReplacer() *strings.Replacer {
	var replData []string

	for _, val := range replacerContent {
		replData = append(replData, val.ch)
		replData = append(replData, val.or)
	}

	return strings.NewReplacer(replData...)
}

func encodeString(s string) (string, error) {
	if len(s) == 0 {
		return "", errors.New("Can't encode empty string")
	}

	if s[0] == 'z' {
		var b bytes.Buffer
		b.WriteString("z")
		b.WriteString(s)
		s = b.String()
	}

	s = encodeReplacer().Replace(s)

	if startEncodedRegex.MatchString(s) {
		var b bytes.Buffer
		b.WriteString("z")
		b.WriteString(s)
		s = b.String()
	}

	return s, nil
}

func decodeString(s string) (string, error) {
	if len(s) == 0 {
		return "", errors.New("Can't decode empty string")
	}

	if s[0] == 'z' {
		s = s[1:]
	}

	s = decodeReplacer().Replace(s)

	return s, nil
}

// EncodeToString gets ApplicationIdentity struct and trying to encode it
func (a *StructuredApplicationName) EncodeToString() (string, error) {
	var result string

	if len(a.Name) == 0 {
		return "", errors.New("Can't encode empty Name field")
	}

	//	fmt.Printf("APP name: %s, check 0: %v, check 1: %v\n", a.Name, hostnameAlphaRegex.MatchString(a.Name), hostnameDigitRegex.MatchString(a.Name))

	// check for valid symbols in names
	if !(hostnameAlphaRegex.MatchString(a.Name) && hostnameDigitRegex.MatchString(a.Name)) {
		return "", errors.New("Invalid symbols in input struct (Name field)")
	}

	//	fmt.Printf("Owner: %s, check 0: %v, check 1: %v\n", a.Owner, validOwnerRegex.MatchString(a.Owner), IsSpecialNameUsed(a.Owner))

	if !IsSpecialNameUsed(a.Owner) && !validOwnerRegex.MatchString(a.Owner) {
		return "", errors.New("Invalid symbols in input struct (Owner field)")
	}

	// construct result string
	result = a.Name

	isDomainsExists := false
	for _, val := range a.DomainList {
		if len(val) == 0 {
			return "", errors.New("Invalid empty Domain string in input struct")
		}

		if !(hostnameAlphaRegex.MatchString(val) && hostnameDigitRegex.MatchString(val)) {
			return "", errors.New("Invalid symbols in input struct (DomainList field)")
		}

		isDomainsExists = true
		result += "." + val
	}

	// at first stage of checks all parts of name is ok
	if len(a.Owner) > 0 {
		encOwner, err := encodeString(a.Owner)
		if err != nil {
			return "", fmt.Errorf("Can't encode Owner field due to: %v", err)
		}

		if len(encOwner) > 63 {
			return "", errors.New("Can't encode Owner field due to resulted field length exceeded 63 symbols")
		}

		result += "." + encOwner
	} else {
		if isDomainsExists {
			result += ".self"
		}
	}

	return result, nil
}

// DecodeApplicationString gets encoded string and trying to decode it
func DecodeApplicationString(st ApplicationNameString) (StructuredApplicationName, error) {
	// split name into parts
	appNameParts := strings.Split(string(st), ".")

	if len(appNameParts) > 1 {
		owner := appNameParts[len(appNameParts)-1]
		if len(owner) > 63 {
			return StructuredApplicationName{}, fmt.Errorf("Can't decode Owner field due to field length exceeded 63 symbols")
		}

		decOwner, err := decodeString(owner)
		if err != nil {
			return StructuredApplicationName{}, fmt.Errorf("Can't decode Owner field due to: %v", err)
		}

		// ok, fill structure
		var domains []string
		if len(appNameParts) > 2 {
			for idx := 1; idx < len(appNameParts)-1; idx++ {
				domains = append(domains, appNameParts[idx])
			}
		}

		return StructuredApplicationName{
			Name:       appNameParts[0],
			DomainList: domains,
			Owner:      decOwner,
		}, nil
	} else {
		return StructuredApplicationName{
			Name:  string(st),
			Owner: "self",
		}, nil
	}
}

// ApplicationEqual compares two ApplicationIdentity/ApplicationStructedName structs and returns true if they are equals
func ApplicationsEqual(a, b interface{}) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}

	return false
}
