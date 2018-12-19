package app

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"gitlab.com/frozy.io/connector/config"
	ssh_custom "gitlab.com/frozy.io/connector/ssh"
	"golang.org/x/crypto/ssh"
)

type brokerClient struct {
	*ssh.Client
	cfg   *ssh.ClientConfig
	addr  config.Endpoint
	mutex *sync.Mutex
}

func (c *brokerClient) keepConnected() {
	for {
		var err error
		fmt.Println("SSH connecting to broker at", c.addr.String())
		c.mutex.Lock()
		c.Client, err = ssh.Dial("tcp", c.addr.String(), c.cfg)
		c.mutex.Unlock()
		if err != nil {
			fmt.Printf("SSH server dial error: %s\n", err)
		} else {
			fmt.Println("SSH connected to broker")
			err = c.Wait()
			fmt.Printf("SSH connection finished with: %s\n", err)
		}
		time.Sleep(config.ReconnectTimeout)
	}
}

func (c *Connector) runConsumer() error {
	sshConfig, err := c.sshClientConfig()
	if err != nil {
		return err
	}

	broker := brokerClient{cfg: sshConfig, addr: c.Config.Frozy.BrokerAddr(), mutex: &sync.Mutex{}}
	go broker.keepConnected()

	listener, err := net.Listen("tcp", c.Config.Connect.Addr.String())
	if err != nil {
		fmt.Printf("Local listen at %s error %s\n", c.Config.Connect.Addr.String(), err.Error())
		return err
	}
	defer listener.Close()

	fmt.Printf("Listening at %s to consume resource %s\n",
		c.Config.Connect.Addr.String(), c.Config.Connect.RemoteResourse().String())

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Local accept error %s\n", err.Error())
			return err
		}

		broker.mutex.Lock()
		if broker.Client == nil {
			fmt.Println("SSH connection not ready")
			conn.Close()
		} else if remote, err := broker.Dial("tcp", c.Config.Connect.RemoteResourse().String()); err != nil {
			fmt.Printf("SSH remote dial error: %s\n", err)
			conn.Close()
		} else {
			go connectionForward(remote, conn)
		}
		broker.mutex.Unlock()
	}
}

func (c *Connector) runProvider() error {
	sshConfig, err := c.sshClientConfig()
	if err != nil {
		return err
	}

	for {
		fmt.Println("Connecting to broker at", c.Config.Frozy.BrokerAddr().String())
		// listener, err := listener(c.Config.Connect.RemoteResourse(), c.Config.Frozy.BrokerAddr(), sshConfig)
		sshClient, err := ssh_custom.Dial(c.Config.Frozy.BrokerAddr().String(), sshConfig)
		if err != nil {
			fmt.Printf("SSH server dial error: %s\n", err)
			time.Sleep(config.ReconnectTimeout)
			continue
		}

		listener, err := sshClient.ListenTCPUnresolve(c.Config.Connect.RemoteResourse())
		if err != nil {
			fmt.Printf("Remote SSH listen error: %s\n", err.Error())
			time.Sleep(config.ReconnectTimeout)
			continue
		}
		defer listener.Close()

		fmt.Printf("Providing resource %s from %s\n",
			c.Config.Connect.RemoteResourse().String(), c.Config.Connect.Addr.String())

		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("Remote accept error: %s\n", err.Error())
				break
			}
			if target, err := net.Dial("tcp", c.Config.Connect.Addr.String()); err != nil {
				fmt.Printf("Dial into target service error: %s\n", err.Error())
				conn.Close()
				continue
			} else {
				go connectionForward(conn, target)
			}
		}

		time.Sleep(config.ReconnectTimeout)
	}
}

func connectionForward(remote net.Conn, target net.Conn) {
	doneR2T := make(chan error)
	doneT2R := make(chan error)

	go func() {
		_, err := io.Copy(remote, target)
		doneT2R <- err
	}()

	go func() {
		_, err := io.Copy(target, remote)
		doneR2T <- err
	}()

	select {
	case <-doneR2T:
		target.Close()
		remote.Close()
		<-doneT2R
	case <-doneT2R:
		target.Close()
		remote.Close()
		<-doneR2T
	}
}

func (c Connector) sshClientConfig() (*ssh.ClientConfig, error) {
	signer, err := ssh.NewSignerFromKey(c.RsaKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to build SSH auth method (%s)", err.Error())
	}

	return &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}, nil
}
