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

// application specific runtime SSH connection storage tank
type sshConnectionRuntime struct {
	brokerData *comm_app.BrokerInfoData

	// SSH part of runtime data
	sshConn         ssh.Conn
	sshConnChannels <-chan ssh.NewChannel
	sshConnRequests <-chan *ssh.Request
	sshConnStatus   atomicStatusData

	// application provide/intent negotiated status storage
	appState atomicStatusData

	// paired connections storage
	pairConnLock sync.Mutex
	pairedConns  map[uint64]*PairConnItem

	// old connection that was not updated on last update event
	// we must not shedule new pair connections on that connections
	outdatedConn atomicStatusData

	// broker registered AppID (defined by broker after provided/intent application advertising)
	brokerAppID uuid.UUID
}

func (s *sshConnectionRuntime) RegisterPairConnection(pConn *PairConnItem) error {
	s.pairConnLock.Lock()
	defer s.pairConnLock.Unlock()

	if pConn == nil {
		return errors.New("Empty paired connection PTR")
	}

	// check if connection already exists
	_, ok := s.pairedConns[pConn.connPairIDX]
	if ok {
		return fmt.Errorf("Paired connection with IDX: %d to %s already registered", pConn.connPairIDX, BrokerInfo(s))
	}

	// register connection
	s.pairedConns[pConn.connPairIDX] = pConn

	return nil
}

func (s *sshConnectionRuntime) DeletePairConnection(pConn *PairConnItem) error {
	s.pairConnLock.Lock()
	defer s.pairConnLock.Unlock()

	if pConn == nil {
		return errors.New("Empty paired connection PTR")
	}

	// check if connection exists
	_, ok := s.pairedConns[pConn.connPairIDX]
	if !ok {
		return fmt.Errorf("Paired connection with IDX: %d to %s doesn't registered", pConn.connPairIDX, BrokerInfo(s))
	}

	// clear connection
	delete(s.pairedConns, pConn.connPairIDX)

	return nil
}

func (s *sshConnectionRuntime) GetConnectionBrokerName() string {
	if s.brokerData != nil {
		return s.brokerData.BrokerName
	}

	return ""
}

// BrokerInfo outputs Broker connection data for logging system
func BrokerInfo(sshRuntime *sshConnectionRuntime) string {
	if sshRuntime == nil {
		return "Broker: Unknown"
	}

	return fmt.Sprintf("Broker: %s (%s:%s)",
		sshRuntime.brokerData.BrokerName,
		sshRuntime.brokerData.BrokerIP.String(),
		fmt.Sprintf("%d", sshRuntime.brokerData.BrokerPort))
}

// ApplicationSSHConnectionsStorage generic SSH connections storage
type ApplicationSSHConnectionsStorage struct {
	sshConnLock    sync.Mutex
	sshConnectorIf BrokerConnectionIf
	sshConnections map[string]*sshConnectionRuntime
}

// GetSuitableSSHConnectionsList returns suitable SSH connections list that ordered according to Broker scores
func (a *ApplicationSSHConnectionsStorage) GetSuitableSSHConnectionsList() []*sshConnectionRuntime {
	a.sshConnLock.Lock()
	defer a.sshConnLock.Unlock()

	// construct slice of suitable brokers
	var brList []comm_app.BrokerInfoData
	for _, sshConnPtr := range a.sshConnections {
		// skip outdated connections or deleted
		if sshConnPtr.outdatedConn.IsStatusOK() || !sshConnPtr.sshConnStatus.IsStatusOK() {
			continue
		}

		brList = append(brList, *(sshConnPtr.brokerData))
	}

	// sort brokers
	brList = BrokersListSort(brList)

	// construct SSH connections list
	var resultSSHConnList []*sshConnectionRuntime
	for _, brIt := range brList {
		sshConnPtr, ok := a.sshConnections[brIt.BrokerName]
		if ok {
			resultSSHConnList = append(resultSSHConnList, sshConnPtr)
		}
	}

	return resultSSHConnList
}

