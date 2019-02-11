package app

import (
	"fmt"
	"io"
	"sync"

	//	log "github.com/sirupsen/logrus"
	comm_app "gitlab.com/frozy.io/connector/common"
	"golang.org/x/crypto/ssh"
)

//******************************** SSH GLOBAL/CHANNEL REQUESTS *********************************

// process SSH connection global requests
func handleGlobalRequests(sshRuntime *sshConnectionRuntime, app *applicationItemData) {
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
	for req := range sshRuntime.sshConnRequests {
		switch req.Type {
		case comm_app.SSHKeepAliveMsgType:
			replyHandle(req, false, nil)
		default:
			app.logger.Warnf("Received unsupported request from %s: type %s, wantReply %t, payload %v", BrokerInfo(sshRuntime), req.Type, req.WantReply, req.Payload)
			// create transport container
			replyDataLoc, errLocal := comm_app.ErrorViaCommunicationContainer("Unsupported request")
			if errLocal != nil {
				app.logger.Errorf("Can't create communication error object for received unsupported request from %s due to: %v", BrokerInfo(sshRuntime), errLocal)
				replyHandle(req, true, nil)
			} else {
				replyHandle(req, true, replyDataLoc)
			}
		}
	}

	app.logger.Debug("Connection global requests handler closed")
}

// supports requests inside created channels
// we doesn't support that types of requests at all
func handleChannelRequests(pairConn *PairConnItem) {
	for req := range pairConn.sshNewRequests {
		pairConn.logger.Debugf("Got a request for paired connection: type %s, wantReply %t, payload %v. Reply with FALSE", req.Type, req.WantReply, req.Payload)

		if req.WantReply {
			req.Reply(false, nil)
		}
	}

	pairConn.logger.Debugf("Paired connection channel requests handler closed")
}

// ********************************  CHANNELS **********************************************
// process SSH channel creation requests
// this is main entry point for provide requests
func handleChannelsCreation(sshRuntime *sshConnectionRuntime, app *applicationItemData) {
	app.logger.Debugf("SSH connection channels creation processing handler opened on %s", BrokerInfo(sshRuntime))

	// wait until channel will be closed by connection shutdown and supply each request in new goroutine
	for newChannel := range sshRuntime.sshConnChannels {
		go handleNewChannel(newChannel, sshRuntime, app)
	}

	app.logger.Debugf("SSH connection channels creation processing handler closed on %s", BrokerInfo(sshRuntime))
}

// service SSH channel creation as separate process
func handleNewChannel(newChannel ssh.NewChannel, sshRuntime *sshConnectionRuntime, app *applicationItemData) {
	chType := newChannel.ChannelType()

	switch chType {
	case comm_app.ProvideSSHChannelType:
		// this commands is not for Intent application type
		if !app.appIntentType {
			err := app.provideApplication(newChannel, sshRuntime)
			if err != nil {
				// create transport container
				replyData, errLocal := comm_app.ErrorViaCommunicationContainer(err.Error())
				if errLocal != nil {
					app.logger.Errorf("Can't create communication error object for received provide channel creation request from %s due to: %v", BrokerInfo(sshRuntime), errLocal)
					newChannel.Reject(ssh.ConnectionFailed, "")
				} else {
					newChannel.Reject(ssh.ConnectionFailed, string(replyData))
				}
				// because something goes wrong, we doesn't reset provide negiate status until connection closed
			}
		} else {
			// create transport container
			replyData, errLocal := comm_app.ErrorViaCommunicationContainer("Internally unsupported payload struct type")
			if errLocal != nil {
				app.logger.Errorf("Can't create communication error object for received unsupported channel creation request from %s due to: %v", BrokerInfo(sshRuntime), errLocal)
				newChannel.Reject(ssh.ConnectionFailed, "")
			} else {
				newChannel.Reject(ssh.ConnectionFailed, string(replyData))
			}
		}
	default:
		app.logger.Warnf("Unsupported channel type %s detected in connection from %s", chType, BrokerInfo(sshRuntime))

		// create transport container
		replyData, errLocal := comm_app.ErrorViaCommunicationContainer(fmt.Sprintf("Unsupported channel type: %s detected", chType))
		if errLocal != nil {
			app.logger.Errorf("Can't create communication error object for received unsupported channel creation request from %s due to: %v", BrokerInfo(sshRuntime), errLocal)
			newChannel.Reject(ssh.ConnectionFailed, "")
		} else {
			newChannel.Reject(ssh.ConnectionFailed, string(replyData))
		}
	}

}

