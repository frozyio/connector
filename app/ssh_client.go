package app

import (
	"fmt"
	"net"
	"sync"

	"gitlab.com/frozy.io/connector/config"
	"golang.org/x/crypto/ssh"
)

// client like a ssh.client
type client struct {
	ssh.Conn
	fwd             forwardChan
	mu              sync.Mutex
	channelHandlers map[string]chan ssh.NewChannel
}

// HandleChannelOpen returns a channel on which NewChannel requests
// for the given type are sent. If the type already is being handled,
// nil is returned. The channel is closed when the connection is closed.
func (c *client) HandleChannelOpen(channelType string) <-chan ssh.NewChannel {
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
func newClient(c ssh.Conn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) *client {
	conn := &client{
		Conn:            c,
		channelHandlers: make(map[string]chan ssh.NewChannel, 1),
	}

	go conn.handleGlobalRequests(reqs)
	go conn.handleChannelOpens(chans)
	go conn.handleChannels(conn.HandleChannelOpen("forwarded-tcpip"))
	go func() {
		conn.Wait()
		close(conn.fwd.channel)
	}()

	return conn
}

func (c *client) handleGlobalRequests(incoming <-chan *ssh.Request) {
	for r := range incoming {
		// This handles keepalive messages and matches
		// the behaviour of OpenSSH.
		r.Reply(false, nil)
	}
}

func (c *client) registerForward(addr config.Endpoint) chan forward {
	c.fwd = forwardChan{
		laddr:   addr,
		channel: make(chan forward, 1),
	}
	return c.fwd.channel
}

// handleChannelOpens channel open messages from the remote side.
func (c *client) handleChannelOpens(in <-chan ssh.NewChannel) {
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

func (c *client) forward(laddr, raddr net.Addr, ch ssh.NewChannel) bool {
	c.fwd.channel <- forward{newCh: ch, raddr: raddr}
	return true
}

func (c *client) handleChannels(in <-chan ssh.NewChannel) {
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

			laddr = config.Endpoint{payload.Addr, uint16(payload.Port)}
			raddr = config.Endpoint{payload.OriginAddr, uint16(payload.OriginPort)}
		default:
			panic(fmt.Errorf("ssh: unknown channel type %s", channelType))
		}
		if ok := c.forward(laddr, raddr, ch); !ok {
			// Section 7.2, implementations MUST reject spurious incoming
			// connections.
			ch.Reject(ssh.Prohibited, "no forward for address")
			continue
		}
	}
}