// UpdateExistedConnectionsWithNewBrokers updates current SSH connections with Broker scores and establishes new connections
// This info used for selection best SSH connections for paired session termination
func (a *ApplicationSSHConnectionsStorage) UpdateExistedConnectionsWithNewBrokers(brokerList []comm_app.BrokerInfoData) []comm_app.BrokerInfoData {
	a.sshConnLock.Lock()
	defer a.sshConnLock.Unlock()

	// define find Broker handler that will process ingress New Brokres slice and search requested broker
	// if Broker found it will be deleted from slice and returned to invoker
	findBroker := func(brokerName string, brokers *[]comm_app.BrokerInfoData) (comm_app.BrokerInfoData, bool) {
		if len(brokerName) == 0 || brokers == nil {
			return comm_app.BrokerInfoData{}, false
		}

		for idx, brokerIt := range *brokers {
			if brokerName == brokerIt.BrokerName {
				copy((*brokers)[idx:], (*brokers)[idx+1:])
				// eliminate memory leak possibility
				(*brokers)[len(*brokers)-1] = comm_app.BrokerInfoData{}
				(*brokers) = (*brokers)[:len(*brokers)-1]

				return brokerIt, true
			}
		}

		return comm_app.BrokerInfoData{}, false
	}

	// set outdated flag on connections that not in new Brokers list and close it if there are no paired connections
	for brokerName, brokerIt := range a.sshConnections {
		brNewData, found := findBroker(brokerName, &brokerList)
		if found {
			// update existed connection to Broker with new scores and reset outdated flag
			brokerIt.brokerData = &brNewData
			brokerIt.outdatedConn.StatusSet(false)
		} else {
			// check outdated flag. If it's not set, wet set it and continue upto next iteration
			// if on next iteration outdated flag is set, we drop empty session
			if !brokerIt.outdatedConn.IsStatusOK() {
				// set outdated flag
				brokerIt.outdatedConn.StatusSet(true)
				continue
			}
			// get connection lock and check if connection doesn't have a paired sessions
			// if so then close SSH session
			brokerIt.pairConnLock.Lock()
			if len(a.sshConnections[brokerName].pairedConns) == 0 {
				// close SSH session
				brokerIt.sshConnStatus.StatusSet(false)

				//	fmt.Printf("Close connection to %s\n", BrokerInfo(brokerIt))

				brokerIt.sshConn.Close()
			}
			brokerIt.pairConnLock.Unlock()
		}
	}

	return brokerList
}

// CheckIfSSHConnectionExists checks if SSH connection to desired Broker already registered
func (a *ApplicationSSHConnectionsStorage) CheckIfSSHConnectionExists(brokerName string, forceOutdateUpdate bool) *sshConnectionRuntime {
	a.sshConnLock.Lock()
	defer a.sshConnLock.Unlock()

	// check if connection already exists
	sshRunTimePtr, ok := a.sshConnections[brokerName]
	if ok {
		if forceOutdateUpdate {
			sshRunTimePtr.outdatedConn.StatusSet(false)
		}
		return sshRunTimePtr
	}

	return nil
}

func (a *ApplicationSSHConnectionsStorage) GetPairedConnectionControlIface(brokerName string, pairConnIdx uint64) (PairedConnectionControlIf, error) {
	a.sshConnLock.Lock()
	defer a.sshConnLock.Unlock()

	// check if connection exists
	sshRunTimePtr, ok := a.sshConnections[brokerName]
	if ok {
		sshRunTimePtr.pairConnLock.Lock()
		defer sshRunTimePtr.pairConnLock.Unlock()

		pairConn, ok := sshRunTimePtr.pairedConns[pairConnIdx]
		if ok {
			return PairedConnectionControlIf(pairConn), nil
		} else {
			return nil, fmt.Errorf("In requested SSH connection to broker %s pair session with ID %d isn't registered", brokerName, pairConnIdx)
		}
	}

	return nil, fmt.Errorf("Requested SSH connection to broker %s isn't registered", brokerName)
}

// RegisterSSHConnection adds SSH connection into application connection storage
func (a *ApplicationSSHConnectionsStorage) RegisterSSHConnection(sshConnData *sshConnectionRuntime) error {
	a.sshConnLock.Lock()
	defer a.sshConnLock.Unlock()

	if sshConnData == nil {
		return errors.New("Empty connection PTR")
	}

	// check if connection already exists
	_, ok := a.sshConnections[sshConnData.brokerData.BrokerName]
	if ok {
		return fmt.Errorf("Connection to requested %s already registered", BrokerInfo(sshConnData))
	}

	// register connetion
	a.sshConnections[sshConnData.brokerData.BrokerName] = sshConnData

	return nil
}

// UnregisterSSHConnection deletes SSH connection from application connection storage
func (a *ApplicationSSHConnectionsStorage) UnregisterSSHConnection(sshConnData *sshConnectionRuntime) error {
	a.sshConnLock.Lock()
	defer a.sshConnLock.Unlock()

	if sshConnData == nil {
		return errors.New("Empty connection PTR")
	}

	// check if connection exists
	_, ok := a.sshConnections[sshConnData.brokerData.BrokerName]
	if !ok {
		return fmt.Errorf("Connection to requested %s doesn't registered for current application", BrokerInfo(sshConnData))
	}

	// clear connection
	delete(a.sshConnections, sshConnData.brokerData.BrokerName)

	return nil
}

