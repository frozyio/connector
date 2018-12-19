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
func (c *intentAppData) run() {
	fmt.Printf("Started thread for intent application: src %s, dst: %s\n", c.sourceAppName.ShortAppName(), c.destinationAppName.ShortAppName())

	// first step run local listener
	go c.listen()

	// as second step do Broker connection
MainLoop:
	for {
		err := createSSHConn(&c.sshData)
		if err != nil {
			fmt.Printf("Can't connect to broker due to %s, next try after some idle period\n", err)
			time.Sleep(pollInterval)
			continue MainLoop
		}

		// run channel handlers
		go handleChannelsCreation(c.sshData.sshConnChannels, c)
		go handleGlobalRequests(c.sshData.sshConnRequests, c)
		go c.consumerPoolHandler()

		// stay here until SSH connection is OK
		err = c.sshData.sshConn.Wait()
		if err != nil {
			fmt.Printf("Consume connection to broker dropped with cause: (%v), next try after some idle period\n", err)
		}

		// stop event handler
		c.poolerControlChannel <- true

		// reset SSH connection status and negotiated status
		c.sshData.StatusSet(false)
		c.StatusSet(false)

		time.Sleep(pollInterval)
	}
}

func (c *intentAppData) consumerPoolHandler() {
	// we will periodically try to provide our application if status of connection is bad
	// arm first check for a short time interval
	timeToConsumeChannel := time.After(time.Millisecond)

	for {
		select {
		case <-c.poolerControlChannel:
			// exit signal received
			fmt.Printf("Consumer`s connection event pooler exited by signal\n")
			return

		case <-timeToConsumeChannel:
			if !c.IsStatusOK() {
				err := c.intentApplicationNegotiate()
				if err != nil {
					fmt.Printf("Can't complete intent application negotiation process due to: (%v), next try after some idle period\n", err)
				}
			}
			// set new timeout
			timeToConsumeChannel = time.After(pollInterval)

			// some new event`s channels here
		}
	}
}

func (c *intentAppData) intentApplicationNegotiate() error {
	// create intent request to broker
	intentRequest := &comm_app.JSONIntentRequest{
		SourceAppName:   c.sourceAppName,
		SourceAppInfo:   c.sourceAppInfo,
		DestinationName: c.destinationAppName,
		AuthInfo: comm_app.ApplicationActionRequestAuthInfo{
			AuthType:    comm_app.TrustUserAuthType,
			AccessToken: c.accessToken,
		},
	}

	fmt.Printf("Do intent request with data: %+v\n", *intentRequest)

	// encode request into JSON
	intentRequestJSON, err := intentRequest.ToStream()
	if err != nil {
		return fmt.Errorf("Intent request can't be encoded due to: (%v)", err)
	}

	// send request and get appID on success
	isSupported, replyData, err := c.sshData.sshConn.SendRequest(comm_app.IntentSSHRequestType, true, intentRequestJSON)
	if !isSupported {
		if replyData == nil {
			fmt.Printf("Intent`s destimnation application doesn't registered on broker yet, but intent request registered\n")
		} else {
			errMsg, errLocal := comm_app.ErrorFromStream(replyData)
			if errLocal != nil {
				return fmt.Errorf("Intent request can't be completed due to it's unsupported on broker and have unhandled error: (%v)", errLocal)
			} else {
				fmt.Printf("Broker can't fully handle intent request due to: %s\n", errMsg)
			}
		}
	} else if err != nil {
		return fmt.Errorf("Intent request can't be completed due to: (%v)", err)
	} else if replyData == nil {
		return errors.New("Intent request's reply doesn't contain body. Can't process such reply ")
	}

	// ok, trying to get reply data
	var replyInfo comm_app.JSONBrokerToIntentReply
	err = comm_app.FromStream(replyData, &replyInfo)
	if err != nil {
		return fmt.Errorf("Can't decode intent request reply due to: (%v)", err)
	}

	fmt.Printf("Received reply (%+v) for intent request: %+v\n", replyInfo, *intentRequest)

	// check if appID is not empty
	if reflect.DeepEqual(&replyInfo.AppID, &emptyUUID) {
		return errors.New("Intent request`s reply is invalid due to empty appID")
	}

	// update provided application data with appID
	c.destinationAppID = replyInfo.AppID

	// set application consume negiated OK status
	// we never reset intent's status until connection closed
	c.StatusSet(true)

	return nil
}

func (c *intentAppData) listen() {
	var err error

MainLoop:
	for {
		// start consumer`s local listener
		c.listener, err = net.Listen("tcp", c.listenAt)
		if err != nil {
			fmt.Printf("Local listener at %s failed due to %v. Next try to listen after 15 sec\n", c.listenAt, err)

			time.Sleep(pollInterval)
			continue MainLoop
		}
		defer c.listener.Close()

		fmt.Printf("Listening at %s to intent application %s\n", c.listenAt, c.destinationAppName.ShortAppName())

	LocalLoop:
		for {
			// accept incoming connections and forward them
			conn, err := c.listener.Accept()
			if err != nil {
				fmt.Printf("Local accept error %v. Rerun listener\n", err)
				break LocalLoop
			}

			// check if we have connect to broker
			if c.sshData.IsStatusOK() {
				// if application isn't negotiated with broker, do it now
				if !c.IsStatusOK() {
					// force consume negotiation request
					err := c.intentApplicationNegotiate()
					if err != nil {
						fmt.Printf("Can't complete intent application negotiation process due to: (%v)\n", err)
					}
				}

				if c.IsStatusOK() {
					// application consume negotiated with broker successfully, do forward
					err = c.forwardLocalConnection(conn)
					if err != nil {
						fmt.Printf("Can't forward local consume connection due to: (%v)\n", err)
						conn.Close()
					}
				} else {
					// if connector doesn't negotiated with broker yet, we drop connection
					fmt.Printf("Can't forward local consume connection due to: Connector doesn't complete negotiation intent application data with broker yet\n")
					conn.Close()
				}
			} else {
				// if connector doesn't have SSH connection to broker
				fmt.Printf("Can't forward local consume connection due to: No SSH connection to broker\n")
				conn.Close()
			}
		}
		// after error on Accept stage we needs to close listener
		// and wait some time to get OS unbind used resources
		c.listener.Close()
		time.Sleep(pollInterval)
	}
}

func (c *intentAppData) forwardLocalConnection(locConn net.Conn) error {
	// create consume request and trying to open new channel on SSH connection
	consumeRequestJSON, err := (&comm_app.JSONConsumeApplicationRequest{
		AppID: c.destinationAppID,
	}).ToStream()
	if err != nil {
		return fmt.Errorf("Can't encode intent request due to: (%v)", err)
	}

	ch, req, err := c.sshData.sshConn.OpenChannel(comm_app.ConsumeSSHChannelType, consumeRequestJSON)
	if err != nil {
		errSsh, ok := err.(*ssh.OpenChannelError)
		if ok {
			errMsg, errLoc := comm_app.ErrorFromStream([]byte(errSsh.Message))
			if errLoc != nil {
				return fmt.Errorf("Can't open intent channel to broker due to: (%v)", err)
			} else {
				return fmt.Errorf("Can't open intent channel to broker due to: (%v)", errMsg)
			}
		} else {
			return fmt.Errorf("Can't open intent channel to broker due to: (%v)", err)
		}
	}

	// run forwarding
	go connectionForward(locConn, ch, req, false)

	return nil
}
