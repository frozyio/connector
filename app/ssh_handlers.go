package app

import (
	"fmt"

	comm_app "gitlab.com/frozy.io/connector/common"
	"golang.org/x/crypto/ssh"
)

//******************************** SSH GLOBAL REQUESTS/CHANNEL HANDLING *********************************

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
		case comm_app.RegisterSSHRejectType:
			app.logger.Warnf("Received registered application reject request from %s: wantReply %t", BrokerInfo(sshRuntime), req.WantReply)
			err := app.ApplicationRejectHandler(sshRuntime, req.Payload, true)
			if err != nil {
				// create transport container
				replyDataLoc, errLocal := comm_app.ErrorViaCommunicationContainer(err.Error())
				if errLocal != nil {
					app.logger.Errorf("Can't create communication error object for received registered application reject request from %s due to: %v", BrokerInfo(sshRuntime), errLocal)
					replyHandle(req, true, nil)
				} else {
					replyHandle(req, true, replyDataLoc)
				}
			}
			// answer with OK status
			replyHandle(req, false, nil)
		case comm_app.IntentSSHRejectType:
			app.logger.Warnf("Received Intent application reject request from %s: wantReply %t", BrokerInfo(sshRuntime), req.WantReply)
			err := app.ApplicationRejectHandler(sshRuntime, req.Payload, false)
			if err != nil {
				// create transport container
				replyDataLoc, errLocal := comm_app.ErrorViaCommunicationContainer(err.Error())
				if errLocal != nil {
					app.logger.Errorf("Can't create communication error object for received Intent application reject request from %s due to: %v", BrokerInfo(sshRuntime), errLocal)
					replyHandle(req, true, nil)
				} else {
					replyHandle(req, true, replyDataLoc)
				}
			}
			// answer with OK status
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