// DropAllSSHConnections drops all SSH connection registered into application connection storage
func (a *ApplicationSSHConnectionsStorage) DropAllSSHConnections() {
	a.sshConnLock.Lock()
	defer a.sshConnLock.Unlock()

	for _, sshConnIt := range a.sshConnections {
		// just close SSH connections all unregistration work will do their own handlers
		sshConnIt.sshConn.Close()
	}
}

type applicationItemData struct {
	// Connector`s metadata interface
	connMetaData ConnectorDataIf

	// application specific logger
	logger *log.Entry

	// app flag that selects application type
	appIntentType bool

	// application access token
	accessToken string

	// application rejected status channel
	// NOTE: Broker can reject application registration if policy has changed or access token
	// with application was registered has been revoked. In this case all we can do is to stop
	// application
	emergencyStopChannel chan bool
	// used for upper level indication that current application is stopped and will not process ingress
	// requests until Connector restart
	applicationStopped atomicStatusData

	// register app data part
	appName    comm_app.StructuredApplicationName
	appRegInfo comm_app.ApplicationRegisterInfo
	connectTo  string

	// intent app data part
	sourceAppName      comm_app.StructuredApplicationName
	sourceAppRegInfo   comm_app.ApplicationIntentInfo
	destinationAppName comm_app.StructuredApplicationName
	listenAt           string
	listener           net.Listener

	// Application SHH connections storage
	appSSHStorage ApplicationSSHConnectionsStorage
}

// ****************************** PairedConnectionToApplicationIf implementation begin ****************
func (a *applicationItemData) GetALBEnabledStatus() bool {
	return a.connMetaData.GetALBEnabledStatus()
}

func (a *applicationItemData) GetBrokersList(excludeBroker string) ([]comm_app.BrokerInfoData, int, error) {
	return a.getSuitableBrokers(true, excludeBroker)
}

func (a *applicationItemData) ConnectToDesiredBroker(brData comm_app.BrokerInfoData) error {
	return a.connectToBroker(brData, true)
}

func (a *applicationItemData) GetApplicationRegistrationStatus(brokerName string) (bool, error) {
	// find sshRuntime for requested Broker
	sshRuntime := a.appSSHStorage.CheckIfSSHConnectionExists(brokerName, false)
	if sshRuntime == nil {
		return false, fmt.Errorf("SSH connection to requested Broker %s doesn't exists", brokerName)
	}

	return sshRuntime.appState.IsStatusOK() && sshRuntime.sshConnStatus.IsStatusOK(), nil
}

func (a *applicationItemData) ConnectToDesiredConnectorViaBroker(brokerName string, remoteConnectorID uuid.UUID) (PairedConnectionControlIf, error) {
	// some sanity check
	if uuid.Equal(remoteConnectorID, uuid.Nil) {
		return nil, errors.New("Remote connector ID is Empty")
	}

	// find sshRuntime for requested Broker
	sshRuntime := a.appSSHStorage.CheckIfSSHConnectionExists(brokerName, false)
	if sshRuntime == nil {
		return nil, fmt.Errorf("SSH connection to requested Broker %s doesn't exists", brokerName)
	}

	// check if application type is Intent and registration already done
	if !a.appIntentType {
		return nil, errors.New("Current application is not Intent, can't request paired connection")
	}
	if !(sshRuntime.appState.IsStatusOK() && sshRuntime.sshConnStatus.IsStatusOK()) {
		return nil, errors.New("Current application is not registered on Broker yet or it's have some problems with SSH connection to Broker")
	}

	// trying to open paired connection to desired connector
	channel, requests, err := a.requestConsumeChannel(sshRuntime, remoteConnectorID)
	if err != nil {
		return nil, fmt.Errorf("Can't open channel to %s for desired ConnectorID %s due to: %v",
			BrokerInfo(sshRuntime), remoteConnectorID.String(), err)
	}

	// create moved pair session and get their control interface
	pairIf, err := CreatePairedConnection(a, sshRuntime, nil, channel, requests)
	if err != nil {
		// close opened channel
		channel.Close()
		return nil, fmt.Errorf("Can't create moved paired connection to %s for desired ConnectorID %s due to: %v", BrokerInfo(sshRuntime), remoteConnectorID.String(), err)
	}

	return pairIf, nil
}

