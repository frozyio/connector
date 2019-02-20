package app

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"
	comm_app "gitlab.com/frozy.io/connector/common"
	"golang.org/x/crypto/ssh"
)

const (
	defaultMovedPairedSessionsLifetime = 60
)

// PairConnItem struct stores info about paired SSH connections
// on registers or intent side
// NOTE: real fprwarding operations done in another forwarding subsystem
// that approach needed for proper connection servicing
type PairConnItem struct {
	// local paired session credentials
	connPairIDX uint64
	connectorID uuid.UUID

	// paired connection control interface to upper level of SSH runtime
	PairedConnectionToSSHRuntimeIf

	// paired connection control interface to upper level of parent application
	PairedConnectionToApplicationIf

	// paired connection logger
	logger *log.Entry

	// flag used for distinguish different connections
	intentConnection                     bool
	moveTransactionRunning               atomicStatusData
	moveStatusLock                       sync.Mutex
	lastUnsuccessfullMoveOperationStatus string

	// interface to channel that uses current paired connection
	// and new request`s originator channel
	// NOTE: this channels will be used for Connector <-> Connector
	// interconnections
	messagesManager comm_app.RequestsMessageManagerIf

	// NOTE: Both connectors must get contextID of their opponents
	// ContextID = remoteConnectorID.remotePairedSessionID
	// NOTE: Master Connector is Intent connector
	remoteDataLock                    sync.Mutex
	remoteConnectorID                 uuid.UUID
	remotePairedSessionID             uint64
	remoteContextNegotiationRunning   atomicStatusData
	remoteContextNegotiationCompleted atomicStatusData
	// Interface to frowarder subsystem
	// NOTE: When paired connection created, Forwarder created for real local connection only
	// When paired connection moved from one broker to another, created forwarder must be moved on new
	// paired connection too
	forwarder DataForwarderIf
	// paired connection stop channel
	// NOTE: this channel used for signallig to paired connection that underlying
	// forwarding layer ends their operations (due to closing one of their connections, etc)
	// For paired connections that prepared to be moved, real forwarder doesn't created
	// but @forwardingStopCh@ created as timeout channel for emergency connection destroying
	forwardingStopCh chan time.Time
}

// PairedConnectionToApplicationIf implements base control of parent application
// from paired connection level
type PairedConnectionToApplicationIf interface {
	// Returns Connector`s status of operation
	// NOTE: Connector may work as Single Broker Connector and all ALB logic must be disabled
	GetALBEnabledStatus() bool
	// Get suitable Brokers list
	GetBrokersList(excludeBroker string) ([]comm_app.BrokerInfoData, int, error)
	// Connects to pointed broker and run application negotiation process according to application type
	// If application already connected, just returns and drop outdated sshRuntime flag
	ConnectToDesiredBroker(brData comm_app.BrokerInfoData) error
	// Get application registration status from desired SSH connection registered in ssRuntime storage
	GetApplicationRegistrationStatus(brokerName string) (bool, error)
	// Select existed sshRuntime by Broker name and request paired connection to desired Connector
	// Broker in this mode searches application registration from pointed connector
	// This operation can be succeded from Intent type application only
	ConnectToDesiredConnectorViaBroker(brokerName string, connectorID uuid.UUID) (PairedConnectionControlIf, error)
	// Gets Paired connection control interface by desired Broker name (selects sshRuntime) and paired connection IDX
	// that must already exists in that sshRuntime
	// Primary this API will be used by Opponent Connector when Intent connector will ask it to get control onto created
	// paired connection
	GetPairConnectionControl(brokerName string, pairConnIdx uint64) (PairedConnectionControlIf, error)
}

// PairedConnectionToSSHRuntimeIf implements methods to interconnection with SSH runtime
// subsystem of application
type PairedConnectionToSSHRuntimeIf interface {
	// deletes pair connection from SSH storage registration
	DeletePairConnection(pConn *PairConnItem) error
	// Returns broker name from current SSH connection
	GetConnectionBrokerName() string
}

