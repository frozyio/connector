package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/satori/go.uuid"
)

//************************************* Connector <--> Broker interface section start ********************************
// Message types for Connector <--> Broker interface
const (
	// error message type
	ErrorMessageType = "frozy-error-message"

	// global channel SSH requests
	RegisterConnectorRequestType = "frozy-connector-id-request"
	RegisterConnectorReplyType   = "frozy-connector-id-request-reply"
	RegisterSSHRequestType       = "frozy-register-request"
	RegisterSSHRequestReplyType  = "frozy-register-request-reply"
	IntentSSHRequestType         = "frozy-intent-request"
	IntentSSHRequestReplyType    = "frozy-intent-request-reply"

	// SSH channel creation requests
	ConsumeSSHChannelType = "frozy-consume-app-id"
	ProvideSSHChannelType = "frozy-provide-app-id"

	// SSH keepalive request
	SSHKeepAliveMsgType = "keepalive@broker.frozy.io"
)

// CommunicationContainer stores data to be JSON marshaled and send through SSH message channel to
// remote connection side
type CommunicationContainer struct {
	MsgType string `json:"msg_type"`
	MsgData string `json:"msg_data"`
}

func toJSONStream(data interface{}) ([]byte, error) {
	var msgType string

	// check input interface
	rv := reflect.ValueOf(data)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil, errors.New("Can't process non Pointer or NULL values")
	}

	switch data.(type) {
	// error message
	case *JSONErrorData:
		msgType = ErrorMessageType

	// application registers
	case *JSONRegisterRequest:
		msgType = RegisterSSHRequestType
	case *JSONBrokerToRegisterRequestReply:
		msgType = RegisterSSHRequestReplyType

	// application intents
	case *JSONIntentRequest:
		msgType = IntentSSHRequestType
	case *JSONBrokerToIntentReply:
		msgType = IntentSSHRequestReplyType

	// application consume request
	case *JSONConsumeApplicationRequest:
		msgType = ConsumeSSHChannelType

	// connector ID runtime registration
	case *JSONConnectorIDRegisterRequest:
		msgType = RegisterConnectorRequestType
	case *JSONConnectorIDRegisterReply:
		msgType = RegisterConnectorReplyType

	// errornous default case
	default:
		return nil, errors.New("Unknown type received. Can't serialize it")
	}

	// do Marshal payload
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	// save marshaled data into container
	conData := &CommunicationContainer{
		MsgType: msgType,
		MsgData: string(payload),
	}

	// marshal communication container
	result, err := json.Marshal(conData)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// FromStream implements generic data deserializer for well known types of Requests/Replies
func FromStream(data []byte, out interface{}) error {
	// check output interface
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return errors.New("Can't process non Pointer or NULL output interface")
	}

	// Unmarshal main transport struct
	var commData CommunicationContainer
	err := json.Unmarshal(data, &commData)
	if err != nil {
		return err
	}

	isTypesMismatched := false

	switch out.(type) {
	// error message
	case *JSONErrorData:
		if commData.MsgType != ErrorMessageType {
			isTypesMismatched = true
		}

	// application registers
	case *JSONRegisterRequest:
		if commData.MsgType != RegisterSSHRequestType {
			isTypesMismatched = true
		}
	case *JSONBrokerToRegisterRequestReply:
		if commData.MsgType != RegisterSSHRequestReplyType {
			isTypesMismatched = true
		}

	// application intents
	case *JSONIntentRequest:
		if commData.MsgType != IntentSSHRequestType {
			isTypesMismatched = true
		}
	case *JSONBrokerToIntentReply:
		if commData.MsgType != IntentSSHRequestReplyType {
			isTypesMismatched = true
		}

	// application consume request
	case *JSONConsumeApplicationRequest:
		if commData.MsgType != ConsumeSSHChannelType {
			isTypesMismatched = true
		}

	// connector ID runtime registration
	case *JSONConnectorIDRegisterRequest:
		if commData.MsgType != RegisterConnectorRequestType {
			isTypesMismatched = true
		}
	case *JSONConnectorIDRegisterReply:
		if commData.MsgType != RegisterConnectorReplyType {
			isTypesMismatched = true
		}

	// errornous default case
	default:
		return errors.New("Unknown input interface type. Can't deserialize data")
	}

	if isTypesMismatched {
		return fmt.Errorf("Transport data and output interface types mismatch. Transport type: %s, output type: %s",
			commData.MsgType, reflect.TypeOf(out).String())
	}

	// Unmrshal data
	err = json.Unmarshal([]byte(commData.MsgData), out)
	if err != nil {
		return err
	}

	return nil
}

// JSONErrorData provides erros messaging container
type JSONErrorData struct {
	ErrorMsg string `json:"error_msg"`
}

// ToStream implements serializer for JSONConnectorIDRegisterRequest
func (j *JSONErrorData) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// ErrorViaCommunicationContainer gets error message on input and serialize
// it onto communication container structure
func ErrorViaCommunicationContainer(err_str string) ([]byte, error) {
	result := &JSONErrorData{
		ErrorMsg: err_str,
	}
	return result.ToStream()
}