func (a *applicationItemData) GetPairConnectionControl(brokerName string, pairConnIdx uint64) (PairedConnectionControlIf, error) {
	return a.appSSHStorage.GetPairedConnectionControlIface(brokerName, pairConnIdx)
}

// ****************************** PairedConnectionToApplicationIf implementation end ****************

func (a *applicationItemData) ApplicationRejectHandler(sshRuntime *sshConnectionRuntime, data []byte, isRegisteredApp bool) error {
	if sshRuntime == nil {
		return errors.New("Can't process reject request with empty SSH runtime")
	}

	// check input parameters and application types
	if !isRegisteredApp && !a.appIntentType {
		return errors.New("Invalid Intent application reject request type to Registered application")
	} else if isRegisteredApp && a.appIntentType {
		return errors.New("Invalid Registered application reject request type to Intent application")
	}
	// get appID from request and check it with registered in SSH connection
	var appID uuid.UUID
	if isRegisteredApp {
		var requestData comm_app.JSONRegisteredApplicationReject
		err := comm_app.FromStream(data, &requestData)
		if err != nil {
			return fmt.Errorf("Can't deserialize Registered application reject request data due to: %v", err)
		}
		appID = requestData.AppID
	} else {
		var requestData comm_app.JSONConsumeApplicationReject
		err := comm_app.FromStream(data, &requestData)
		if err != nil {
			return fmt.Errorf("Can't deserialize Intent application reject request data due to: %v", err)
		}
		appID = requestData.AppID
	}
	// check requested App
	if appID != sshRuntime.brokerAppID {
		return fmt.Errorf("Can't process reject request data due to: AppIDs mismatched: received %s, expected %s",
			appID.String(), sshRuntime.brokerAppID.String())
	}

	// set App state flag
	a.applicationStopped.StatusSet(true)

	// stop local listener thread for Intent
	if a.appIntentType {
		a.listener.Close()
	}

	// signal to main thread on exit
	a.emergencyStopChannel <- true

	return nil
}

// return Brokers list registered in ALB cache
// lazySelection - disables intent/register selection. If true - ALL brokers will be selected
// excludeBroker - sets name of the Broker that must be deleted from resulted list
func (a *applicationItemData) getSuitableBrokers(lazySelection bool, excludeBroker string) ([]comm_app.BrokerInfoData, int, error) {
	var brokerList []comm_app.BrokerInfoData
	var brokerDesireConnections int
	var err error

	// at startup for Intent app trying to get application specific brokers list
	if a.appIntentType && a.GetALBEnabledStatus() && !lazySelection {
		brokerList, brokerDesireConnections, err = a.appSSHStorage.sshConnectorIf.GetBrokersList(a.destinationAppName)
		if err != nil {
			a.logger.Warnf("Can't get application specific brokers list due to: %v", err)
		}
	}

	// for all apps if Broker list is empty, get generic brokers list
	if len(brokerList) == 0 {
		brokerList, brokerDesireConnections, err = a.appSSHStorage.sshConnectorIf.GetBrokersList(comm_app.StructuredApplicationName{})
		if err != nil {
			a.logger.Warnf("Can't get generic broker list due to: %v", err)
		}
	}

	// there are nothing to do because we doesn't have any Brokers
	if len(brokerList) == 0 {
		return brokerList, 0, errors.New("No available Brokers found. Wait some time and try again")
	}

	//		a.logger.Debugf("Brokers list to update: %+v", brokerList)

	// filter Broker list against Black listed ones
	brokerList = a.appSSHStorage.sshConnectorIf.FilterBlackListedBrokers(brokerList)

	if len(excludeBroker) != 0 {
	ExternalLoop:
		for {
			found := false
		InnerLoop:
			for idx, brIt := range brokerList {
				if brIt.BrokerName == excludeBroker {
					brokerList = append(brokerList[:idx], brokerList[idx+1:]...)
					found = true
					break InnerLoop
				}
			}

			if !found {
				break ExternalLoop
			}
		}
	}

	//	a.logger.Debugf("Brokers list flitered: %+v", brokerList)

	return brokerList, brokerDesireConnections, nil
}

