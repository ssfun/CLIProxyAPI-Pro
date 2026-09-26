package socks5

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	version5          = 0x05
	methodNoAuth      = 0x00
	methodUnsupported = 0xff
	commandConnect    = 0x01
	addressIPv4       = 0x01
	addressDomain     = 0x03
	addressIPv6       = 0x04
	replySucceeded    = 0x00
	replyGeneral      = 0x01
	replyNotAllowed   = 0x02
	replyHost         = 0x04
	replyRefused      = 0x05
	replyCommand      = 0x07
	replyAddress      = 0x08
	handshakeTimeout  = 15 * time.Second
)

type DialResult struct {
	Conn    net.Conn
	Release func()
}

type DialFunc func(context.Context, string) (DialResult, error)

type Server struct {
	listener net.Listener
	dial     DialFunc

	ctx               context.Context
	cancel            context.CancelFunc
	startOnce         sync.Once
	stopAcceptingOnce sync.Once
	closeOnce         sync.Once
	wg                sync.WaitGroup
	conns             sync.Map
}

// New constructs a SOCKS5 server without starting its accept loop. Callers can
// publish all state used by dial before Start makes the listener observable.
func New(listener net.Listener, dial DialFunc) (*Server, error) {
	if listener == nil {
		return nil, fmt.Errorf("SOCKS5 listener is nil")
	}
	if dial == nil {
		return nil, fmt.Errorf("SOCKS5 dial function is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{listener: listener, dial: dial, ctx: ctx, cancel: cancel}, nil
}

func (s *Server) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if s.ctx.Err() != nil {
			return
		}
		s.wg.Add(1)
		go s.acceptLoop()
	})
}

func (s *Server) Address() string {
	if s == nil || s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// StopAccepting synchronously releases the listening socket while allowing
// existing tunnels and background shutdown work to be completed separately.
func (s *Server) StopAccepting() {
	if s == nil {
		return
	}
	s.stopAcceptingOnce.Do(func() {
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
}

func (s *Server) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.cancel()
		s.StopAccepting()
		s.conns.Range(func(key, _ any) bool {
			if conn, ok := key.(net.Conn); ok {
				_ = conn.Close()
			}
			return true
		})
		s.wg.Wait()
	})
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, errAccept := s.listener.Accept()
		if errAccept != nil {
			if s.ctx.Err() != nil || errors.Is(errAccept, net.ErrClosed) {
				return
			}
			continue
		}
		s.conns.Store(conn, struct{}{})
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.conns.Delete(conn)
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *Server) handle(client net.Conn) {
	_ = client.SetDeadline(time.Now().Add(handshakeTimeout))
	reader := bufio.NewReader(client)
	if errMethod := negotiateMethod(reader, client); errMethod != nil {
		return
	}
	target, reply, errTarget := readTarget(reader)
	if errTarget != nil {
		_ = writeReply(client, reply, nil)
		return
	}
	result, errDial := s.dial(s.ctx, target)
	if errDial != nil || result.Conn == nil {
		_ = writeReply(client, replyForDialError(errDial), nil)
		return
	}
	upstream := result.Conn
	if result.Release != nil {
		defer result.Release()
	}
	defer upstream.Close()
	s.conns.Store(upstream, struct{}{})
	defer s.conns.Delete(upstream)
	if errReply := writeReply(client, replySucceeded, upstream.LocalAddr()); errReply != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})
	relay(client, reader, upstream)
}

func negotiateMethod(reader *bufio.Reader, writer io.Writer) error {
	header := make([]byte, 2)
	if _, errRead := io.ReadFull(reader, header); errRead != nil {
		return errRead
	}
	if header[0] != version5 || header[1] == 0 {
		return fmt.Errorf("invalid SOCKS5 greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, errRead := io.ReadFull(reader, methods); errRead != nil {
		return errRead
	}
	selected := byte(methodUnsupported)
	for _, method := range methods {
		if method == methodNoAuth {
			selected = methodNoAuth
			break
		}
	}
	if _, errWrite := writer.Write([]byte{version5, selected}); errWrite != nil {
		return errWrite
	}
	if selected == methodUnsupported {
		return fmt.Errorf("SOCKS5 client does not support no-auth")
	}
	return nil
}

func readTarget(reader *bufio.Reader) (string, byte, error) {
	header := make([]byte, 4)
	if _, errRead := io.ReadFull(reader, header); errRead != nil {
		return "", replyGeneral, errRead
	}
	if header[0] != version5 {
		return "", replyGeneral, fmt.Errorf("invalid SOCKS5 request version")
	}
	if header[1] != commandConnect {
		return "", replyCommand, fmt.Errorf("unsupported SOCKS5 command")
	}
	var host string
	switch header[3] {
	case addressIPv4:
		value := make([]byte, net.IPv4len)
		if _, errRead := io.ReadFull(reader, value); errRead != nil {
			return "", replyAddress, errRead
		}
		host = net.IP(value).String()
	case addressIPv6:
		value := make([]byte, net.IPv6len)
		if _, errRead := io.ReadFull(reader, value); errRead != nil {
			return "", replyAddress, errRead
		}
		host = net.IP(value).String()
	case addressDomain:
		length, errRead := reader.ReadByte()
		if errRead != nil || length == 0 {
			return "", replyAddress, fmt.Errorf("invalid SOCKS5 domain")
		}
		value := make([]byte, int(length))
		if _, errRead = io.ReadFull(reader, value); errRead != nil {
			return "", replyAddress, errRead
		}
		host = string(value)
	default:
		return "", replyAddress, fmt.Errorf("unsupported SOCKS5 address type")
	}
	portBytes := make([]byte, 2)
	if _, errRead := io.ReadFull(reader, portBytes); errRead != nil {
		return "", replyAddress, errRead
	}
	port := binary.BigEndian.Uint16(portBytes)
	if port == 0 {
		return "", replyAddress, fmt.Errorf("invalid SOCKS5 target port")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), replySucceeded, nil
}

func writeReply(writer io.Writer, reply byte, address net.Addr) error {
	ip := net.IPv4zero
	port := 0
	if tcpAddress, ok := address.(*net.TCPAddr); ok && tcpAddress != nil {
		ip = tcpAddress.IP
		port = tcpAddress.Port
	}
	response := []byte{version5, reply, 0x00}
	if ip4 := ip.To4(); ip4 != nil {
		response = append(response, addressIPv4)
		response = append(response, ip4...)
	} else {
		response = append(response, addressIPv6)
		response = append(response, ip.To16()...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	response = append(response, portBytes...)
	_, errWrite := writer.Write(response)
	return errWrite
}

func replyForDialError(err error) byte {
	if err == nil {
		return replyGeneral
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return replyHost
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return replyHost
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return replyHost
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "refused") {
		return replyRefused
	}
	if strings.Contains(message, "not allowed") {
		return replyNotAllowed
	}
	return replyGeneral
}

func relay(client net.Conn, clientReader io.Reader, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, clientReader)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
	}()
	wg.Wait()
}

func closeWrite(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.CloseWrite()
		return
	}
	_ = conn.Close()
}
