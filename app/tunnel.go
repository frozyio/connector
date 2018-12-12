package app

import (
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

const pollInterval = 15 * time.Second

func runConsumer(conf *Config) error {
	sshConfig, err := conf.sshClientConfig()
	if err != nil {
		return err
	}

	for {
		serverConn, err := ssh.Dial("tcp", conf.Init.Broker.String(), sshConfig)
		if err != nil {
			fmt.Printf("SSH server dial error: %s\n", err)
			return err
		}
		defer serverConn.Close()

		listener, err := net.Listen("tcp", conf.Connector.Addr.String())
		if err != nil {
			fmt.Printf("Local listen error %s\n", err.Error())
			return err
		}
		defer listener.Close()

		fmt.Printf("Listening at %s to consume resource %s (brocker at %s)\n",
			conf.Connector.Addr.String(), conf.remoteResourse().String(), conf.Init.Broker.String())

		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("Local accept error %s\n", err.Error())
				break
			}
			go conf.consumerForward(conn, serverConn)
		}

		time.Sleep(pollInterval)
	}
}

func (conf Config) consumerForward(localConn net.Conn, sshClient *ssh.Client) {
	remoteConn, err := sshClient.Dial("tcp", conf.remoteResourse().String())
	if err != nil {
		fmt.Printf("SSH remote dial error: %s\n", err)
		return
	}
	defer remoteConn.Close()

	connectionForward(remoteConn, localConn)
}

func runProvider(conf *Config) error {
	sshConfig, err := conf.sshClientConfig()
	if err != nil {
		return err
	}

	for {
		listener, err := listener(conf.remoteResourse(), conf.Init.Broker, sshConfig)
		if err != nil {
			fmt.Printf("Remote SSH listen error: %s\n", err.Error())
			return err
		}
		defer listener.Close()

		fmt.Printf("Providing resource %s from %s (brocker at %s)\n",
			conf.remoteResourse().String(), conf.Connector.Addr.String(), conf.Init.Broker.String())

		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("Remote accept error: %s\n", err.Error())
				break
			}
			defer conn.Close()
			go conf.providerForward(conn)
		}

		time.Sleep(pollInterval)
	}
}

func (conf Config) providerForward(conn net.Conn) {
	target, err := net.Dial("tcp", conf.Connector.Addr.String())
	if err != nil {
		fmt.Printf("Dial into target service error: %s\n", err.Error())
		return
	}
	defer target.Close()
	connectionForward(conn, target)
}

func connectionForward(remoteConn net.Conn, targetConn net.Conn) {
	done := make(chan bool)
	copyConn := func(writer io.Writer, reader io.Reader) {
		_, err := io.Copy(writer, reader)
		if err != nil {
			fmt.Printf("Connection forwarding finished with: %s\n", err.Error())
		}
		done <- true
	}

	go copyConn(targetConn, remoteConn)
	go copyConn(remoteConn, targetConn)
	<-done
}

func (conf Config) sshClientConfig() (*ssh.ClientConfig, error) {
	signer, err := ssh.NewSignerFromKey(conf.RsaKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to build SSH auth method (%s)", err.Error())
	}

	return &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}, nil
}