func (a *applicationItemData) connectToBroker(brokerIt comm_app.BrokerInfoData, forceOutdateUpdate bool) error {
	// because of we can have cases when AD/ALB returns many brokers with same name (TTL doesn't expired at read moment)
	// we must check that SSH connection on that broker doesn't already exists
	if a.appSSHStorage.CheckIfSSHConnectionExists(brokerIt.BrokerName, forceOutdateUpdate) != nil {
		return nil
	}

	a.logger.Debugf("Trying to setup SSH connectionto to %s",
		fmt.Sprintf("Broker: %s (%s:%d)", brokerIt.BrokerName, brokerIt.BrokerIP.String(), brokerIt.BrokerPort))

	// connect to Broker
	sshRuntime, err := a.appSSHStorage.sshConnectorIf.ConnectToBroker(brokerIt)
	if err != nil {
		// add broker into BlackList
		errAdd := a.appSSHStorage.sshConnectorIf.AddBrokerToBlackList(brokerIt)
		if errAdd != nil {
			a.logger.Errorf("Can't add %s into Blacklist due to: %v",
				fmt.Sprintf("Broker: %s (%s:%s)", brokerIt.BrokerName, brokerIt.BrokerIP.String(), fmt.Sprintf("%d", brokerIt.BrokerPort)), errAdd)
		}
		return fmt.Errorf("Can't connect to %s due to: %v, Broker will be placed into Blacklist for a while",
			fmt.Sprintf("Broker: %s (%s:%d)", brokerIt.BrokerName, brokerIt.BrokerIP.String(), brokerIt.BrokerPort), err)
	}

	// register new SSH connection
	err = a.appSSHStorage.RegisterSSHConnection(sshRuntime)
	if err != nil {
		sshRuntime.sshConn.Close()
		return fmt.Errorf("Can't register SSH connection to %s due to: %v", BrokerInfo(sshRuntime), err)
	}

	// run control handler for new connection
	go a.applicationConnectionProcess(sshRuntime)

	a.logger.Debugf("SSH connection to %s successfully Registered", BrokerInfo(sshRuntime))

	return nil
}

// intent application main handler do general work for app process init
// and monitor/control their state
func (a *applicationItemData) run() {
	// add some pause to get time for first discovery iteration
	time.Sleep(time.Duration(2) * time.Second)

	a.logger.Info("Application processing thread started")

	if a.appIntentType {
		// first step run local listener
		go a.intentAppListen()
	}

	// running right after start
	timeToRunCh := time.After(time.Millisecond)

	// as second step run Broker connection control thread
MainLoop:
	for {
		select {
		case <-a.emergencyStopChannel:
			// drop all our SSH connections and exit
			a.logger.Info("Emergency application stop requested from broker, drop all SSH connections to brokers and exit")
			a.appSSHStorage.DropAllSSHConnections()
			break MainLoop

		case <-timeToRunCh:
			// get Brokers list without Lazy selection, so in this mode for Intents applications Brokers
			// with registered dst Intent application will be selected
			brokerList, brokerDesireConnections, err := a.getSuitableBrokers(false, "")
			if err != nil {
				a.logger.Debugf("%s", err)

				// rearm our channel
				timeToRunCh = time.After(applicationRunInterval)
				continue MainLoop
			}

			// form highly desired Brokers list
			highDesiredBrokers := brokerList
			if brokerDesireConnections < len(brokerList) {
				highDesiredBrokers = brokerList[:brokerDesireConnections]
			}

			// now check established SSH connections, update them and connects to new Brokers
			newBrokersToConnect := a.appSSHStorage.UpdateExistedConnectionsWithNewBrokers(highDesiredBrokers)

			//		a.logger.Debugf("Brokers list to NEW SSH connections: %+v", newBrokersToConnect)

			// connect to new Brokers
			for _, brokerIt := range newBrokersToConnect {
				err = a.connectToBroker(brokerIt, false)
				if err != nil {
					a.logger.Error(err)
				}
			}

			// rearm our channel
			timeToRunCh = time.After(applicationRunInterval)
		}
	}

	a.logger.Info("Application processing thread stopped")
}

func (a *applicationItemData) applicationConnectionProcess(sshRuntime *sshConnectionRuntime) {
	// make buffered control channel for poolerControl
	stopCh := make(chan bool, 1)

	// run channel handlers
	go handleChannelsCreation(sshRuntime, a)
	go handleGlobalRequests(sshRuntime, a)
	go a.poolerHandler(sshRuntime, stopCh)

	// stay here until SSH connection is OK
	err := sshRuntime.sshConn.Wait()
	if err != nil {
		a.logger.Warnf("SSH connection to %s is dropped with cause: %v", BrokerInfo(sshRuntime), err)
	}

	// set unusable status on SSH connection
	sshRuntime.sshConnStatus.StatusSet(false)

	// stop event handler
	stopCh <- true

	err = a.appSSHStorage.UnregisterSSHConnection(sshRuntime)
	if err != nil {
		a.logger.Errorf("Can't unregister SSH connection to %s due to: %v", BrokerInfo(sshRuntime), err)
	}

	a.logger.Debugf("SSH connection to %s successfully Unregistered and control handler closed", BrokerInfo(sshRuntime))
}