// PairedConnectionControlIf implements control of paired connection
// from another paired connection
type PairedConnectionControlIf interface {
	// returns remote negotiate process status with opponent connector
	// also API returns remoteConnectorID and remotePairedSessionIDX
	GetRemoteNegotiateStatus() (bool, uuid.UUID, uint64)
	// API gets DataForwarder interface and register their channel into it
	// NOTE: API will success if forwarder interfaceis NIL in requested paired session
	// DataForwarder interface will be saved in paired session
	RegisterChannelInDataForwarder(dfif DataForwarderIf) error
	// Returns Data Forwarder interface from requested paired session
	GetDataForwarder() (DataForwarderIf, error)
}

func CreatePairedConnection(appData *applicationItemData, sshRuntime *sshConnectionRuntime, localConn net.Conn, pairChannel ssh.Channel, pairRequests <-chan *ssh.Request) (PairedConnectionControlIf, error) {
	var err error

	// some sanity checks
	if appData == nil || appData.logger == nil || appData.connMetaData == nil || appData.appSSHStorage.sshConnectorIf == nil {
		return nil, errors.New("Can't process empty Application data")
	}
	if sshRuntime == nil || pairChannel == nil || pairRequests == nil {
		return nil, errors.New("Can't process empty SSH runtime or Paired channels")
	}

	// create and register paired connection
	pairConn := &PairConnItem{
		logger:                          appData.logger,
		connPairIDX:                     appData.appSSHStorage.sshConnectorIf.GetNewConnectionIDX(),
		connectorID:                     appData.connMetaData.GetConnectorID(),
		intentConnection:                appData.appIntentType,
		PairedConnectionToSSHRuntimeIf:  PairedConnectionToSSHRuntimeIf(sshRuntime),
		PairedConnectionToApplicationIf: PairedConnectionToApplicationIf(appData),
		forwardingStopCh:                make(chan time.Time, 1),
	}

	// create message transport layer
	pairConn.messagesManager, err = comm_app.NewRequestMessageManager(pairChannel, pairRequests, pairConn.logger, pairConn.connectorID.String()+"c", pairConn.connPairIDX)
	if err != nil {
		return nil, fmt.Errorf("Can't create SSH channel request messages manager due to: %v", err)
	}

	// register request processing handler
	pairConn.messagesManager.RegisterMessageProcessingHandler(pairConn.channelRequestProcessingHandler)

	// if local connection is valid, create forwarder
	if localConn != nil {
		fwdData := &dataFwdRemotePart{
			writer:             pairConn.messagesManager.Writer(),
			reader:             pairConn.messagesManager.Reader(),
			closer:             pairConn.messagesManager.Closer(),
			forwardStopChannel: pairConn.forwardingStopCh,
		}
		pairConn.forwarder, err = NewForwarder(localConn, fwdData, appData.appIntentType, appData.logger)
		if err != nil {
			return nil, fmt.Errorf("Can't create Data Forwarder due to: %v", err)
		}
	}

	// register connection in storage
	err = sshRuntime.RegisterPairConnection(pairConn)
	if err != nil {
		return nil, fmt.Errorf("Can't register paired connection due to: %v", err)
	} else {
		appData.logger.Debugf("Paired connection %d successfully registered", pairConn.connPairIDX)
	}

	// run control thread
	go pairConn.run()

	return PairedConnectionControlIf(pairConn), nil
}

