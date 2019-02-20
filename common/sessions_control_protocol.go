package common

import (
	//	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

// Message types for Connector <--> Broker and Connector <--> Connector paired session control interface
const (
	// transport level
	TransportSessionBrokerToConnector    = "fr-sess-br-conn"
	TransportSessionConnectorToBroker    = "fr-sess-conn-br"
	TransportSessionConnectorToConnector = "fr-sess-conn-conn"

	// session control sublevel
	SessionErrorMessageType = "fr-sess-err"

	// Connector commands range
	SessionContextIDRequestMessageType            = "fr-sess-context-id-request"
	SessionAppBrokersListRequestMessageType       = "fr-sess-app-brokers-list-request"
	SessionAppBrokerConnectRequestMessageType     = "fr-sess-app-broker-connect-request"
	SessionMovePairedConnectionRequestMessageType = "fr-sess-move-paired-connection-request"

	// Broker commands range
	// this request runs paired session move process. Broker must send this command to
	// Intent part of paired connection. Method returns last iteration status message
	SessionMoveRequestMessageType = "fr-sess-move-request"

	// send request answer timeout
	TransportSessionAnswerTimeout = 3
)

//****************************** CHANNELS CONTROL MESSAGING LAYER ***************************

// CommunicationContainer stores data to be JSON marshaled and send through SSH message channel to
// remote connection side
type PairSessCommContainer struct {
	MsgType string `json:"msg_type"`
	MsgData string `json:"msg_data"`
}

// sessMsgToStream implements generic data serializer for well known types of
// Requests/Replies in pair sessions communication
func sessMsgToStream(data interface{}) ([]byte, error) {
	var msgType string

	// check input interface
	rv := reflect.ValueOf(data)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil, errors.New("Can't process non Pointer or NULL values")
	}

	switch data.(type) {
	// error message
	case *SessionErrorData:
		msgType = SessionErrorMessageType

	case *SessionContextIDRequest:
		msgType = SessionContextIDRequestMessageType

	case *SessionAppBrokersListRequest:
		msgType = SessionAppBrokersListRequestMessageType

	case *SessionAppBrokerConnectRequest:
		msgType = SessionAppBrokerConnectRequestMessageType

	case *SessionMovePairedConnectionRequest:
		msgType = SessionMovePairedConnectionRequestMessageType

	case *SessionMoveRequest:
		msgType = SessionMoveRequestMessageType

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
	conData := &PairSessCommContainer{
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

// SessMsgFromStream implements generic data deserializer for well known types of
// Requests/Replies in pair sessions communication
func SessMsgFromStream(data []byte, out interface{}) error {
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
	case *SessionErrorData:
		if commData.MsgType != SessionErrorMessageType {
			isTypesMismatched = true
		}

	case *SessionContextIDRequest:
		if commData.MsgType != SessionContextIDRequestMessageType {
			isTypesMismatched = true
		}

	case *SessionAppBrokersListRequest:
		if commData.MsgType != SessionAppBrokersListRequestMessageType {
			isTypesMismatched = true
		}

	case *SessionAppBrokerConnectRequest:
		if commData.MsgType != SessionAppBrokerConnectRequestMessageType {
			isTypesMismatched = true
		}

	case *SessionMovePairedConnectionRequest:
		if commData.MsgType != SessionMovePairedConnectionRequestMessageType {
			isTypesMismatched = true
		}

	case *SessionMoveRequest:
		if commData.MsgType != SessionMoveRequestMessageType {
			isTypesMismatched = true
		}

	// errornous default case
	default:
		return errors.New("Unknown input interface type. Can't deserialize data")
	}

	if isTypesMismatched {
		// trying to get error from stream and return it
		if commData.MsgType == SessionErrorMessageType {
			// Unmrshal data
			var errorData SessionErrorData
			err = json.Unmarshal([]byte(commData.MsgData), errorData)
			if err != nil {
				return fmt.Errorf("Transport data and output interface types mismatch. Transport type: %s, output type: %s. Error recover from stream ends with error: %v",
					commData.MsgType, reflect.TypeOf(out).String(), err)
			} else {
				return fmt.Errorf("Error on requested operation. Remote side returned an error: %s", errorData.ErrorMsg)
			}
		} else {
			return fmt.Errorf("Transport data and output interface types mismatch. Transport type: %s, output type: %s",
				commData.MsgType, reflect.TypeOf(out).String())
		}
	}

	// Unmarshal data
	err = json.Unmarshal([]byte(commData.MsgData), out)
	if err != nil {
		return err
	}

	return nil
}

// ****************** Paired Session Move ************************
// NOTE: On current moment we don't needed any data transfer to Connector
// to transaction get started
type SessionMoveRequest struct {
	LastStatus string `json:"sess_move_status"`
}

// ToStream implements serializer for SessionMoveRequest
func (s *SessionMoveRequest) ToStream() ([]byte, error) {
	return sessMsgToStream(s)
}

// ***************** Remote Brokers list request ****************

type SessionAppBrokersListRequest struct {
	BrokerList []string `json"brokers_list"`
}

// ToStream implements serializer for SessionAppBrokersListRequest
func (s *SessionAppBrokersListRequest) ToStream() ([]byte, error) {
	return sessMsgToStream(s)
}

// *************** Remote Broker Connect request *****************

type SessionAppBrokerConnectRequest struct {
	BrokerData BrokerInfoData `json:"broker_data"`
}

// ToStream implements serializer for SessionAppBrokerConnectRequest
func (s *SessionAppBrokerConnectRequest) ToStream() ([]byte, error) {
	return sessMsgToStream(s)
}

// ************** Remote Paired session move request ************

type SessionMovePairedConnectionRequest struct {
	BrokerName       string `json:"broker_name"`
	PairedSessionIDX uint64 `json:"paired_session_idx"`
}

// ToStream implements serializer for SessionMovePairedConnectionRequest
func (s *SessionMovePairedConnectionRequest) ToStream() ([]byte, error) {
	return sessMsgToStream(s)
}

// ****************** Paired Session ContextID *******************
type SessionContextIDRequest struct {
	LocalConnectorID    uuid.UUID `json:"local_connector_id"`
	LocalPairSessionID  uint64    `json:"local_pair_sess_id"`
	RemoteConnectorID   uuid.UUID `json:"remote_connector_id"`
	RemotePairSessionID uint64    `json:"remote_pair_sess_id"`
}

// ToStream implements serializer for SessionContextIDRequest
func (s *SessionContextIDRequest) ToStream() ([]byte, error) {
	return sessMsgToStream(s)
}

// ****************** ERROR handling container *******************
// SessionErrorData provides erros messaging container
type SessionErrorData struct {
	ErrorMsg string `json:"error_msg"`
}

// ToStream implements serializer for JSONConnectorIDRegisterRequest
func (s *SessionErrorData) ToStream() ([]byte, error) {
	return sessMsgToStream(s)
}

// ErrorViaSessCommContainer gets error message on input and serialize
// it onto communication container structure
func ErrorViaSessCommContainer(err_str string) ([]byte, error) {
	result := &SessionErrorData{
		ErrorMsg: err_str,
	}
	return result.ToStream()
}

// ErrorFromStream gets error message stream on input and deserialize
// it onto communication container structure
func ErrorMsgFromSessCommStream(data []byte) (string, error) {
	var errorData SessionErrorData

	err := FromStream(data, &errorData)
	if err != nil {
		return "", err
	}
	return errorData.ErrorMsg, nil
}

//****************************** CHANNELS TRANSPORT LAYER ************************************

// Connector <-> Connector and Connector <-> Broker control protocol
// NOTE: Due to SSH channel request subsystem doesn't support full duplex messaging we need to implement some
// abstraction for durable message delivery
type RequestsMessageManagerIf interface {
	// Registers new request messages handler
	// handler - argument is function must return:
	//   MsgItem pointer - is struct with same MsgID as original for proper processing on remote end
	//                     and response data
	//   error code - if somewhat goes wrong
	RegisterMessageProcessingHandler(handler func(transportReqType string, requestData *MsgItem) (*MsgItem, error))
	// SendRequest sends request to desired ssh.Channel and waits for answer
	// Input parameters:
	//		RequestType - sublevel request type
	//		payload - any datatype encapsulated into JSON
	// NOTE: Function returns:
	//		payload - bytes of answer payload
	//		err - error if any errors occurred in SendRequest API invoke lifetime
	SendBrokerToConnectorRequest(RequestType string, payload []byte) ([]byte, error)
	SendConnectorToBrokerRequest(RequestType string, payload []byte) ([]byte, error)
	SendConnectorToConnectorRequest(RequestType string, payload []byte) ([]byte, error)
	// Returns channel Closer interface
	Closer() io.Closer
	// Returns channel reader interface
	Reader() io.Reader
	// Returns channel writer interface
	Writer() io.Writer
}

// MsgItem describes internal messages sublevel
type MsgItem struct {
	// MsgId is unique message identifier that used to register request intent
	// in messages manager. This value can be builded from UUID of Connector or
	// Broker and unique identifier of message used locally on Connector or Broker
	MsgID string `json:"msg_id"`
	// RequestType is messages sublevel request type. This data primarily  must be
	// processed by msgProcHandler
	RequestType string `json:"msg_request_type"`
	// Payload is message data (any generic datatype Marshaled into JSON)
	Payload []byte `json:"msg_payload"`
}

type requestMessageManager struct {
	// requests indexer
	reqID     uint64 // requests counter inside paired session
	regOffset uint64 // PairedSession IDX
	regPrefix string // ConnectorID

	// system OK status flag
	systemOK bool

	// logger to output messages
	logger *log.Entry

	// unhandled messages processing callback
	// Function must return:
	//   request type - is a transport level message type (needed for proper routing on broker)
	//   MsgItem pointer - is struct with same MsgID as original for proper processing on remote end
	//                     and response data
	//   error code - if somewhat goes wrong
	msgProcHandler func(transportReqType string, requestData *MsgItem) (*MsgItem, error)

	// parts of external SSH connectivity
	sshChannel  ssh.Channel
	sshRequests <-chan *ssh.Request

	// storage of active requesters
	mapLock         sync.Mutex
	waitingRequests map[string]chan *MsgItem
}

// NewRequestMessageManager creates new messages manager as additional wrap level upon SSH channel`s requests transport
// Input:
// 		channel - interface to SSH channel
//		requests - channel of new requests received from current SSH channel
//		logger - logger to output messages
//		messangerIDPrefix - messages ID prefix used by manager to produce unique message ID.
//							As prefix can be used ConnectorID/BrokerID as UUID value marshaled into string
func NewRequestMessageManager(channel ssh.Channel, requests <-chan *ssh.Request, logger *log.Entry, messangerIDPrefix string, regOffset uint64) (RequestsMessageManagerIf, error) {
	if logger == nil || channel == nil || requests == nil {
		return nil, errors.New("Can't init messages manager with empty input parameters")
	}

	msgMan := new(requestMessageManager)
	if msgMan == nil {
		panic("Can't create new requests messaging manager. Out of memory")
	}

	// set system OK status and other stuff
	msgMan.regPrefix = messangerIDPrefix
	msgMan.regOffset = regOffset
	msgMan.systemOK = true
	msgMan.logger = logger
	msgMan.sshChannel = channel
	msgMan.sshRequests = requests
	msgMan.waitingRequests = make(map[string]chan *MsgItem)

	// start processing handler
	go msgMan.requestsProcessHandler()

	return msgMan, nil
}

func (r *requestMessageManager) Reader() io.Reader {
	return io.Reader(r.sshChannel)
}

func (r *requestMessageManager) Writer() io.Writer {
	return io.Writer(r.sshChannel)
}

func (r *requestMessageManager) Closer() io.Closer {
	return io.Closer(r.sshChannel)
}

func (r *requestMessageManager) getNewMessageID() string {
	mID := atomic.AddUint64(&r.reqID, 1)
	return fmt.Sprintf("%s-%d-%d", r.regPrefix, r.regOffset, mID)
}

func (r *requestMessageManager) sendRequest(transportRequestType string, RequestType string, payload []byte) ([]byte, error) {
	if len(RequestType) == 0 || len(payload) == 0 {
		return nil, errors.New("Cam't process with empty input data")
	}

	if len(transportRequestType) == 0 {
		return nil, errors.New("Cam't process with empty transport request type")
	}

	if !r.systemOK {
		return nil, errors.New("Cam't process request due to: Subsystem isn't ready")
	}

	// create message item
	msg := &MsgItem{
		MsgID:       r.getNewMessageID(),
		RequestType: RequestType,
		Payload:     payload,
	}

	msgSerializedData, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("Cam't process request (marshal) due to: %v", err)
	}

	// create requester channel, register it and send original request
	requesterChan := make(chan *MsgItem)
	if requesterChan == nil {
		panic("Can't create message channel. Out of memory")
	}

	// check if msgID already registered
	r.mapLock.Lock()
	_, ok := r.waitingRequests[msg.MsgID]
	if !ok {
		r.waitingRequests[msg.MsgID] = requesterChan
	}
	r.mapLock.Unlock()

	if ok {
		return nil, errors.New("Cam't process request due to: Just created unique message identifier already registered")
	}

	// send original request
	_, err = r.sshChannel.SendRequest(transportRequestType, false, msgSerializedData)
	if err != nil {
		return nil, fmt.Errorf("Cam't send original request due to: %v", err)
	}

	r.logger.Debugf("Send MSG with ID: %s and wait for answer", msg.MsgID)

	// arm timeout for operation
	var requestReply *MsgItem
	timeOut := false
	timeOutChannel := time.After(time.Duration(TransportSessionAnswerTimeout) * time.Second)
MainLoop:
	for {
		select {
		case requestReply = <-requesterChan:
			break MainLoop
		case <-timeOutChannel:
			timeOut = true
			break MainLoop
		}
	}

	// close channel and delete requester from map
	close(requesterChan)
	r.mapLock.Lock()
	delete(r.waitingRequests, msg.MsgID)
	r.mapLock.Unlock()

	if requestReply == nil || timeOut {
		return nil, errors.New("Can't complete SendRequest transaction due to: Timeout I/O operation")
	}

	r.logger.Debugf("For listened MSG ID: %s received answer %+v", msg.MsgID, string(requestReply.Payload))

	return requestReply.Payload, nil
}

func (r *requestMessageManager) SendBrokerToConnectorRequest(RequestType string, payload []byte) ([]byte, error) {
	return r.sendRequest(TransportSessionBrokerToConnector, RequestType, payload)
}

func (r *requestMessageManager) SendConnectorToBrokerRequest(RequestType string, payload []byte) ([]byte, error) {
	return r.sendRequest(TransportSessionConnectorToBroker, RequestType, payload)
}

func (r *requestMessageManager) SendConnectorToConnectorRequest(RequestType string, payload []byte) ([]byte, error) {
	return r.sendRequest(TransportSessionConnectorToConnector, RequestType, payload)
}

func (r *requestMessageManager) RegisterMessageProcessingHandler(handler func(transportReqType string, requestData *MsgItem) (*MsgItem, error)) {
	r.mapLock.Lock()
	defer r.mapLock.Unlock()

	// we may accept a NIL handler to disable unregistered request processing
	r.msgProcHandler = handler
}

func (r *requestMessageManager) requestsProcessHandler() {
	r.logger.Debug("Messages manager`s request handler started")

	// wait for new requests from channel
	for newReq := range r.sshRequests {
		go r.handleRequest(newReq)
	}

	// drop system OK status
	r.systemOK = false

	r.logger.Debug("Messages manager`s request handler stoped")
}

// request processing handler
func (r *requestMessageManager) handleRequest(req *ssh.Request) {
	// just reply with @false@ for all requests because of our messaging model
	// builded on async returned requests in answer role
	if req.WantReply {
		req.Reply(false, nil)
	}
	// drop empty requests
	if req.Payload == nil || len(req.Type) == 0 {
		r.logger.Debug("Dropped request with empty payload or type")
		return
	}

	// get message data and trying to check if we have registered requester that wait for an answer
	var msg MsgItem
	err := json.Unmarshal(req.Payload, &msg)
	if err != nil {
		r.logger.Errorf("Can't unmarshal received request payload data due to: %v", err)
		return
	}

	// sanity check if request filled correctly
	if len(msg.MsgID) == 0 {
		r.logger.Error("Can't process received request dut to Empty MsgID field")
		return
	}

	r.logger.Debugf("Received request: transpType: %s, subType: %s, payload: %+v, msgID: %s",
		req.Type, msg.RequestType, string(msg.Payload), msg.MsgID)

	// check if request registered
	r.mapLock.Lock()
	requestDataChannel, ok := r.waitingRequests[msg.MsgID]
	r.mapLock.Unlock()
	if ok {
		r.logger.Debugf("FOUND registered listener for msgID: %s, sent message there", msg.MsgID)

		// push received data into channel
		// NOTE: delete requester from map and channel close must be done by API that
		// orginally register that request
		// NOTE: to give message processing API (sendRequest) some time to receive
		// our message we will use some timeout on this operation
		timeOutCh := time.After(5 * time.Millisecond)
		select {
		case requestDataChannel <- &msg:
		case <-timeOutCh:
			r.logger.Errorf("Can't sent received request with ID %s into requester channel", msg.MsgID)
		}
		return
	}

	r.logger.Debugf("Registered listener for msgID: %s is not FOUND, do ordinary processing", msg.MsgID)

	if r.msgProcHandler == nil {
		r.logger.Debug("Can't handle received unknown request dut to: Request processing handler isn't registered")
		return
	}

	// save original msgID
	storedMsgID := msg.MsgID

	// handle unknown requests and answer for it on success
	respMsgPtr, err := r.msgProcHandler(req.Type, &msg)
	if err != nil {
		r.logger.Errorf("Can't handle received request due to: %v", err)

		// send error to remote end
		msg.Payload, err = ErrorViaSessCommContainer(err.Error())
		if err != nil {
			r.logger.Errorf("Can't create error stream due to: %v", err)
		} else {
			if msg.MsgID != storedMsgID {
				r.logger.Warnf("Registered request handler changes original message ID by error: %s instead of: %s. Restore original value for proper processing",
					msg.MsgID, storedMsgID)
				msg.MsgID = storedMsgID
			}
			// sent request as answer
			answerData, err := json.Marshal(&msg)
			if err != nil {
				r.logger.Errorf("Can't marshal error answer due to: %v", err)
			} else {
				_, err = r.sshChannel.SendRequest(req.Type, false, answerData)
				if err != nil {
					r.logger.Errorf("Can't send error answer due to: %v", err)
				}
			}
		}
		return
	}

	if respMsgPtr == nil {
		r.logger.Debug("Registered request handler returns empty data, skip sending an answer")
		return
	}

	if respMsgPtr.MsgID != storedMsgID {
		r.logger.Warnf("Registered request handler returns non original message ID: %s instead of: %s. Restore original value for proper processing",
			respMsgPtr.MsgID, storedMsgID)
		respMsgPtr.MsgID = storedMsgID
	}

	// sent request as answer
	answerData, err := json.Marshal(respMsgPtr)
	if err != nil {
		r.logger.Errorf("Can't marshal request answer due to: %v", err)
		return
	}

	_, err = r.sshChannel.SendRequest(req.Type, false, answerData)
	if err != nil {
		r.logger.Errorf("Can't send request answer due to: %v", err)
		return
	}

	return
}
