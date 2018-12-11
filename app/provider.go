package app

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

func listener(resource Endpoint, broker Endpoint, sshConfig *ssh.ClientConfig) (net.Listener, error) {
	dialer := net.Dialer{
		Timeout:   60 * time.Second,
		KeepAlive: 15 * time.Second,
	}

	tcpConn, err := dialer.Dial("tcp", broker.String())
	if err != nil {
		return nil, fmt.Errorf("SSH server dial error: %s", err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(tcpConn, broker.String(), sshConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH client connect error: %s", err)
	}

	payload := ssh.Marshal(
		&channelForwardMsg{
			addr: resource.Host,
			port: uint32(resource.Port),
		})

	ok, reply, err := sshConn.SendRequest("tcpip-forward", true, payload)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("SSH tcpip-forward request denied by server. Reply payload %#v", reply)
	}

	client := newClient(sshConn, chans, reqs)
	ch := client.registerForward(resource)
	return &frozyListener{resource, client, ch}, nil
}

type frozyListener struct {
	addr Endpoint
	conn *client
	in   <-chan forward
}

// Accept waits for and returns the next connection to the listener.
func (l *frozyListener) Accept() (net.Conn, error) {
	s, ok := <-l.in
	if !ok {
		return nil, io.EOF
	}
	ch, incoming, err := s.newCh.Accept()
	if err != nil {
		return nil, err
	}
	go ssh.DiscardRequests(incoming)

	return &chanConn{
		Channel: ch,
		laddr:   l.addr,
		raddr:   s.raddr,
	}, nil
}

// Close closes the listener.
func (l *frozyListener) Close() error {
	m := channelForwardMsg{
		l.addr.String(),
		uint32(l.addr.Port),
	}

	close(l.conn.fwd.channel)

	ok, _, err := l.conn.SendRequest("cancel-tcpip-forward", true, ssh.Marshal(&m))
	if err == nil && !ok {
		err = errors.New("ssh: cancel-tcpip-forward failed")
	}
	return err
}

// Addr returns the listener's network address.
func (l *frozyListener) Addr() net.Addr {
	return l.addr
}

// forwardChan represents an established mapping of a laddr on a
// remote ssh server to a channel connected to a tcpListener.
type forwardChan struct {
	laddr   net.Addr
	channel chan forward
}

// forward represents an incoming forwarded tcpip connection. The
// arguments to add/remove/lookup should be address as specified in
// the original forward-request.
type forward struct {
	newCh ssh.NewChannel // the ssh client channel underlying this forward
	raddr net.Addr       // the raddr of the incoming connection
}

// RFC 4254 7.1
type channelForwardMsg struct {
	addr string
	port uint32
}

// RFC 4254, section 7.2
type forwardedTCPPayload struct {
	Addr       string
	Port       uint32
	OriginAddr string
	OriginPort uint32
}

// chanConn fulfills the net.Conn interface without
// the tcpChan having to hold laddr or raddr directly.
type chanConn struct {
	ssh.Channel
	laddr, raddr net.Addr
}

func (t *chanConn) LocalAddr() net.Addr {
	return t.laddr
}

func (t *chanConn) RemoteAddr() net.Addr {
	return t.raddr
}

func (t *chanConn) SetDeadline(deadline time.Time) error {
	if err := t.SetReadDeadline(deadline); err != nil {
		return err
	}
	return t.SetWriteDeadline(deadline)
}

func (t *chanConn) SetReadDeadline(deadline time.Time) error {
	return errors.New("ssh: chanConn: deadline not supported")
}

func (t *chanConn) SetWriteDeadline(deadline time.Time) error {
	return errors.New("ssh: chanConn: deadline not supported")
}