func (p *PairConnItem) RegisterChannelInDataForwarder(dfif DataForwarderIf) error {
	// for atomic register operation we will use moveStatusLock
	p.moveStatusLock.Lock()
	defer p.moveStatusLock.Unlock()

	// some sanity checks
	if dfif == nil {
		return errors.New("Input Data Forwarder interface is empty, can't register new paired channel in that forwarder")
	}

	if p.forwarder != nil {
		return errors.New("Data Forwarder interface already inited for requested paired session")
	}

	// create forward data from paired connection runtime channel
	fwdData := &dataFwdRemotePart{
		writer:             p.messagesManager.Writer(),
		reader:             p.messagesManager.Reader(),
		closer:             p.messagesManager.Closer(),
		forwardStopChannel: p.forwardingStopCh,
	}

	err := dfif.RegisterNewRemotePart(fwdData)
	if err != nil {
		return err
	}

	// if all ok, register data forwarder in paired connection
	p.forwarder = dfif

	return nil
}

func (p *PairConnItem) GetDataForwarder() (DataForwarderIf, error) {
	if p.forwarder == nil {
		return nil, errors.New("Data Forwarder interface isn't inited for requested paired session")
	}

	return p.forwarder, nil
}

func (p *PairConnItem) channelRequestProcessingHandler(transportReqType string, requestData *comm_app.MsgItem) (*comm_app.MsgItem, error) {
	var err error

	// some sanity check
	if len(transportReqType) == 0 || requestData == nil {
		return nil, errors.New("Can't process request with empty input data")
	}

	switch transportReqType {
	case comm_app.TransportSessionBrokerToConnector:
		// NOTE: this messages will control sessions move originated by Broker
		switch requestData.RequestType {
		case comm_app.SessionMoveRequestMessageType:
			// answer OK to broker and start move transaction
			requestData.Payload, err = p.brokerMoveRequestHandler(requestData.Payload)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("Can't process unsupported sublevel message type: %s for supported transport type: %s",
				requestData.RequestType, transportReqType)
		}
	case comm_app.TransportSessionConnectorToConnector:
		// this is ordinary Connector to Connector communications
		switch requestData.RequestType {
		case comm_app.SessionContextIDRequestMessageType:
			// answer for remote request
			requestData.Payload, err = p.remoteContextIDRequestHandler(requestData.Payload)
			if err != nil {
				return nil, err
			}

		case comm_app.SessionAppBrokersListRequestMessageType:
			// answer for remote request
			requestData.Payload, err = p.remoteBrokersListRequestHandler(requestData.Payload)
			if err != nil {
				return nil, err
			}

		case comm_app.SessionAppBrokerConnectRequestMessageType:
			// answer for remote request
			requestData.Payload, err = p.remoteConnectToBrokerRequestHandler(requestData.Payload)
			if err != nil {
				return nil, err
			}

		case comm_app.SessionMovePairedConnectionRequestMessageType:
			// answer for remote request
			requestData.Payload, err = p.remotePairedSessionMoveRequestHandler(requestData.Payload)
			if err != nil {
				return nil, err
			}

		default:
			return nil, fmt.Errorf("Can't process unsupported sublevel message type: %s for supported transport type: %s",
				requestData.RequestType, transportReqType)
		}
	default:
		return nil, fmt.Errorf("Can't process unsupported transport message type: %s", transportReqType)
	}

	return requestData, nil
}

func (p *PairConnItem) brokerMoveRequestHandler(inData []byte) ([]byte, error) {
	var request comm_app.SessionMoveRequest

	err := comm_app.SessMsgFromStream(inData, &request)
	if err != nil {
		return nil, fmt.Errorf("Can't process session move request due to: %v", err)
	}

	// some sanity check
	if len(request.LastStatus) != 0 {
		return nil, fmt.Errorf("Can't process session move request due to: Request data isn't Empty as expected")
	}

	p.logger.Debug("Received session move request")

	// check if paired connection is intent and it's not already in move transaction
	if !p.intentConnection || p.moveTransactionRunning.IsStatusOK() {
		return nil, fmt.Errorf("Can't process session move request due to: Non Intent paired connection or move transaction already started")
	}

	// set last move status. This data can be used for details about last move operation fail status
	request.LastStatus = p.getLastMoveStatus()

	answerData, err := request.ToStream()
	if err != nil {
		return nil, fmt.Errorf("Can't process session move request due to: %v", err)
	}

	// starting transaction
	go p.sessionMoveHandler()

	return answerData, nil
}