// ErrorFromStream gets error message stream on input and deserialize
// it onto communication container structure
func ErrorFromStream(data []byte) (string, error) {
	var errorData JSONErrorData

	err := FromStream(data, &errorData)
	if err != nil {
		return "", err
	}

	return errorData.ErrorMsg, nil
}

// ConnectorMetadata stores information about connector that must be registered into DB
// for each Connector`s runtime. If this procedures did not completed, Broker will reject any
// requests from such connector
type ConnectorMetadata struct {
	ConnectorName      string `json:"conn_name"`
	ConnectorLocalIP   string `json:"conn_local_ip"`
	ConnectorOSName    string `json:"conn_os_name"`
	ConnectorHostname  string `json:"conn_hostname"`
	ConnectorCloudName string `json:"conn_cloud_name"`
	// some additional information here in future
}

// ConnectorIDRegisterRequest provide request to broker with connector metadata to
// get Connector`s runtime identifier. If Connector already registered we send
// current ConnectorID in metadata
type JSONConnectorIDRegisterRequest struct {
	ConnectorID   string            `json:"conn_id"`
	ConnectorData ConnectorMetadata `json:"conn_metadata"`
}

// ToStream implements serializer for JSONConnectorIDRegisterRequest
func (j *JSONConnectorIDRegisterRequest) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// ConnectorIDRegisterReply provide ConnectorID identifier received from Broker
// If Connector already registered, he sent their own ID in request, Broker check that ID
// and answers with SAME ID or error message if received ID isn't registered
type JSONConnectorIDRegisterReply struct {
	ConnectorID string `json:"conn_id"`
}

// ToStream implements serializer for JSONConnectorIDRegisterReply
func (j *JSONConnectorIDRegisterReply) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// ApplicationRegisterInfo stores information about ports of provided application
// this info can be used by consumer connector to listen on same ports for user
// convenience
type ApplicationRegisterInfo struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

// ApplicationIntentInfo stores information about ports of consumed application
// i.e. port number where consumer connector will listen local connections
// NOTE: As workaround for case when consumer will listen at same port as returned provide
// application info (in JSONBrokerToConsumerReply) we may use port = 0 in this structure
type ApplicationIntentInfo struct {
	Port uint16 `json:"port"`
}

// JSONRegisterRequest JSON provide request with application name pre-parsed
// (but of course not resolved) by connector and ready to process via policy
// after broker performs name resolution
type JSONRegisterRequest struct {
	Name     StructuredApplicationName        `json:"name"`
	Info     ApplicationRegisterInfo          `json:"info"`
	AuthInfo ApplicationActionRequestAuthInfo `json:"auth_info"`
}

// ToStream implements serializer for JSONRegisterRequest
func (j *JSONRegisterRequest) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// JSONBrokerToRegisterRequestReply describes Broker to Provider connector reply
// (and request from Broker to Provider later) on request to provide desired resource.
// When provider connects to broker and request application providing
// broker must return registered application UUID as answer to provide request
// When Broker needs to route application consume request to Provider he will request
// new channel creation to Provider and sent this data in request payload.
// Provider will lookup their runtime data by this appID and route request to proper
// application that was requested to provide early
// This UUID will be used for provided application selection on Provider side
type JSONBrokerToRegisterRequestReply struct {
	AppID uuid.UUID `json:"app_id"`
}

// ToStream implements serializer for JSONBrokerToRegisterRequestReply
func (j *JSONBrokerToRegisterRequestReply) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// JSONIntentRequest describes JSON consume request data where application name
// already pre-parsed (but of course not resolved) by connector and ready to
// process via policy after broker performs name resolution
type JSONIntentRequest struct {
	SourceAppName   StructuredApplicationName        `json:"src_name"`
	SourceAppInfo   ApplicationIntentInfo            `json:"src_info"`
	DestinationName StructuredApplicationName        `json:"dst_name"`
	AuthInfo        ApplicationActionRequestAuthInfo `json:"auth_info"`
}

// ToStream implements serializer for JSONIntentRequest
func (j *JSONIntentRequest) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// JSONBrokerToIntentReply describes JSON intent request's reply data with surrogate appID
// by that current request was registered on current connection on broker
// When consumer wants to consume application he must send that appID in their NewChannel Open request args
type JSONBrokerToIntentReply struct {
	AppID uuid.UUID `json:"app_id"`
}

// ToStream implements serializer for JSONBrokerToIntentReply
func (j *JSONBrokerToIntentReply) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// JSONConsumeApplicationRequest describes JSON application consume request from Consumer side when
// NewChannel must be opened
type JSONConsumeApplicationRequest struct {
	AppID uuid.UUID `json:"app_id"`
}

// ToStream implements serializer for JSONConsumeApplicationRequest
func (j *JSONConsumeApplicationRequest) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

//************************************* Connector <--> Broker interface section end ***********************************

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
