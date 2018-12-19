package app

import (
	"fmt"
	"io"
	"net"
	"sync"

	comm_app "gitlab.com/frozy.io/connector/common"
	"golang.org/x/crypto/ssh"
)

func createSSHConn(sshData *sshRuntimeData) error {
	var err error

	fmt.Printf("Connecting to broker at %s\n", sshData.brokerConnStr)

	// create network connection
	conn, err := net.DialTimeout("tcp", sshData.brokerConnStr, sshData.sshCfg.Timeout)
	if err != nil {
		return err
	}

	// create SSH client connection
	sshData.sshConn, sshData.sshConnChannels, sshData.sshConnRequests, err = ssh.NewClientConn(conn, sshData.brokerConnStr, sshData.sshCfg)
	if err != nil {
		return err
	}

	// now, trying to advertise ConnectorID from Broker
	err = sshData.ConnectorRegisterIf.RegisterConnectorID(sshData.sshConn)
	if err != nil {
		sshData.sshConn.Close()
		return err
	}

	// set connection status
	sshData.StatusSet(true)

	return nil
}

//******************************** SSH GLOBAL/CHANNEL REQUESTS *********************************

// process SSH connection global requests
func handleGlobalRequests(requests <-chan *ssh.Request, payload interface{}) {
	// define reply helper
	replyHandle := func(req *ssh.Request, isError bool, msg []byte) {
		if req == nil {
			return
		}

		if req.WantReply {
			req.Reply(!isError, msg)
		}
	}

	// wait global requests and processing it
	for req := range requests {
		switch req.Type {
		case comm_app.SSHKeepAliveMsgType:
			replyHandle(req, false, nil)
		default:
			fmt.Printf("Received unsupported request: type %s, wantReply %t, payload %v\n", req.Type, req.WantReply, req.Payload)
			// create transport container
			replyDataLoc, errLocal := comm_app.ErrorViaCommunicationContainer("Unsupported request")
			if errLocal != nil {
				fmt.Printf("Can't create communication error object for received unsupported request due to: %v\n", errLocal)
				replyHandle(req, true, nil)
			} else {
				replyHandle(req, true, replyDataLoc)
			}
		}
	}

	fmt.Printf("Connection global requests handler closed\n")
}

// supports requests inside created channels
// we doesn't support that types of requests at all
func handleChannelRequests(requests <-chan *ssh.Request) {
	for req := range requests {
		fmt.Printf("Got a request for connection: type %s, wantReply %t, payload %v. Reply with FALSE\n", req.Type, req.WantReply, req.Payload)

		if req.WantReply {
			req.Reply(false, nil)
		}
	}

	fmt.Printf("Connection channel requests handler closed\n")
}

// ********************************  CHANNELS **********************************************
// process SSH channel creation requests
// this is main entry point for provide requests
func handleChannelsCreation(newChannels <-chan ssh.NewChannel, payload interface{}) {
	// wait until channel will be closed by connection shutdown and supply each request in new goroutine
	for newChannel := range newChannels {
		go handleNewChannel(newChannel, payload)
	}

	fmt.Printf("SSH connection channels creation processing handler closed\n")
}

// service SSH channel creation as separate process
func handleNewChannel(newChannel ssh.NewChannel, payload interface{}) {
	chType := newChannel.ChannelType()

	switch chType {
	case comm_app.ProvideSSHChannelType:
		switch payload.(type) {
		case *registerAppData:
			// we doesn't full assert with OK status because this part of job already done with switch statement
			provAppDataPTR := payload.(*registerAppData)
			// ok, we have proper provider`s handler, do processing
			err := provAppDataPTR.provideApplication(newChannel)
			if err != nil {
				// create transport container
				replyData, errLocal := comm_app.ErrorViaCommunicationContainer(err.Error())
				if errLocal != nil {
					fmt.Printf("Can't create communication error object for received provide channel creation request due to: %v\n", errLocal)
					newChannel.Reject(ssh.ConnectionFailed, "")
				} else {
					newChannel.Reject(ssh.ConnectionFailed, string(replyData))
				}
				// because something goes wrong, we doesn't reset provide negiate status until connection closed
			}
		default:
			// create transport container
			replyData, errLocal := comm_app.ErrorViaCommunicationContainer("Internally unsupported payload struct type")
			if errLocal != nil {
				fmt.Printf("Can't create communication error object for received unsupported channel creation request due to: %v\n", errLocal)
				newChannel.Reject(ssh.ConnectionFailed, "")
			} else {
				newChannel.Reject(ssh.ConnectionFailed, string(replyData))
			}
		}
	default:
		fmt.Printf("Unsupported channel type: %s detected\n", chType)

		// create transport container
		replyData, errLocal := comm_app.ErrorViaCommunicationContainer(fmt.Sprintf("Unsupported channel type: %s detected", chType))
		if errLocal != nil {
			fmt.Printf("Can't create communication error object for received unsupported channel creation request due to: %v\n", errLocal)
			newChannel.Reject(ssh.ConnectionFailed, "")
		} else {
			newChannel.Reject(ssh.ConnectionFailed, string(replyData))
		}
	}

}

//************************** GENERIC DATA FORWARD **********************************************
func connectionForward(tcpConn net.Conn, sshChannel ssh.Channel, sshRequest <-chan *ssh.Request, isProvideConnection bool) {
	// define helper connection type name handler
	connectionTypeName := func() string {
		if isProvideConnection {
			return "Provide"
		} else {
			return "Consume"
		}
	}

	// create WaitGroup for CopyBufferIO
	wg := &sync.WaitGroup{}

	// Prepare teardown function
	close := func() {
		tcpConn.Close()
		sshChannel.Close()

		// waiting both IO buffer copy goroutines exited
		wg.Wait()
	}

	// this is optional processing, it may be skipped
	go handleChannelRequests(sshRequest)

	// run channel buffers management
	var once sync.Once

	// downstream
	wg.Add(1)
	go func() {
		fmt.Printf("Downstream %s connection part %s <- %s started\n",
			connectionTypeName(), tcpConn.RemoteAddr().String(), tcpConn.LocalAddr().String())

		written, err := io.Copy(tcpConn, sshChannel)
		if err != nil {
			fmt.Printf("Donstream %s connection part closed with status: %v. Total transmitted bytes: %d\n", connectionTypeName(), err, written)
		} else {
			fmt.Printf("Downstream %s connection part %s <- %s closed without errors. Total transmitted bytes: %d\n",
				connectionTypeName(), tcpConn.RemoteAddr().String(), tcpConn.LocalAddr().String(), written)
		}

		// we must not use DEFER here because wg.Wait() runs into close() func,
		// so one of defer() is never fire up because stack will be stocked in close()
		wg.Done()

		once.Do(close)
	}()

	// upstream
	wg.Add(1)
	go func() {
		fmt.Printf("Upstream %s connection part %s -> %s started\n",
			connectionTypeName(), tcpConn.RemoteAddr().String(), tcpConn.LocalAddr().String())

		written, err := io.Copy(sshChannel, tcpConn)
		if err != nil {
			fmt.Printf("Upstream %s connection closed with status: %v. Total transmitted bytes: %d\n", connectionTypeName(), err, written)
		} else {
			fmt.Printf("Upstream %s connection part %s -> %s closed without errors. Total transmitted bytes: %d\n",
				connectionTypeName(), tcpConn.RemoteAddr().String(), tcpConn.LocalAddr().String(), written)
		}

		// we must not use DEFER here because wg.Wait() runs into close() func,
		// so one of defer() is never fire up because stack will be stocked in close()
		wg.Done()

		once.Do(close)
	}()
}