func (p *PairConnItem) getLastMoveStatus() string {
	p.moveStatusLock.Lock()
	defer p.moveStatusLock.Unlock()

	return p.lastUnsuccessfullMoveOperationStatus
}

func (p *PairConnItem) setLastMoveStatus(err error) {
	p.moveStatusLock.Lock()
	defer p.moveStatusLock.Unlock()

	p.lastUnsuccessfullMoveOperationStatus = err.Error()

	p.logger.Error(err)
}

// Main session move handler. All logic will be there
func (p *PairConnItem) sessionMoveHandler() {
	// check running status
	if !p.moveTransactionRunning.StatusCheckAndSet(true) {
		p.logger.Info("Session move process already started by another thread")
		return
	}
	defer p.moveTransactionRunning.StatusSet(false)

	p.logger.Debugf("Started paired session %d move transaction", p.connPairIDX)

	// get application specific broker list except current connected broker
	localBrokersList, _, err := p.GetBrokersList(p.GetConnectionBrokerName())
	if err != nil {
		p.setLastMoveStatus(fmt.Errorf("Can't complete local Brokers list get  process due to: %v", err))
		return
	}

	// check if we have brokers to connect
	if len(localBrokersList) == 0 {
		p.setLastMoveStatus(errors.New("There are no available brokers on local connector to move paired connection"))
		return
	}

	// ask remote Connector for application specific broker list
	req := &comm_app.SessionAppBrokersListRequest{}
	serialData, err := req.ToStream()
	if err != nil {
		p.setLastMoveStatus(fmt.Errorf("Can't start remote Brokers list get  process (Marshal step) due to: %v", err))
		return
	}

	answerData, err := p.messagesManager.SendConnectorToConnectorRequest(comm_app.SessionAppBrokersListRequestMessageType, serialData)
	if err != nil {
		p.setLastMoveStatus(fmt.Errorf("Can't complete remote Brokers list get  process (Send request step)  due to: %v", err))
		return
	}

	// unmarshal remote brokers list data
	var result comm_app.SessionAppBrokersListRequest
	err = comm_app.SessMsgFromStream(answerData, &result)
	if err != nil {
		p.setLastMoveStatus(fmt.Errorf("Can't complete remote Brokers list get  process (Unmarshal stage) due to: %v", err))
		return
	}

	// check if we have brokers to connect on remote connector
	if len(result.BrokerList) == 0 {
		p.setLastMoveStatus(errors.New("There are no available brokers on remote connector to move paired connection"))
		return
	}

	// select appropriate broker from list and trying to connect there
	// if connection fail on any side, select nexr appropriate broker with higher scores
	var selectedBrokers []comm_app.BrokerInfoData
	for _, locBrIt := range localBrokersList {
		for _, remBrIt := range result.BrokerList {
			if locBrIt.BrokerName == remBrIt {
				selectedBrokers = append(selectedBrokers, locBrIt)
			}
		}
	}

	// check if no one broker can be used by both connectors
	if len(selectedBrokers) == 0 {
		p.setLastMoveStatus(errors.New("There are no available brokers for both connectors to move paired connection"))
		return
	}

	// ask remote connector to coonect to the best broker that we selected
	var connectedSharedBroker comm_app.BrokerInfoData
	for _, brIt := range selectedBrokers {
		// ask remote Connector for application specific broker list
		req := &comm_app.SessionAppBrokerConnectRequest{
			BrokerData: brIt,
		}
		serialData, err := req.ToStream()
		if err != nil {
			p.setLastMoveStatus(fmt.Errorf("Can't complete remote connector to Broker %s connect process (Marshal step) due to: %v", brIt.BrokerName, err))
			continue
		}

		answerData, err := p.messagesManager.SendConnectorToConnectorRequest(comm_app.SessionAppBrokerConnectRequestMessageType, serialData)
		if err != nil {
			p.setLastMoveStatus(fmt.Errorf("Can't complete remote connector to Broker %s connect process (Send request step)  due to: %v", brIt.BrokerName, err))
			continue
		}

		// unmarshal remote brokers list data
		var result comm_app.SessionAppBrokerConnectRequest
		err = comm_app.SessMsgFromStream(answerData, &result)
		if err != nil {
			p.setLastMoveStatus(fmt.Errorf("Can't complete remote connector to Broker %s connect process (Unmarshal stage) due to: %v", brIt.BrokerName, err))
			continue
		}

		// connect to same broker from local connector
		err = p.ConnectToDesiredBroker(brIt)
		if err != nil {
			p.setLastMoveStatus(fmt.Errorf("Can't complete local connector to Broker %s connect process (SSH connection stage) due to: %v", brIt.BrokerName, err))
			continue
		}

		// save connected Broker name and break the loop
		connectedSharedBroker = brIt
		break
	}

	// check if we connected to broker
	if reflect.DeepEqual(&connectedSharedBroker, &comm_app.BrokerInfoData{}) {
		p.setLastMoveStatus(errors.New("There are no connected shared broker for both connectors to move paired connection"))
		return
	}

	// NOTE: after connectors will connects to Broker they will run their negotiations seqeunces.
	// we must wait some time before trying to build moved paired connection via broker or do polling with operation
	// repeat until success for some timeout period

	// ask to establish pair connection to remote connector without physical connection to local ends
	remConnID, _ := p.getRemoteConnectorData()
	var counter int
	for {
		isOk, err := p.GetApplicationRegistrationStatus(connectedSharedBroker.BrokerName)
		if err != nil {
			p.setLastMoveStatus(fmt.Errorf("Can't get application registration status via Broker %s due to: %v",
				connectedSharedBroker.BrokerName, err))
			return
		}

		// TBD: as additioanl robust step we can check remote application registration status on opponent connector

		if !isOk {
			time.Sleep(time.Millisecond)
			counter++
		} else {
			break
		}

		// we wait for 2 seconds
		if counter > 20 {
			p.setLastMoveStatus(fmt.Errorf("Can't get application registration status via Broker %s due to: Timeout",
				connectedSharedBroker.BrokerName))
			return
		}
	}

	// at this moment remote connector must register their application too
	// if not, we will wait for a 5 seconds in next step
	pairConnControlIf, err := p.ConnectToDesiredConnectorViaBroker(connectedSharedBroker.BrokerName, remConnID)
	if err != nil {
		p.setLastMoveStatus(fmt.Errorf("Can't establish moved pair session via Broker %s on ConnectorID: %s due to: %v",
			connectedSharedBroker.BrokerName, remConnID.String(), err))
		return
	}

	// wait remote megotiation success in that pair connection
	counter = 0
	var remoteMovedConnID uuid.UUID
	var remoteMovedPairSessIDX uint64
	var isOk bool
	for {
		isOk, remoteMovedConnID, remoteMovedPairSessIDX = pairConnControlIf.GetRemoteNegotiateStatus()
		if !isOk {
			time.Sleep(time.Millisecond)
			counter++
		} else {
			break
		}

		// we wait for 5 seconds (due to SendConnectorToConnectorRequest may wait for a 3 seconds each)
		if counter > 50 {
			p.setLastMoveStatus(fmt.Errorf("Can't get metadata for remote moved pair session via Broker %s on ConnectorID: %s due to: Timeout",
				connectedSharedBroker.BrokerName, remConnID.String()))
			return
		}
	}

	// NOTE: by design all moved pair connection is limited with lifetime until they will be triggered to
	// stay @normal@ paired connection. So we will not worry about proper closing such connections
	// last sanity check before connection moving
	if remoteMovedConnID != remConnID {
		p.setLastMoveStatus(fmt.Errorf("Can't complete paired session move sequence due to: destination paired session established on wrong connector: expected: %s but received %s",
			remConnID.String(), remoteMovedConnID.String()))
		return
	}

	// register channel from just created session in data forwarder of current paired session
	err = pairConnControlIf.RegisterChannelInDataForwarder(p.forwarder)
	if err != nil {
		p.setLastMoveStatus(fmt.Errorf("Can't complete paired session move  process sequence due to: %v", err))
		return
	}

	// ask remote side to move local connection end into new paired connection and do it himself locally
	moveReq := &comm_app.SessionMovePairedConnectionRequest{
		BrokerName:       connectedSharedBroker.BrokerName,
		PairedSessionIDX: remoteMovedPairSessIDX,
	}
	serialData, err = moveReq.ToStream()
	if err != nil {
		p.setLastMoveStatus(fmt.Errorf("Can't complete paired session move  process (Marshal step) due to: %v", err))
		return
	}

	answerData, err = p.messagesManager.SendConnectorToConnectorRequest(comm_app.SessionMovePairedConnectionRequestMessageType, serialData)
	if err != nil {
		p.setLastMoveStatus(fmt.Errorf("Can't complete paired session move  process (Send request step)  due to: %v", err))
		return
	}

	// ok, remote forwarder stopped and wait for primary channel close event
	p.forwarder.StopForward()

	// wait for transactions stop in forwarder
	for {
		up, down := p.forwarder.GetForwardStates()
		if !up && !down {
			// close current paired connection channel and break the loop
			p.messagesManager.Closer().Close()
			break
		} else {
			time.Sleep(time.Millisecond)
		}
	}

	p.logger.Debugf("Paired session %d move transaction successfully completed", p.connPairIDX)

	return
}

