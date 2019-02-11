package app

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"time"

	comm_app "gitlab.com/frozy.io/connector/common"
	"golang.org/x/crypto/ssh"
)

// intent application main handler do general work for app process init
// and monitor/control their state
func (a *applicationItemData) run() {
	// add some pause to get time for first discovery iteration
	time.Sleep(time.Second)

	a.logger.Info("Application processing thread started")

	if a.appIntentType {
		// first step run local listener
		go a.intentAppListen()
	}

	// as second step run Broker connection control thread
MainLoop:
	for {
		var err error
		var brokerList []comm_app.BrokerInfoData
		var brokerDesireConnections int

		// at startup for Intent app trying to get application specific brokers list
		if a.appIntentType {
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
			a.logger.Debug("No available Brokers found. Wait some time and try again")
			time.Sleep(pollInterval)
			continue MainLoop
		}

		a.logger.Debugf("Brokers list to update: %+v", brokerList)

		// filter Broker list against Black listed ones
		brokerList = a.appSSHStorage.sshConnectorIf.FilterBlackListedBrokers(brokerList)

		a.logger.Debugf("Brokers list flitered: %+v", brokerList)

		// form highly desired Brokers list
		highDesiredBrokers := brokerList
		if brokerDesireConnections < len(brokerList) {
			highDesiredBrokers = brokerList[:brokerDesireConnections]
		}

		// now check established SSH connections, update them and connects to new Brokers
		newBrokersToConnect := a.appSSHStorage.UpdateExistedConnectionsWithNewBrokers(highDesiredBrokers)

		a.logger.Debugf("Brokers list to NEW SSH connections: %+v", newBrokersToConnect)

		// connect to new Brokers
		for _, brokerIt := range newBrokersToConnect {
			// because of we can have cases when AD/ALB returns many brokers with same name (TTL doesn't expired at read moment)
			// we must check that SSH connection on that broker doesn't already exists
			if a.appSSHStorage.CheckIfSSHConnectionExists(brokerIt.BrokerName) {
				continue
			}

			a.logger.Debugf("Trying to setup SSH connectionto to %s",
				fmt.Sprintf("Broker: %s (%s:%d)", brokerIt.BrokerName, brokerIt.BrokerIP.String(), brokerIt.BrokerPort))

			// connect to Broker
			sshRuntime, err := a.appSSHStorage.sshConnectorIf.ConnectToBroker(brokerIt)
			if err != nil {
				a.logger.Errorf("Can't connect to %s due to: %v, Broker will be placed into Blacklist for a while",
					fmt.Sprintf("Broker: %s (%s:%d)", brokerIt.BrokerName, brokerIt.BrokerIP.String(), brokerIt.BrokerPort), err)

				// add broker into BlackList
				err = a.appSSHStorage.sshConnectorIf.AddBrokerToBlackList(brokerIt)
				if err != nil {
					a.logger.Errorf("Can't add %s into Blacklist due to: %v",
						fmt.Sprintf("Broker: %s (%s:%s)", brokerIt.BrokerName, brokerIt.BrokerIP.String(), fmt.Sprintf("%d", brokerIt.BrokerPort)), err)
				}
				continue
			}

			// register new SSH connection
			err = a.appSSHStorage.RegisterSSHConnection(sshRuntime)
			if err != nil {
				a.logger.Errorf("Can't register SSH connection to %s due to: %v", BrokerInfo(sshRuntime), err)
			}

			a.logger.Debugf("SSH connection to %s successfully Registered", BrokerInfo(sshRuntime))

			// run control handler for new connection
			go a.applicationConnectionProcess(sshRuntime)
		}

		time.Sleep(pollInterval)
	}
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
			timeToConsumeChannel = time.After(pollInterval)

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
				a.logger.Errorf("Broker can't fully handle request due to: %s", errMsg)
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

	// do local connection to provided application
	targetConn, err := net.DialTimeout("tcp", a.connectTo, providedApplicationConnectionDefaultTimeout)
	if err != nil {
		// close opened channel on local connection error
		channel.Close()
		a.logger.Errorf("Can't connect to locally registered application due to: %v by request from: %s", err, BrokerInfo(sshRuntime))
		return fmt.Errorf("Can't connect to locally registered application due to: %v", err)
	}

	// here we must to register paired connection
	pairConn := &PairConnItem{
		logger:           a.logger,
		connPairIDX:      a.appSSHStorage.sshConnectorIf.GetNewConnectionIDX(),
		intentConnection: false,
		sshChannelIf:     channel,
		sshNewRequests:   request,
		localTcpConn:     targetConn,
	}

	err = sshRuntime.RegisterPairConnection(pairConn)
	if err != nil {
		// close opened channel and loacl connection
		channel.Close()
		targetConn.Close()
		a.logger.Errorf("Can't register paired connection requested from %s to locally provided application due to: %v", BrokerInfo(sshRuntime), err)
		return fmt.Errorf("Can't register paired connection to locally provided application due to %v", err)
	} else {
		a.logger.Debugf("Paired connection %d successfully registered", pairConn.connPairIDX)
	}

	// run forwarding
	go connectionForward(sshRuntime, pairConn)

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

			time.Sleep(pollInterval)
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
		time.Sleep(pollInterval)
	}
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
	var sshRuntime *sshConnectionRuntime
	var channel ssh.Channel
	var request <-chan *ssh.Request

	for _, sshConnRuntimePTR := range sshConnList {
		if !sshConnRuntimePTR.appState.IsStatusOK() {
			a.logger.Errorf("Can't forward accepted local consume connection to %s due to: Connector doesn't complete negotiation intent application data with broker yet",
				BrokerInfo(sshConnRuntimePTR))
			continue
		}

		if !sshConnRuntimePTR.sshConnStatus.IsStatusOK() {
			a.logger.Errorf("Can't forward accepted local consume connection to %s due to: SSH connection marked as Closed", BrokerInfo(sshConnRuntimePTR))
			continue
		}

		// create consume request and trying to open new channel on SSH connection
		consumeRequestJSON, err := (&comm_app.JSONConsumeApplicationRequest{
			AppID: sshConnRuntimePTR.brokerAppID,
		}).ToStream()
		if err != nil {
			a.logger.Errorf("Can't encode new channel request for accepted consum connection due to: %v", err)
			continue
		}

		channel, request, err = sshConnRuntimePTR.sshConn.OpenChannel(comm_app.ConsumeSSHChannelType, consumeRequestJSON)
		if err != nil {
			errSsh, ok := err.(*ssh.OpenChannelError)
			if ok {
				errMsg, errLoc := comm_app.ErrorFromStream([]byte(errSsh.Message))
				if errLoc != nil {
					a.logger.Errorf("Can't open new channel for accepted consume connection to %s due to: %v", BrokerInfo(sshConnRuntimePTR), errLoc)
					continue
				} else {
					a.logger.Errorf("Can't open new channel for accepted consume connection to %s due to: %v", BrokerInfo(sshConnRuntimePTR), errMsg)
					continue
				}
			} else {
				a.logger.Errorf("Can't open new channel for accepted consume connection to %s due to: %v", BrokerInfo(sshConnRuntimePTR), err)
				continue
			}
		}

		// if we get here all ok and we may break
		// set current SSH connection
		sshRuntime = sshConnRuntimePTR

		break
	}

	if sshRuntime == nil || channel == nil || request == nil || locConn == nil {
		return fmt.Errorf("No one of existed SSH connections to Brokers isn't suitable to terminate requested local connection")
	}

	// here we must to register paired connection
	pairConn := &PairConnItem{
		logger:           a.logger,
		connPairIDX:      a.appSSHStorage.sshConnectorIf.GetNewConnectionIDX(),
		intentConnection: true,
		sshChannelIf:     channel,
		sshNewRequests:   request,
		localTcpConn:     locConn,
	}

	err := sshRuntime.RegisterPairConnection(pairConn)
	if err != nil {
		// close opened channel
		channel.Close()
		return fmt.Errorf("Can't register paired connection from locally accepted consume connection due to: %v", err)
	} else {
		a.logger.Debugf("Paired connection %d successfully registered", pairConn.connPairIDX)
	}

	// run forwarding
	go connectionForward(sshRuntime, pairConn)

	return nil
}
