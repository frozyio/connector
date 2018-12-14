package app

import (
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

const pollInterval = 15 * time.Second

func (c *Connector) runConsumer() error {
	sshConfig, err := c.sshClientConfig()
	if err != nil {
		return err
	}

	for {
		fmt.Println("Connecting to broker at", c.Config.Frozy.BrokerAddr().String())
		serverConn, err := ssh.Dial("tcp", c.Config.Frozy.BrokerAddr().String(), sshConfig)
		if err != nil {
			fmt.Printf("SSH server dial error: %s\n", err)
			return err
		}
		defer serverConn.Close()

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
				break
			}
			go c.consumerForward(conn, serverConn)
		}

		time.Sleep(pollInterval)
	}
}

func (c Connector) consumerForward(localConn net.Conn, sshClient *ssh.Client) {
	remoteConn, err := sshClient.Dial("tcp", c.Config.Connect.RemoteResourse().String())
	if err != nil {
		fmt.Printf("SSH remote dial error: %s\n", err)
		return
	}
	defer remoteConn.Close()

	connectionForward(remoteConn, localConn)
}

func (c *Connector) runProvider() error {
	sshConfig, err := c.sshClientConfig()
	if err != nil {
		return err
	}

	for {
		fmt.Println("Connecting to broker at", c.Config.Frozy.BrokerAddr().String())
		listener, err := listener(c.Config.Connect.RemoteResourse(), c.Config.Frozy.BrokerAddr(), sshConfig)
		if err != nil {
			fmt.Printf("Remote SSH listen error: %s\n", err.Error())
			return err
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
			defer conn.Close()
			go c.providerForward(conn)
		}

		time.Sleep(pollInterval)
	}
}

func (c Connector) providerForward(conn net.Conn) {
	target, err := net.Dial("tcp", c.Config.Connect.Addr.String())
	if err != nil {
		fmt.Printf("Dial into target service error: %s\n", err.Error())
		return
	}
	defer target.Close()
	connectionForward(conn, target)
}

func connectionForward(remoteConn net.Conn, targetConn net.Conn) {
	done := make(chan error, 2)
	copyConn := func(writer io.Writer, reader io.Reader) {
		_, err := io.Copy(writer, reader)
		if err != nil {
			fmt.Printf("Connection forwarding finished with: %s\n", err.Error())
		}
		done <- err
	}

	go copyConn(targetConn, remoteConn)
	go copyConn(remoteConn, targetConn)
	<-done
	<-done
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
