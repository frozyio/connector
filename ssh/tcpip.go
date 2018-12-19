package ssh

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"gitlab.com/frozy.io/connector/config"
	"golang.org/x/crypto/ssh"
)

// RFC 4254 7.1
type channelForwardMsg struct {
	addr  string
	rport uint32
}

// handleForwards starts goroutines handling forwarded connections.
// It's called on first use by (*Client).ListenTCP to not launch
// goroutines until needed.
func (c *Client) handleForwards() {
	go c.forwards.handleChannels(c.HandleChannelOpen("forwarded-tcpip"))
}

// ListenTCPUnresolve requests the remote peer open a listening socket
// on laddr. Incoming connections will be available by calling
// Accept on the returned net.Listener.
func (c *Client) ListenTCPUnresolve(laddr config.Endpoint) (net.Listener, error) {
	c.handleForwardsOnce.Do(c.handleForwards)

	m := channelForwardMsg{
		laddr.Host,
		uint32(laddr.Port),
	}

	// send message
	ok, _, err := c.SendRequest("tcpip-forward", true, ssh.Marshal(&m))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("ssh: tcpip-forward request denied by peer")
	}

	// Register this forward, using the port number we obtained.
	ch := c.forwards.add(laddr)

	return &tcpUnresolveListener{&laddr, c, ch}, nil
}

// forwardList stores a mapping between remote
// forward requests and the tcpListeners.
type forwardList struct {
	sync.Mutex
	entries []forwardEntry
}

// forwardEntry represents an established mapping of a laddr on a
// remote ssh server to a channel connected to a tcpListener.
type forwardEntry struct {
	laddr net.Addr
	c     chan forward
}

// forward represents an incoming forwarded tcpip connection. The
// arguments to add/remove/lookup should be address as specified in
// the original forward-request.
type forward struct {
	newCh ssh.NewChannel // the ssh client channel underlying this forward
	raddr net.Addr       // the raddr of the incoming connection
}

func (l *forwardList) add(addr net.Addr) chan forward {
	l.Lock()
	defer l.Unlock()
	f := forwardEntry{
		laddr: addr,
		c:     make(chan forward, 1),
	}
	l.entries = append(l.entries, f)
	return f.c
}

// See RFC 4254, section 7.2
type forwardedTCPPayload struct {
	Addr       string
	Port       uint32
	OriginAddr string
	OriginPort uint32
}

func (l *forwardList) handleChannels(in <-chan ssh.NewChannel) {
	for ch := range in {
		var (
			laddr net.Addr
			raddr net.Addr
			err   error
		)
		switch channelType := ch.ChannelType(); channelType {
		case "forwarded-tcpip":
			var payload forwardedTCPPayload
			if err = ssh.Unmarshal(ch.ExtraData(), &payload); err != nil {
				ch.Reject(ssh.ConnectionFailed, "could not parse forwarded-tcpip payload: "+err.Error())
				continue
			}

			laddr = config.Endpoint{Host: payload.Addr, Port: uint16(payload.Port)}
			raddr = config.Endpoint{Host: payload.OriginAddr, Port: uint16(payload.OriginPort)}
		default:
			fmt.Printf("ssh: unknown channel type %s", channelType)
			ch.Reject(ssh.ConnectionFailed, "unknown channel type "+channelType)
			continue
		}
		if ok := l.forward(laddr, raddr, ch); !ok {
			// Section 7.2, implementations MUST reject spurious incoming
			// connections.
			fmt.Println("No forward for address")
			ch.Reject(ssh.Prohibited, "no forward for address")
			continue
		}
	}
}

// remove removes the forward entry, and the channel feeding its
// listener.
func (l *forwardList) remove(addr net.Addr) {
	l.Lock()
	defer l.Unlock()
	for i, f := range l.entries {
		if addr.Network() == f.laddr.Network() && addr.String() == f.laddr.String() {
			l.entries = append(l.entries[:i], l.entries[i+1:]...)
			close(f.c)
			return
		}
	}
}

// closeAll closes and clears all forwards.
func (l *forwardList) closeAll() {
	l.Lock()
	defer l.Unlock()
	for _, f := range l.entries {
		close(f.c)
	}
	l.entries = nil
}

func (l *forwardList) forward(laddr, raddr net.Addr, ch ssh.NewChannel) bool {
	l.Lock()
	defer l.Unlock()
	for _, f := range l.entries {
		if laddr.Network() == f.laddr.Network() && laddr.String() == f.laddr.String() {
			f.c <- forward{newCh: ch, raddr: raddr}
			return true
		}
	}
	return false
}

type tcpUnresolveListener struct {
	laddr *config.Endpoint

	conn *Client
	in   <-chan forward
}

// Accept waits for and returns the next connection to the listener.
func (l *tcpUnresolveListener) Accept() (net.Conn, error) {
	s, ok := <-l.in
	if !ok {
		return nil, io.EOF
	}
	ch, incoming, err := s.newCh.Accept()
	if err != nil {
		return nil, err
	}
	go discardRequests(incoming)

	return &ChanConn{
		Channel: ch,
		laddr:   l.laddr,
		raddr:   s.raddr,
	}, nil
}

func discardRequests(in <-chan *ssh.Request) {
	for req := range in {
		fmt.Println("Discarding request:", req)
		if req.WantReply {
			req.Reply(false, nil)
		}
	}
}

// Close closes the listener.
func (l *tcpUnresolveListener) Close() error {
	m := channelForwardMsg{
		l.laddr.Host,
		uint32(l.laddr.Port),
	}

	// this also closes the listener.
	l.conn.forwards.remove(l.laddr)
	ok, _, err := l.conn.SendRequest("cancel-tcpip-forward", true, ssh.Marshal(&m))
	if err == nil && !ok {
		err = errors.New("ssh: cancel-tcpip-forward failed")
	}
	return err
}

// Addr returns the listener's network address.
func (l *tcpUnresolveListener) Addr() net.Addr {
	return l.laddr
}

// ChanConn fulfills the net.Conn interface without
// the tcpChan having to hold laddr or raddr directly.
type ChanConn struct {
	ssh.Channel
	laddr, raddr net.Addr
}

// LocalAddr returns the local network address.
func (t *ChanConn) LocalAddr() net.Addr {
	return t.laddr
}

// RemoteAddr returns the remote network address.
func (t *ChanConn) RemoteAddr() net.Addr {
	return t.raddr
}

// SetDeadline sets the read and write deadlines associated
// with the connection.
func (t *ChanConn) SetDeadline(deadline time.Time) error {
	if err := t.SetReadDeadline(deadline); err != nil {
		return err
	}
	return t.SetWriteDeadline(deadline)
}

// SetReadDeadline sets the read deadline.
// A zero value for t means Read will not time out.
// After the deadline, the error from Read will implement net.Error
// with Timeout() == true.
func (t *ChanConn) SetReadDeadline(deadline time.Time) error {
	// for compatibility with previous version,
	// the error message contains "tcpChan"
	return errors.New("ssh: tcpChan: deadline not supported")
}

// SetWriteDeadline exists to satisfy the net.Conn interface
// but is not implemented by this type.  It always returns an error.
func (t *ChanConn) SetWriteDeadline(deadline time.Time) error {
	return errors.New("ssh: tcpChan: deadline not supported")
}
