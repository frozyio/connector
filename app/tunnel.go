package app

import (
	"fmt"
	"io"
	"net"
	"time"

	"gitlab.com/frozy.io/connector/config"
	"golang.org/x/crypto/ssh"
)

func (c *Connector) runConsumer() error {
	listener, err := net.Listen("tcp", c.Config.Connect.Addr.String())
	if err != nil {
		fmt.Printf("Local listen at %s error %s\n", c.Config.Connect.Addr.String(), err.Error())
		return err
	}
	defer listener.Close()

	for {
		if sshClient, err := c.connectConsumerToBroker(); err == nil {
			defer sshClient.Close()

			fmt.Printf("Listening at %s to consume resource %s\n",
				c.Config.Connect.Addr.String(), c.Config.Connect.RemoteResourse().String())

			for {
				conn, err := listener.Accept()
				if err != nil {
					fmt.Printf("Local accept error %s\n", err.Error())
					return err
				}

				if remoteConn, err := sshClient.Dial("tcp", c.Config.Connect.RemoteResourse().String()); err != nil {
					fmt.Printf("SSH remote dial error: %s\n", err)
					conn.Close()
					break
				} else {
					go connectionForward(remoteConn, conn)
				}
			}
		}

		time.Sleep(config.ReconnectTimeout)
	}
}

func (c *Connector) connectConsumerToBroker() (*ssh.Client, error) {
	config, err := c.sshClientConfig()
	if err != nil {
		return nil, err
	}

	fmt.Println("Connecting to broker at", c.Config.Frozy.BrokerAddr().String())
	client, err := ssh.Dial("tcp", c.Config.Frozy.BrokerAddr().String(), config)
	if err != nil {
		fmt.Printf("SSH server dial error: %s\n", err)
		return nil, err
	}
	return client, nil
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

func connectionForward(remoteConn net.Conn, targetConn net.Conn) {
	defer remoteConn.Close()
	defer targetConn.Close()

	copyConn := func(writer io.Writer, reader io.Reader) {
		_, err := io.Copy(writer, reader)
		if err != nil {
			fmt.Printf("Connection forwarding finished with: %s\n", err.Error())
		}
	}

	go copyConn(targetConn, remoteConn)
	copyConn(remoteConn, targetConn)
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
