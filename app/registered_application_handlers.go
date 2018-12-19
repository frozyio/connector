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

// provide application main handler do general work for provide app process init
// and monitor/control their state
func (p *registerAppData) run() {
	fmt.Printf("Started thread for register application: %s\n", p.appName.ShortAppName())

	// first of all do Broker connection
MainLoop:
	for {
		err := createSSHConn(&p.sshData)
		if err != nil {
			fmt.Printf("Can't connect to broker due to %s, next try after some idle period\n", err)
			time.Sleep(pollInterval)
			continue MainLoop
		}

		// run channel handlers
		go handleChannelsCreation(p.sshData.sshConnChannels, p)
		go handleGlobalRequests(p.sshData.sshConnRequests, p)
		go p.providerPoolHandler()

		// stay here until SSH connection is OK
		err = p.sshData.sshConn.Wait()
		if err != nil {
			fmt.Printf("Provide connection to broker dropped with cause: (%v), next try after some idle period\n", err)
		}

		// stop event handler
		p.poolerControlChannel <- true

		// reset SSH connection status and negotiated status too
		p.sshData.StatusSet(false)
		p.StatusSet(false)

		time.Sleep(pollInterval)
	}
}

func (p *registerAppData) providerPoolHandler() {
	// we will periodically try to provide our application if status of connection is bad
	// first iteration will run quickly
	timeToProvideChannel := time.After(time.Millisecond)

	for {
		select {
		case <-p.poolerControlChannel:
			// exit signal received
			fmt.Printf("Provider`s connection event pooler exited by signal\n")
			return

		case <-timeToProvideChannel:
			if !p.IsStatusOK() {
				err := p.registerApplicationNegotiate()
				if err != nil {
					fmt.Printf("Can't complete provided application negotiation process due to: (%v), next try after some idle period\n", err)
				}
			}
			// set new timeout
			timeToProvideChannel = time.After(pollInterval)

			// some new event`s channels here
		}
	}
}

func (p *registerAppData) registerApplicationNegotiate() error {
	// create provide request to broker
	registerRequest := &comm_app.JSONRegisterRequest{
		Name: p.appName,
		Info: p.appInfo,
		AuthInfo: comm_app.ApplicationActionRequestAuthInfo{
			AuthType:    comm_app.TrustUserAuthType,
			AccessToken: p.accessToken,
		},
	}

	fmt.Printf("Do register request with data: %+v\n", *registerRequest)

	// encode request into JSON
	registerRequestJSON, err := registerRequest.ToStream()
	if err != nil {
		return fmt.Errorf("Register request can't be encoded due to: (%v)", err)
	}

	// send request and get appID on success
	isSupported, replyData, err := p.sshData.sshConn.SendRequest(comm_app.RegisterSSHRequestType, true, registerRequestJSON)
	if !isSupported {
		if replyData == nil {
			return errors.New("Register request is Unsupported on broker")
		} else {
			errMsg, errLocal := comm_app.ErrorFromStream(replyData)
			if errLocal != nil {
				return fmt.Errorf("Register request can't be completed due to it's unsupported on broker and have unhandled error: (%v)", errLocal)
			} else {
				return fmt.Errorf("Broker can't fully handle register request due to: %s\n", errMsg)
			}
		}
	} else if err != nil {
		return fmt.Errorf("Provide request can't be completed due to: (%v)", err)
	}

	// ok, trying to get reply data
	var replyID comm_app.JSONBrokerToRegisterRequestReply
	err = comm_app.FromStream(replyData, &replyID)
	if err != nil {
		return fmt.Errorf("Can't decode register request reply due to: (%v)", err)
	}

	fmt.Printf("Received reply (%+v) for register request: %+v\n", replyID, *registerRequest)

	// check if appID is not empty
	if reflect.DeepEqual(&replyID.AppID, &emptyUUID) {
		return errors.New("Register request`s reply is invalid due to empty appID")
	}

	// update provided application data with appID
	p.appID = replyID.AppID

	// set provide application status to negotiated OK
	p.StatusSet(true)

	return nil
}

// method implements new channel open request processing for provided application
func (p *registerAppData) provideApplication(newChannelRequest ssh.NewChannel) error {
	fmt.Printf("Provide channel request for application (%s) received with data: %s\n",
		p.appName.ShortAppName(), string(newChannelRequest.ExtraData()))

	// first of all check if current provider already successfully negiated with broker
	if !p.IsStatusOK() {
		fmt.Printf("Can't process provide request due to: Provided application info isn't negotiated with broker yet")
		return errors.New("Provided application info isn't negotiated with broker yet")
	}

	// extract request raw data
	chanPayload := newChannelRequest.ExtraData()

	var provideRequest comm_app.JSONBrokerToRegisterRequestReply
	err := comm_app.FromStream(chanPayload, &provideRequest)
	if err != nil {
		fmt.Printf("Can't decode Register request message due to: %v. Message: %v\n", err, string(chanPayload))
		return errors.New("Invalid Register request message")
	}

	// check if appID is OK and accept channel request
	if !reflect.DeepEqual(&provideRequest.AppID, &p.appID) {
		return errors.New("Consume request for provided application is invalid due to appID mismatch")
	}

	// all ok, accept request
	channel, request, err := newChannelRequest.Accept()
	if err != nil {
		return fmt.Errorf("Can't accept Chanel open provide request due to (%v)", err)
	}

	// and initiate local connection and forwarding
	err = p.forward(channel, request)
	if err != nil {
		// close opened channel on local connection error
		channel.Close()
		return fmt.Errorf("Can't forward open channel`s data to localy provided application due to (%v). Channel closed", err)
	}

	return nil
}

func (p *registerAppData) forward(ch ssh.Channel, req <-chan *ssh.Request) error {
	// do local connection to provided application
	targetConn, err := net.DialTimeout("tcp", p.connectTo, providedApplicationConnectionDefaultTimeout)
	if err != nil {
		return fmt.Errorf("Error on connection to provided application: (%v)", err)
	}

	// run forwarding
	go connectionForward(targetConn, ch, req, true)

	return nil
}
