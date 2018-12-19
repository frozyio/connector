package ssh

import (
	"fmt"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Client implements a traditional SSH client that supports shells,
// subprocesses, TCP port/streamlocal forwarding and tunneled dialing.
type Client struct {
	ssh.Conn

	handleForwardsOnce sync.Once // guards calling (*Client).handleForwards

	forwards        forwardList // forwarded tcpip connections from the remote side
	mu              sync.Mutex
	channelHandlers map[string]chan ssh.NewChannel
}

// HandleChannelOpen returns a channel on which NewChannel requests
// for the given type are sent. If the type already is being handled,
// nil is returned. The channel is closed when the connection is closed.
func (c *Client) HandleChannelOpen(channelType string) <-chan ssh.NewChannel {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channelHandlers == nil {
		// The SSH channel has been closed.
		c := make(chan ssh.NewChannel)
		close(c)
		return c
	}

	ch := c.channelHandlers[channelType]
	if ch != nil {
		return nil
	}

	ch = make(chan ssh.NewChannel, 16)
	c.channelHandlers[channelType] = ch
	return ch
}

// NewClient creates a Client on top of the given connection.
func NewClient(c ssh.Conn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) *Client {
	conn := &Client{
		Conn:            c,
		channelHandlers: make(map[string]chan ssh.NewChannel, 1),
	}

	go conn.handleGlobalRequests(reqs)
	go conn.handleChannelOpens(chans)
	go func() {
		conn.Wait()
		conn.forwards.closeAll()
	}()
	return conn
}

func (c *Client) handleGlobalRequests(incoming <-chan *ssh.Request) {
	for r := range incoming {
		// This handles keepalive messages and matches
		// the behaviour of OpenSSH.
		r.Reply(false, nil)
	}
}

// handleChannelOpens channel open messages from the remote side.
func (c *Client) handleChannelOpens(in <-chan ssh.NewChannel) {
	for ch := range in {
		c.mu.Lock()
		handler := c.channelHandlers[ch.ChannelType()]
		c.mu.Unlock()

		if handler != nil {
			handler <- ch
		} else {
			ch.Reject(ssh.UnknownChannelType, fmt.Sprintf("unknown channel type: %v", ch.ChannelType()))
		}
	}

	c.mu.Lock()
	for _, ch := range c.channelHandlers {
		close(ch)
	}
	c.channelHandlers = nil
	c.mu.Unlock()
}

// Dial starts a client connection to the given SSH server. It is a
// convenience function that connects to the given network address,
// initiates the SSH handshake, and then sets up a Client.  For access
// to incoming channels and requests, use net.Dial with NewClientConn
// instead.
func Dial(addr string, config *ssh.ClientConfig) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, config.Timeout)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		return nil, err
	}
	return NewClient(c, chans, reqs), nil
}
