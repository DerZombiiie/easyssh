package easyssh

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
)

// Applicaple SSH Request types for Port Forwarding - RFC 4254 7.X
const (
	DirectForwardRequest       = "direct-tcpip"         // RFC 4254 7.2
	RemoteForwardRequest       = "tcpip-forward"        // RFC 4254 7.1
	ForwardedTCPReturnRequest  = "forwarded-tcpip"      // RFC 4254 7.2
	CancelRemoteForwardRequest = "cancel-tcpip-forward" // RFC 4254 7.1
)

// TcpipForward is structure for RFC 4254 7.1 "tcpip-forward" request
type TcpipForward struct {
	Host string
	Port uint32
}

// DirectForward is struxture for RFC 4254 7.2 - can be used for "forwarded-tcpip" and "direct-tcpip"
type DirectForward struct {
	Host1 string
	Port1 uint32
	Host2 string
	Port2 uint32
}

func (p DirectForward) String() string {
	return fmt.Sprintf("CONNECT: %s:%d FROM: %s:%d", p.Host1, p.Port1, p.Host2, p.Port2)
}

// TCPIPForwardRequestHandler returns a GlobalRequestHandler that implements remote port forwarding - ssh -R
func TCPIPForwardRequestHandler() GlobalRequestHandler {
	return GlobalRequestHandlerFunc(TCPIPForwardRequest)
}

// TCPIPForwardRequest fulfills RFC 4254 7.1 "tcpip-forward" request
//
// TODO: Need to add state to handle "cancel-tcpip-forward"
func TCPIPForwardRequest(req *ssh.Request, sshConn ssh.Conn) {

	t := TcpipForward{}
	reply := (t.Port == 0) && req.WantReply
	ssh.Unmarshal(req.Payload, &t)
	addr := fmt.Sprintf("%s:%d", t.Host, t.Port)
	ln, err := net.Listen("tcp", addr) //tie to the client connection

	if err != nil {
		logger.Println("Unable to listen on address: ", addr)
		return
	}
	logger.Println("Listening on address: ", ln.Addr().String())

	quit := make(chan bool)

	if reply { // Client sent port 0. let them know which port is actually being used

		_, port, err := GetHostPortFromAddr(ln.Addr())
		if err != nil {
			return
		}

		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(port))
		t.Port = uint32(port)
		req.Reply(true, b)
	} else {
		req.Reply(true, nil)
	}

	go func() { // Handle incoming connections on this new listener
		for {
			select {
			case <-quit:

				return
			default:
				conn, err := ln.Accept()
				if err != nil { // Unable to accept new connection - listener likely closed
					continue
				}
				go func(conn net.Conn) {
					p := DirectForward{}
					var err error

					var portnum int
					p.Host1 = t.Host
					p.Port1 = t.Port
					p.Host2, portnum, err = GetHostPortFromAddr(conn.RemoteAddr())
					if err != nil {

						return
					}

					p.Port2 = uint32(portnum)
					ch, reqs, err := sshConn.OpenChannel(ForwardedTCPReturnRequest, ssh.Marshal(p))
					if err != nil {
						logger.Println("Open forwarded Channel: ", err.Error())
						return
					}
					go ssh.DiscardRequests(reqs)
					go func(ch ssh.Channel, conn net.Conn) {

						close := func() {
							ch.Close()
							conn.Close()

							// logger.Printf("forwarding closed")
						}

						go CopyReadWriters(conn, ch, close)

					}(ch, conn)

				}(conn)
			}

		}

	}()
	sshConn.Wait()
	logger.Println("Stop forwarding/listening on ", ln.Addr())
	ln.Close()
	quit <- true

}

func GetHostPortFromAddr(addr net.Addr) (host string, port int, err error) {
	host, portString, err := net.SplitHostPort(addr.String())
	if err != nil {
		return
	}
	port, err = strconv.Atoi(portString)
	return
}

// DirectPortForwardHandler returns a ChannelHandler that implements standard SSH direct portforwarding
func DirectPortForwardHandler() ChannelHandler { return ChannelHandlerFunc(DirectPortForwardChannel) }

// DirectPortForwardChannel acts as an SSH Direct Port Forwarder - ssh -L
//
// Should be  to channel type - "direct-tcpip"  - RFC 4254 7.2
func DirectPortForwardChannel(newChannel ssh.NewChannel, channel ssh.Channel, reqs <-chan *ssh.Request, sshConn ssh.Conn) {

	p := DirectForward{}
	ssh.Unmarshal(newChannel.ExtraData(), &p)
	logger.Println(p)

	go func(ch ssh.Channel, sshConn ssh.Conn) {
		addr := fmt.Sprintf("%s:%d", p.Host1, p.Port1)
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return
		}
		close := func() {
			ch.Close()
			conn.Close()

			//logger.Printf("forwarding closed")
		}

		go CopyReadWriters(conn, ch, close)

	}(channel, sshConn)

}