func (p *PairConnItem) remotePairedSessionMoveRequestHandler(inData []byte) ([]byte, error) {
	var request comm_app.SessionMovePairedConnectionRequest
	// get move request data
	err := comm_app.SessMsgFromStream(inData, &request)
	if err != nil {
		return nil, fmt.Errorf("Can't process paired session move request (Unmarshal stage) due to: %v", err)
	}

	// now, in context of current paired session (that originally receive request to move from broker)
	// we must to get control interface of just created paired session (that already connected via another broker)
	// and register their channel in data forwarder of current session

	// trying to get control interface of just created paired session
	pairedConnIf, err := p.GetPairConnectionControl(request.BrokerName, request.PairedSessionIDX)
	if err != nil {
		return nil, fmt.Errorf("Can't process paired session move request (Created paired session control get stage) due to: %v", err)
	}

	// register channel from just created session in data forwarder of current paired session
	err = pairedConnIf.RegisterChannelInDataForwarder(p.forwarder)
	if err != nil {
		return nil, fmt.Errorf("Can't process paired session move request (Register new channel in data forwarder stage) due to: %v", err)
	}

	// and stop forwarding in data forwarder of current paired session for primary SSH channel
	// NOTE: forwarding will be restarted when primary channel will be closed
	// After that event, registered channel will be replaced with primary channel and paired
	// session move process will be completed
	p.forwarder.StopForward()

	// just return same data as answer without error
	return inData, nil
}

