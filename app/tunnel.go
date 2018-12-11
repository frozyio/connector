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

	fmt.Printf("Listening at %s to consume resource %s (brocker at %s)\n",
		conf.Connector.Addr.String(), conf.remoteResourse().String(), conf.Init.Broker.String())

	for {
		listener, err := net.Listen("tcp", conf.Connector.Addr.String())
		if err != nil {
			fmt.Printf("Local listen error %s\n", err.Error())
			return err
		}
		defer listener.Close()

		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("Local accept error %s\n", err.Error())
				break
			}
			go conf.consumerForward(conn, sshConfig)
		}

		time.Sleep(pollInterval)
	}
}

func (conf Config) consumerForward(localConn net.Conn, sshConfig *ssh.ClientConfig) {
	serverConn, err := ssh.Dial("tcp", conf.Init.Broker.String(), sshConfig)
	if err != nil {
		fmt.Printf("SSH server dial error: %s\n", err)
		return
	}

	remoteConn, err := serverConn.Dial("tcp", conf.remoteResourse().String())
	if err != nil {
		fmt.Printf("SSH remote dial error: %s\n", err)
		return
	}
	defer remoteConn.Close()

	done := make(chan bool)
	copyConn := func(writer, reader net.Conn) {
		_, err := io.Copy(writer, reader)
		if err != nil {
			fmt.Printf("io.Copy error: %s\n", err)
		}
		done <- true
	}

	go copyConn(localConn, remoteConn)
	go copyConn(remoteConn, localConn)
	<-done
}

func runProvider(conf *Config) error {
	sshConfig, err := conf.sshClientConfig()
	if err != nil {
		return err
	}

	fmt.Printf("Providing resource %s from %s (brocker at %s)\n",
		conf.remoteResourse().String(), conf.Connector.Addr.String(), conf.Init.Broker.String())

	for {
		listener, err := listener(conf.remoteResourse(), conf.Init.Broker, sshConfig)
		if err != nil {
			return fmt.Errorf("Remote SSH listen error: %s", err.Error())
		}
		defer listener.Close()

		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("Remote accept error: %s\n", err.Error())
				break
			}

			target, err := net.Dial("tcp", conf.Connector.Addr.String())
			if err != nil {
				fmt.Printf("Dial into target service error: %s\n", err.Error())
			}

			go conf.providerForward(conn, target)
		}

		time.Sleep(pollInterval)
	}
}

func (conf Config) providerForward(remoteConn net.Conn, targetConn net.Conn) {
	done := make(chan bool)
	copyConn := func(writer io.Writer, reader io.Reader) {
		_, err := io.Copy(writer, reader)
		if err != nil {
			fmt.Printf("io.Copy error: %s\n", err.Error())
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