func (a *applicationItemData) poolerHandler(sshRuntime *sshConnectionRuntime, stopCh chan bool) {
	// we will periodically try to intent/provide our application if status of connection is bad
	// arm first check for a short time interval
	timeToConsumeChannel := time.After(time.Millisecond)

	for {
		select {
		case <-stopCh:
			// exit signal received
			a.logger.Debugf("Application event pooler to %s is exited by exit signal", BrokerInfo(sshRuntime))
			return

		case <-timeToConsumeChannel:
			if !sshRuntime.appState.IsStatusOK() {
				var err error
				if a.appIntentType {
					err = a.intentApplicationNegotiate(sshRuntime)
				} else {
					err = a.registerApplicationNegotiate(sshRuntime)
				}
				if err != nil {
					a.logger.Errorf("Can't complete application negotiation process on %s due to: %v", BrokerInfo(sshRuntime), err)
				}
			}
			// set new timeout
			timeToConsumeChannel = time.After(applicationRunInterval)

			// some new event`s channels here
		}
	}
}

func (a *applicationItemData) intentApplicationNegotiate(sshRuntime *sshConnectionRuntime) error {
	// create intent request to broker
	intentRequest := &comm_app.JSONIntentRequest{
		SourceAppName:   a.sourceAppName,
		SourceAppInfo:   a.sourceAppRegInfo,
		DestinationName: a.destinationAppName,
		AuthInfo: comm_app.ApplicationActionRequestAuthInfo{
			AuthType:    comm_app.TrustUserAuthType,
			AccessToken: a.accessToken,
		},
	}

	a.logger.Debugf("Do intent request to %s", BrokerInfo(sshRuntime))

	// encode request into JSON
	intentRequestJSON, err := intentRequest.ToStream()
	if err != nil {
		return fmt.Errorf("Intent request can't be encoded due to: %v", err)
	}

	// send request and get appID on success
	isSupported, replyData, err := sshRuntime.sshConn.SendRequest(comm_app.IntentSSHRequestType, true, intentRequestJSON)
	if err != nil {
		return fmt.Errorf("Intent request can't be completed due to: %v", err)
	} else if !isSupported {
		if replyData == nil {
			a.logger.Errorf("Destimnation application for Intent doesn't registered on %s yet, but intent request registered", BrokerInfo(sshRuntime))
		} else {
			errMsg, errLocal := comm_app.ErrorFromStream(replyData)
			if errLocal != nil {
				return fmt.Errorf("Intent request can't be completed due to: It's unsupported on broker and have unhandled error: %v", errLocal)
			} else {
				return fmt.Errorf("Broker can't fully handle request due to: %s", errMsg)
			}
		}
	} else if replyData == nil {
		return errors.New("Intent request's reply doesn't contain body. Can't process such reply")
	}

	// ok, trying to get reply data
	var replyInfo comm_app.JSONBrokerToIntentReply
	err = comm_app.FromStream(replyData, &replyInfo)
	if err != nil {
		return fmt.Errorf("Can't decode Intent request reply due to: %v", err)
	}

	// check if appID is not empty
	if reflect.DeepEqual(&replyInfo.AppID, &emptyUUID) {
		return errors.New("Intent request`s reply is invalid due to empty appID")
	}

	a.logger.Debugf("Intent request successfully registered on %s", BrokerInfo(sshRuntime))

	// update provided application data with appID
	sshRuntime.brokerAppID = replyInfo.AppID

	// set application consume negiated OK status
	// we never reset intent's status until connection closed
	sshRuntime.appState.StatusSet(true)

	return nil
}