func (p *PairConnItem) remoteConnectToBrokerRequestHandler(inData []byte) ([]byte, error) {
	var request comm_app.SessionAppBrokerConnectRequest

	err := comm_app.SessMsgFromStream(inData, &request)
	if err != nil {
		return nil, fmt.Errorf("Can't process remote Broker connect request (Unmarshal stage) due to: %v", err)
	}

	// check that request is empty
	// check if we connected to broker
	if reflect.DeepEqual(&request.BrokerData, &comm_app.BrokerInfoData{}) {
		return nil, errors.New("Can't process remote Broker connect request due to: Input broker data is empty")
	}

	// connect to broker
	err = p.ConnectToDesiredBroker(request.BrokerData)
	if err != nil {
		return nil, fmt.Errorf("Can't process remote Broker connect request (Connection stage) due to: %v", err)
	}

	// just return same data as answer without error
	return inData, nil
}

func (p *PairConnItem) remoteBrokersListRequestHandler(inData []byte) ([]byte, error) {
	var request comm_app.SessionAppBrokersListRequest

	err := comm_app.SessMsgFromStream(inData, &request)
	if err != nil {
		return nil, fmt.Errorf("Can't process remote Broker list request (Unmarshal stage) due to: %v", err)
	}

	// check that request is empty
	if len(request.BrokerList) != 0 {
		return nil, errors.New("Can't process remote Broker list request due to: Input broker list isn't empty")
	}

	// get application specific broker list except current connected broker
	brokersList, _, err := p.GetBrokersList(p.GetConnectionBrokerName())
	if err != nil {
		return nil, fmt.Errorf("Can't complete Brokers list get  process (List request stage) due to: %v", err)
	}

	// reload brokers into request struct
	for _, brIt := range brokersList {
		request.BrokerList = append(request.BrokerList, brIt.BrokerName)
	}

	answerData, err := request.ToStream()
	if err != nil {
		return nil, fmt.Errorf("Can't complete Brokers list get  process (Marshal stage) due to: %v", err)
	}

	return answerData, nil
}

