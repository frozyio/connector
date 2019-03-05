package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

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
	RegisterSSHRejectType        = "frozy-register-reject"
	IntentSSHRejectType          = "frozy-intent-reject"

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

	// application rejects from Broker side
	case *JSONConsumeApplicationReject:
		msgType = IntentSSHRejectType
	case *JSONRegisteredApplicationReject:
		msgType = RegisterSSHRejectType

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

	// application rejects from Broker side
	case *JSONConsumeApplicationReject:
		if commData.MsgType != IntentSSHRejectType {
			isTypesMismatched = true
		}
	case *JSONRegisteredApplicationReject:
		if commData.MsgType != RegisterSSHRejectType {
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

// ToStream implements serializer for JSONErrorData
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
	ConnectorID   string                           `json:"conn_id"`
	ConnectorData ConnectorMetadata                `json:"conn_metadata"`
	AuthInfo      ApplicationActionRequestAuthInfo `json:"auth_info"`
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
	Host string `json:"host"`
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
	AppID           uuid.UUID `json:"app_id"`
	MovedConnection bool      `json:"moved_connection"`
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
	AppID       uuid.UUID `json:"app_id"`
	ConnectorID uuid.UUID `json:"connector_id"`
}

// ToStream implements serializer for JSONConsumeApplicationRequest
func (j *JSONConsumeApplicationRequest) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// JSONConsumeApplicationReject describes JSON application consume reject by Broker due to policy check
type JSONConsumeApplicationReject struct {
	AppID uuid.UUID `json:"app_id"`
}

// ToStream implements serializer for JSONConsumeApplicationReject
func (j *JSONConsumeApplicationReject) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

// JSONRegisteredApplicationReject describes JSON registered application reject by Broker due to policy check
type JSONRegisteredApplicationReject struct {
	AppID uuid.UUID `json:"app_id"`
}

// ToStream implements serializer for JSONRegisteredApplicationReject
func (j *JSONRegisteredApplicationReject) ToStream() ([]byte, error) {
	return toJSONStream(j)
}

//************************************* Connector <--> Broker interface section end ***********************************