func (a *applicationItemData) registerApplicationNegotiate(sshRuntime *sshConnectionRuntime) error {
	// create provide request to broker
	registerRequest := &comm_app.JSONRegisterRequest{
		Name: a.appName,
		Info: a.appRegInfo,
		AuthInfo: comm_app.ApplicationActionRequestAuthInfo{
			AuthType:    comm_app.TrustUserAuthType,
			AccessToken: a.accessToken,
		},
	}

	a.logger.Debugf("Do Register request to %s", BrokerInfo(sshRuntime))

	// encode request into JSON
	registerRequestJSON, err := registerRequest.ToStream()
	if err != nil {
		return fmt.Errorf("Register request can't be encoded due to: %v", err)
	}

	// send request and get appID on success
	isSupported, replyData, err := sshRuntime.sshConn.SendRequest(comm_app.RegisterSSHRequestType, true, registerRequestJSON)
	if err != nil {
		return fmt.Errorf("Register request can't be completed due to: %v", err)
	} else if !isSupported {
		if replyData == nil {
			return errors.New("Register request is Unsupported by broker")
		} else {
			errMsg, errLocal := comm_app.ErrorFromStream(replyData)
			if errLocal != nil {
				return fmt.Errorf("Register request can't be completed due to: It's unsupported on Broker and have unhandled error: %v", errLocal)
			} else {
				return fmt.Errorf("Broker can't fully handle request due to: %s", errMsg)
			}
		}
	}

	// ok, trying to get reply data
	var replyID comm_app.JSONBrokerToRegisterRequestReply
	err = comm_app.FromStream(replyData, &replyID)
	if err != nil {
		return fmt.Errorf("Can't decode register request reply due to: %v", err)
	}

	// check if appID is not empty
	if reflect.DeepEqual(&replyID.AppID, &emptyUUID) {
		return errors.New("Register request`s reply is invalid due to: empty appID")
	}

	a.logger.Debugf("Register request successfully handled by: %s", BrokerInfo(sshRuntime))

	// update provided application data with appID
	sshRuntime.brokerAppID = replyID.AppID

	// set provide application status to negotiated OK
	sshRuntime.appState.StatusSet(true)

	return nil
}

// method implements new channel open request processing for provided application
func (a *applicationItemData) provideApplication(newChannelRequest ssh.NewChannel, sshRuntime *sshConnectionRuntime) error {
	a.logger.Debugf("Registered application received new channel request with data: %s, from %s", string(newChannelRequest.ExtraData()), BrokerInfo(sshRuntime))

	// first of all check if current provider already successfully negiated with broker
	if !sshRuntime.appState.IsStatusOK() {
		a.logger.Errorf("Registered application credentials isn't negotiated with %s yet", BrokerInfo(sshRuntime))
		return errors.New("Registered application credentials isn't negotiated with broker yet")
	}

	// extract request raw data
	chanPayload := newChannelRequest.ExtraData()

	var provideRequest comm_app.JSONBrokerToRegisterRequestReply
	err := comm_app.FromStream(chanPayload, &provideRequest)
	if err != nil {
		a.logger.Errorf("Can't process consume reuest from %s due to: %v", BrokerInfo(sshRuntime), err)
		return errors.New("Invalid application consume request message format")
	}

	// check if appID is OK and accept channel request
	if !reflect.DeepEqual(&provideRequest.AppID, &sshRuntime.brokerAppID) {
		a.logger.Errorf("Can't process consume reuest from %s due to: appID mismatched", BrokerInfo(sshRuntime))
		return errors.New("Consume request is invalid due to: appID mismatched")
	}

	// all ok, accept request
	channel, request, err := newChannelRequest.Accept()
	if err != nil {
		a.logger.Errorf("Can't accept chanel open consume request from %s due to: %v", BrokerInfo(sshRuntime), err)
		return fmt.Errorf("Can't accept chanel open consume request due to: %v", err)
	}

	var targetConn net.Conn
	// we don't connect to registered application if request received with MovedConnection flag
	// NOTE: move connection mode used for cases when connection gonna be moved from another broker, so
	// connections to remote applications already established
	if !provideRequest.MovedConnection {
		// do local connection to provided application
		targetConn, err = net.DialTimeout("tcp", a.connectTo, providedApplicationConnectionDefaultTimeout)
		if err != nil {
			// close opened channel on local connection error
			channel.Close()
			a.logger.Errorf("Can't connect to locally registered application due to: %v by request from: %s", err, BrokerInfo(sshRuntime))
			return fmt.Errorf("Can't connect to locally registered application due to: %v", err)
		}
	}

	// create paired connection
	_, err = CreatePairedConnection(a, sshRuntime, targetConn, channel, request)
	if err != nil {
		// close paired connection
		channel.Close()
		if targetConn != nil {
			targetConn.Close()
		}
		a.logger.Errorf("Can't create paired connection from %s due to: %v", BrokerInfo(sshRuntime), err)
		return fmt.Errorf("Can't connect to locally registered application due to: %v", err)
	}

	return nil
}