func (p *PairConnItem) remoteContextIDRequestHandler(inData []byte) ([]byte, error) {
	var request comm_app.SessionContextIDRequest

	err := comm_app.SessMsgFromStream(inData, &request)
	if err != nil {
		return nil, fmt.Errorf("Can't process remote context ID request due to: %v", err)
	}

	// some sanity check
	if len(request.LocalConnectorID) == 0 || request.LocalPairSessionID == 0 {
		return nil, fmt.Errorf("Can't process remote context ID request due to: Empty local data in request")
	}

	p.logger.Debugf("Received remote context ID request from: Connector %s, Pair session: %d",
		request.LocalConnectorID, request.LocalPairSessionID)

	// update struct
	request.RemoteConnectorID = p.connectorID
	request.RemotePairSessionID = p.connPairIDX

	answerData, err := request.ToStream()
	if err != nil {
		return nil, fmt.Errorf("Can't process remote context ID request due to: %v", err)
	}

	return answerData, nil
}

// remoteContextIDNegotiate communicates with remote connector and get
// their ContextID. When ContextID received, API exits
func (p *PairConnItem) remoteContextIDNegotiate(exitChannel chan bool) {
	// check running status
	if !p.remoteContextNegotiationRunning.StatusCheckAndSet(true) {
		p.logger.Debug("Remote ContextID negotiation process already started by another thread")
		return
	}

	// force bad status until success on exit
	p.remoteContextNegotiationCompleted.StatusSet(false)
	defer p.remoteContextNegotiationRunning.StatusSet(false)

	// create request for ContextID and send it to remote connector
	req := &comm_app.SessionContextIDRequest{
		LocalConnectorID:   p.connectorID,
		LocalPairSessionID: p.connPairIDX,
	}
	serialData, err := req.ToStream()
	if err != nil {
		p.logger.Errorf("Can't start RemoteContextID negotiation process due to: %v", err)
		return
	}

	timeOutCh := time.After(time.Nanosecond)
MainLoop:
	for {
		select {
		case <-exitChannel:
			p.logger.Debug("Paired connection remote Negotiation process exited by signal")
			return

		case <-timeOutCh:
			// rearm timer
			timeOutCh = time.After(time.Duration(5) * time.Second)

			answerData, err := p.messagesManager.SendConnectorToConnectorRequest(comm_app.SessionContextIDRequestMessageType, serialData)
			if err != nil {
				p.logger.Errorf("Can't complete RemoteContextID negotiation process (send stage) due to: %v", err)
				continue
			}

			// get data
			var result comm_app.SessionContextIDRequest
			err = comm_app.SessMsgFromStream(answerData, &result)
			if err != nil {
				p.logger.Errorf("Can't complete RemoteContextID negotiation process (Unmarshal stage) due to: %v", err)
				continue
			}

			// some sanity checks
			if result.LocalConnectorID == req.LocalConnectorID &&
				result.LocalPairSessionID == req.LocalPairSessionID &&
				len(result.RemoteConnectorID) != 0 &&
				result.RemotePairSessionID != 0 {
				// set output data
				p.setRemoteConnectorData(result.RemoteConnectorID, result.RemotePairSessionID)

				// and stop loop
				break MainLoop
			} else {
				p.logger.Error("Can't complete RemoteContextID negotiation process due to: Invalid data returned")
				continue
			}
		}
	}

	p.remoteContextNegotiationCompleted.StatusSet(true)

	p.logger.Debugf("Remote context ID request successfully negotiated: Local Connector %s, Local Pair session: %d, Remote Connector %s, Remote Pair session: %d",
		p.connectorID, p.connPairIDX, p.remoteConnectorID, p.remotePairedSessionID)

	return
}