//************************** GENERIC DATA FORWARD **********************************************
func connectionForward(pcCtrl PairedConnectionClearIf, pairConn *PairConnItem) {
	// define helper connection type name handler
	connectionTypeName := func() string {
		if pairConn.intentConnection {
			return "Consume"
		} else {
			return "Provide"
		}
	}

	// create WaitGroup for CopyBufferIO
	wg := &sync.WaitGroup{}

	// Prepare teardown function
	close := func() {
		pairConn.localTcpConn.Close()
		pairConn.sshChannelIf.Close()

		// delete paired connection from SSH context
		err := pcCtrl.DeletePairConnection(pairConn)
		if err != nil {
			pairConn.logger.Errorf("Can't delete paired connection %d from registry", pairConn.connPairIDX)
		} else {
			pairConn.logger.Debugf("Paired connection %d successfully deleted from registry", pairConn.connPairIDX)
		}

		// waiting both IO buffer copy goroutines exited
		wg.Wait()
	}

	// this is optional processing, it may be skipped
	go handleChannelRequests(pairConn)

	// run channel buffers management
	var once sync.Once

	// downstream
	wg.Add(1)
	go func() {
		pairConn.logger.Debugf("Downstream %s connection part %s <- %s started",
			connectionTypeName(), pairConn.localTcpConn.RemoteAddr().String(), pairConn.localTcpConn.LocalAddr().String())

		written, err := io.Copy(pairConn.localTcpConn, pairConn.sshChannelIf)
		if err != nil {
			pairConn.logger.Debugf("Donstream %s connection part %s <- %s closed with status: %v. Total transmitted bytes: %d",
				connectionTypeName(), pairConn.localTcpConn.RemoteAddr().String(), pairConn.localTcpConn.LocalAddr().String(), err, written)
		} else {
			pairConn.logger.Debugf("Downstream %s connection part %s <- %s closed without errors. Total transmitted bytes: %d",
				connectionTypeName(), pairConn.localTcpConn.RemoteAddr().String(), pairConn.localTcpConn.LocalAddr().String(), written)
		}

		// we must not use DEFER here because wg.Wait() runs into close() func,
		// so one of defer() is never fire up because stack will be stocked in close()
		wg.Done()

		once.Do(close)
	}()

	// upstream
	wg.Add(1)
	go func() {
		pairConn.logger.Debugf("Upstream %s connection part %s -> %s started",
			connectionTypeName(), pairConn.localTcpConn.RemoteAddr().String(), pairConn.localTcpConn.LocalAddr().String())

		written, err := io.Copy(pairConn.sshChannelIf, pairConn.localTcpConn)
		if err != nil {
			pairConn.logger.Debugf("Upstream %s connection part %s -> %s closed with status: %v. Total transmitted bytes: %d",
				connectionTypeName(), pairConn.localTcpConn.RemoteAddr().String(), pairConn.localTcpConn.LocalAddr().String(), err, written)
		} else {
			pairConn.logger.Debugf("Upstream %s connection part %s -> %s closed without errors. Total transmitted bytes: %d",
				connectionTypeName(), pairConn.localTcpConn.RemoteAddr().String(), pairConn.localTcpConn.LocalAddr().String(), written)
		}

		// we must not use DEFER here because wg.Wait() runs into close() func,
		// so one of defer() is never fire up because stack will be stocked in close()
		wg.Done()

		once.Do(close)
	}()
}