func (a *applicationItemData) intentAppListen() {
	var err error

MainLoop:
	for {
		// start consumer`s local listener
		a.listener, err = net.Listen("tcp", a.listenAt)
		if err != nil {
			a.logger.Errorf("Can't start local listener at %s due to: %v. Next try to listen after 15 sec", a.listenAt, err)

			time.Sleep(listenFailTimeout)
			continue MainLoop
		}
		defer a.listener.Close()

		a.logger.Debugf("Started local listener at %s", a.listenAt)

	LocalLoop:
		for {
			// accept incoming connections and forward them
			conn, err := a.listener.Accept()
			if err != nil {
				a.logger.Errorf("Local consume connection accept error: %v. Rerun listener", err)
				break LocalLoop
			}

			// trying to forward accepted connection
			err = a.forwardLocalConnection(conn)
			if err != nil {
				a.logger.Errorf("Can't forward local consume connection due to: %v", err)
				conn.Close()
			}
		}
		// after error on Accept stage we needs to close listener
		// and wait some time to get OS unbind used resources
		a.listener.Close()

		// check if application stopped, then exit too
		if a.applicationStopped.IsStatusOK() {
			return
		}

		time.Sleep(listenFailTimeout)
	}
}

func (a *applicationItemData) requestConsumeChannel(sshRuntime *sshConnectionRuntime, connectorID uuid.UUID) (ssh.Channel, <-chan *ssh.Request, error) {
	if sshRuntime == nil {
		return nil, nil, errors.New("Can't process with Empty inout SSH runtime")
	}

	if !sshRuntime.appState.IsStatusOK() {
		return nil, nil, fmt.Errorf("Requested SSH connector doesn't complete negotiation intent application data with %s yet", BrokerInfo(sshRuntime))
	}

	if !sshRuntime.sshConnStatus.IsStatusOK() {
		return nil, nil, fmt.Errorf("SSH connection to %s marked as Closed", BrokerInfo(sshRuntime))
	}

	// create consume request and trying to open new channel on SSH connection
	consumeRequestJSON, err := (&comm_app.JSONConsumeApplicationRequest{
		AppID:       sshRuntime.brokerAppID,
		ConnectorID: connectorID,
	}).ToStream()
	if err != nil {
		return nil, nil, fmt.Errorf("Can't encode new channel request for SSH connection due to: %v", err)
	}

	channel, request, err := sshRuntime.sshConn.OpenChannel(comm_app.ConsumeSSHChannelType, consumeRequestJSON)
	if err != nil {
		errSsh, ok := err.(*ssh.OpenChannelError)
		if ok {
			errMsg, errLoc := comm_app.ErrorFromStream([]byte(errSsh.Message))
			if errLoc != nil {
				return nil, nil, fmt.Errorf("Can't open new channel for SSH connection to %s due to: %v", BrokerInfo(sshRuntime), errLoc)
			} else {
				return nil, nil, fmt.Errorf("Can't open new channel for SSH connection to %s due to: %v", BrokerInfo(sshRuntime), errMsg)
			}
		} else {
			return nil, nil, fmt.Errorf("Can't open new channel for SSH connection to %s due to: %v", BrokerInfo(sshRuntime), err)
		}
	}

	return channel, request, nil
}

func (a *applicationItemData) forwardLocalConnection(locConn net.Conn) error {
	// At first step get successfully registered SSH connection, trying to open channel there
	// if it failed, mark that connection and get next connection and try again

	// get list of suitable SSH connections to process
	sshConnList := a.appSSHStorage.GetSuitableSSHConnectionsList()
	if len(sshConnList) == 0 {
		return errors.New("Can't find any suitable SSH connections to terminate accepted local consume connection")
	}

	// check if we have connect to broker
	var err error
	var sshRuntime *sshConnectionRuntime
	var channel ssh.Channel
	var request <-chan *ssh.Request

	for _, sshConnRuntimePTR := range sshConnList {
		// do new channel request
		channel, request, err = a.requestConsumeChannel(sshConnRuntimePTR, uuid.Nil)
		if err != nil {
			a.logger.Error(err)
			continue
		}

		// if we get here all ok, we may break
		// set current SSH connection
		sshRuntime = sshConnRuntimePTR

		break
	}

	if sshRuntime == nil || channel == nil || request == nil || locConn == nil {
		return fmt.Errorf("No one of existed SSH connections to Brokers isn't suitable to terminate requested local connection")
	}

	// create paired connection
	_, err = CreatePairedConnection(a, sshRuntime, locConn, channel, request)
	if err != nil {
		// close paired connection
		channel.Close()
		// NOTE: local connection must be closed on level from where this API was invoked
		return err
	}

	return nil
}