func (p *PairConnItem) GetRemoteNegotiateStatus() (bool, uuid.UUID, uint64) {
	if !p.remoteContextNegotiationCompleted.IsStatusOK() {
		return false, uuid.Nil, 0
	}

	p.remoteDataLock.Lock()
	defer p.remoteDataLock.Unlock()
	return true, p.remoteConnectorID, p.remotePairedSessionID
}

func (p *PairConnItem) setRemoteConnectorData(remConnID uuid.UUID, remConnPairIDX uint64) {
	p.remoteDataLock.Lock()
	defer p.remoteDataLock.Unlock()

	p.remoteConnectorID = remConnID
	p.remotePairedSessionID = remConnPairIDX
}

func (p *PairConnItem) getRemoteConnectorData() (remConnID uuid.UUID, remConnPairIDX uint64) {
	p.remoteDataLock.Lock()
	defer p.remoteDataLock.Unlock()

	return p.remoteConnectorID, p.remotePairedSessionID
}

func (p *PairConnItem) run() {
	negStopCh := make(chan bool, 1)
	var timeoutChForMovedConn <-chan time.Time

	if p.forwarder == nil {
		timeoutChForMovedConn = time.After(time.Duration(defaultMovedPairedSessionsLifetime) * time.Second)
	} else {
		timeoutChForMovedConn = nil
	}

	// run Connectors remote context ID negotiation process only if ALB mode is enabled
	if p.GetALBEnabledStatus() {
		go p.remoteContextIDNegotiate(negStopCh)
	}

	// waiting for forwarder status
MainLoop:
	for {
		select {
		case <-timeoutChForMovedConn:
			// check if paired connection remains moved
			if p.forwarder == nil {
				p.logger.Debugf("Received TIMEOUT signal for paired connection %d", p.connPairIDX)
				break MainLoop
			} else {
				// reset timeout channel if paired connection enriched with data forwarder
				timeoutChForMovedConn = nil
			}
		case <-p.forwardingStopCh:
			p.logger.Debugf("Received STOP signal for paired connection %d from data forwarder", p.connPairIDX)
			break MainLoop
		}
	}

	// forcing channel closing (if it's already not done)
	p.messagesManager.Closer().Close()

	// stop negotiation channel if already not stopped
	negStopCh <- true

	// delete paired connection from SSH context
	err := p.DeletePairConnection(p)
	if err != nil {
		p.logger.Errorf("Can't delete paired connection %d from registry", p.connPairIDX)
	} else {
		p.logger.Debugf("Paired connection %d successfully deleted from registry", p.connPairIDX)
	}
}
