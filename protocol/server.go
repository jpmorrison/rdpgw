package protocol

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/bolkedebruin/rdpgw/common"
	"io"
	"log"
	"net"
	"strconv"
	"time"
)

type VerifyTunnelCreate func(context.Context, string) (bool, error)
type VerifyTunnelAuthFunc func(context.Context, string) (bool, error)
type VerifyServerFunc func(context.Context, string) (bool, error)

type Server struct {
	Session              *SessionInfo
	VerifyTunnelCreate   VerifyTunnelCreate
	VerifyTunnelAuthFunc VerifyTunnelAuthFunc
	VerifyServerFunc     VerifyServerFunc
	RedirectFlags        int
	IdleTimeout          int
	SmartCardAuth        bool
	TokenAuth            bool
	ClientName           string
	Remote               net.Conn
	State                int
}

type ServerConf struct {
	VerifyTunnelCreate   VerifyTunnelCreate
	VerifyTunnelAuthFunc VerifyTunnelAuthFunc
	VerifyServerFunc     VerifyServerFunc
	RedirectFlags        RedirectFlags
	IdleTimeout          int
	SmartCardAuth        bool
	TokenAuth            bool
	ReceiveBuf			 int
	SendBuf				 int
}

func NewServer(s *SessionInfo, conf *ServerConf) *Server {
	h := &Server{
		State:				  SERVER_STATE_INITIAL,
		Session:              s,
		RedirectFlags:        makeRedirectFlags(conf.RedirectFlags),
		IdleTimeout:          conf.IdleTimeout,
		SmartCardAuth:        conf.SmartCardAuth,
		TokenAuth:            conf.TokenAuth,
		VerifyTunnelCreate:   conf.VerifyTunnelCreate,
		VerifyServerFunc:     conf.VerifyServerFunc,
		VerifyTunnelAuthFunc: conf.VerifyTunnelAuthFunc,
	}
	return h
}

const tunnelId = 10

func (s *Server) Process(ctx context.Context) error {
	for {
		pt, sz, pkt, err := readMessage(s.Session.TransportIn)
		if err != nil {
			log.Printf("Cannot read message from stream %s", err)
			return err
		}

		switch pt {
		case PKT_TYPE_HANDSHAKE_REQUEST:
			log.Printf("Client handshakeRequest from %s", common.GetClientIp(ctx))
			if s.State != SERVER_STATE_INITIAL {
				log.Printf("Handshake attempted while in wrong state %d != %d", s.State, SERVER_STATE_INITIAL)
				return errors.New("wrong state")
			}
			major, minor, _, _ := s.handshakeRequest(pkt) // todo check if auth matches what the handler can do
			msg := s.handshakeResponse(major, minor)
			s.Session.TransportOut.WritePacket(msg)
			s.State = SERVER_STATE_HANDSHAKE
		case PKT_TYPE_TUNNEL_CREATE:
			log.Printf("Tunnel create")
			if s.State != SERVER_STATE_HANDSHAKE {
				log.Printf("Tunnel create attempted while in wrong state %d != %d",
					s.State, SERVER_STATE_HANDSHAKE)
				return errors.New("wrong state")
			}
			_, cookie := s.tunnelRequest(pkt)
			if s.VerifyTunnelCreate != nil {
				if ok, _ := s.VerifyTunnelCreate(ctx, cookie); !ok {
					log.Printf("Invalid PAA cookie received from client %s", common.GetClientIp(ctx))
// FIXME: don't return if not using PAA 
//					return errors.New("invalid PAA cookie")
				}
			}
			msg := s.tunnelResponse()
			s.Session.TransportOut.WritePacket(msg)
			s.State = SERVER_STATE_TUNNEL_CREATE
		case PKT_TYPE_TUNNEL_AUTH:
			log.Printf("Tunnel auth")
			if s.State != SERVER_STATE_TUNNEL_CREATE {
				log.Printf("Tunnel auth attempted while in wrong state %d != %d",
					s.State, SERVER_STATE_TUNNEL_CREATE)
				return errors.New("wrong state")
			}
			client := s.tunnelAuthRequest(pkt)
			if s.VerifyTunnelAuthFunc != nil {
				if ok, _ := s.VerifyTunnelAuthFunc(ctx, client); !ok {
					log.Printf("Invalid client name: %s", client)
					return errors.New("invalid client name")
				}
			}
			msg := s.tunnelAuthResponse()
			s.Session.TransportOut.WritePacket(msg)
			s.State = SERVER_STATE_TUNNEL_AUTHORIZE
		case PKT_TYPE_CHANNEL_CREATE:
			log.Printf("Channel create")
			if s.State != SERVER_STATE_TUNNEL_AUTHORIZE {
				log.Printf("Channel create attempted while in wrong state %d != %d",
					s.State, SERVER_STATE_TUNNEL_AUTHORIZE)
				return errors.New("wrong state")
			}
			server, port := s.channelRequest(pkt)
			host := net.JoinHostPort(server, strconv.Itoa(int(port)))
			if s.VerifyServerFunc != nil {
				if ok, _ := s.VerifyServerFunc(ctx, host); !ok {
					log.Printf("Not allowed to connect to %s by policy handler", host)
					return errors.New("denied by security policy")
				}
			}
			log.Printf("Establishing connection to RDP server: %s", host)
			s.Remote, err = net.DialTimeout("tcp", host, time.Second*15)
			if err != nil {
				log.Printf("Error connecting to %s, %s", host, err)
				return err
			}
			log.Printf("Connection established")
			msg := s.channelResponse()
			s.Session.TransportOut.WritePacket(msg)

			// Make sure to start the flow from the RDP server first otherwise connections
			// might hang eventually
			go forward(s.Remote, s.Session.TransportOut)
			s.State = SERVER_STATE_CHANNEL_CREATE
		case PKT_TYPE_DATA:
			if s.State < SERVER_STATE_CHANNEL_CREATE {
				log.Printf("Data received while in wrong state %d != %d", s.State, SERVER_STATE_CHANNEL_CREATE)
				return errors.New("wrong state")
			}
			s.State = SERVER_STATE_OPENED
			receive(pkt, s.Remote)
		case PKT_TYPE_KEEPALIVE:
			// keepalives can be received while the channel is not open yet
			if s.State < SERVER_STATE_CHANNEL_CREATE {
				log.Printf("Keepalive received while in wrong state %d != %d", s.State, SERVER_STATE_CHANNEL_CREATE)
				return errors.New("wrong state")
			}

			// avoid concurrency issues
			// p.TransportIn.Write(createPacket(PKT_TYPE_KEEPALIVE, []byte{}))
		case PKT_TYPE_CLOSE_CHANNEL:
			log.Printf("Close channel")
			if s.State != SERVER_STATE_OPENED {
				log.Printf("Channel closed while in wrong state %d != %d", s.State, SERVER_STATE_OPENED)
				return errors.New("wrong state")
			}
			s.Session.TransportIn.Close()
			s.Session.TransportOut.Close()
			s.State = SERVER_STATE_CLOSED
		default:
			log.Printf("Unknown packet (size %d): %x", sz, pkt)
		}
	}
}

// Creates a packet the is a response to a handshakeRequest request
// HTTP_EXTENDED_AUTH_SSPI_NTLM is not supported in Linux
// but could be in Windows. However the NTLM protocol is insecure
func (s *Server) handshakeResponse(major byte, minor byte) []byte {
	var caps uint16
	if s.SmartCardAuth {
		caps = caps | HTTP_EXTENDED_AUTH_SC
	}
	if s.TokenAuth {
		caps = caps | HTTP_EXTENDED_AUTH_PAA
	}

	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, uint32(0)) // error_code
	buf.Write([]byte{major, minor})
	binary.Write(buf, binary.LittleEndian, uint16(0))    // server version
	binary.Write(buf, binary.LittleEndian, uint16(caps)) // extended auth

	return createPacket(PKT_TYPE_HANDSHAKE_RESPONSE, buf.Bytes())
}

func (s *Server) handshakeRequest(data []byte) (major byte, minor byte, version uint16, extAuth uint16) {
	r := bytes.NewReader(data)
	binary.Read(r, binary.LittleEndian, &major)
	binary.Read(r, binary.LittleEndian, &minor)
	binary.Read(r, binary.LittleEndian, &version)
	binary.Read(r, binary.LittleEndian, &extAuth)

	log.Printf("major: %d, minor: %d, version: %d, ext auth: %d", major, minor, version, extAuth)
	return
}

func (s *Server) tunnelRequest(data []byte) (caps uint32, cookie string) {
	var fields uint16

	r := bytes.NewReader(data)

	binary.Read(r, binary.LittleEndian, &caps)
	binary.Read(r, binary.LittleEndian, &fields)
	r.Seek(2, io.SeekCurrent)

	if fields == HTTP_TUNNEL_PACKET_FIELD_PAA_COOKIE {
		var size uint16
		binary.Read(r, binary.LittleEndian, &size)
		cookieB := make([]byte, size)
		r.Read(cookieB)
		cookie, _ = DecodeUTF16(cookieB)
	}
	return
}

func (s *Server) tunnelResponse() []byte {
	buf := new(bytes.Buffer)

	binary.Write(buf, binary.LittleEndian, uint16(0))                                                                    // server version
	binary.Write(buf, binary.LittleEndian, uint32(0))                                                                    // error code
	binary.Write(buf, binary.LittleEndian, uint16(HTTP_TUNNEL_RESPONSE_FIELD_TUNNEL_ID|HTTP_TUNNEL_RESPONSE_FIELD_CAPS)) // fields present
	binary.Write(buf, binary.LittleEndian, uint16(0))                                                                    // reserved

	// tunnel id (when is it used?)
	binary.Write(buf, binary.LittleEndian, uint32(tunnelId))

	binary.Write(buf, binary.LittleEndian, uint32(HTTP_CAPABILITY_IDLE_TIMEOUT))

	return createPacket(PKT_TYPE_TUNNEL_RESPONSE, buf.Bytes())
}

func (s *Server) tunnelAuthRequest(data []byte) string {
	buf := bytes.NewReader(data)

	var size uint16
	binary.Read(buf, binary.LittleEndian, &size)
	clData := make([]byte, size)
	binary.Read(buf, binary.LittleEndian, &clData)
	clientName, _ := DecodeUTF16(clData)

	return clientName
}

func (s *Server) tunnelAuthResponse() []byte {
	buf := new(bytes.Buffer)

	binary.Write(buf, binary.LittleEndian, uint32(0))                                                                                        // error code
	binary.Write(buf, binary.LittleEndian, uint16(HTTP_TUNNEL_AUTH_RESPONSE_FIELD_REDIR_FLAGS|HTTP_TUNNEL_AUTH_RESPONSE_FIELD_IDLE_TIMEOUT)) // fields present
	binary.Write(buf, binary.LittleEndian, uint16(0))                                                                                        // reserved

	// idle timeout
	if s.IdleTimeout < 0 {
		s.IdleTimeout = 0
	}

	binary.Write(buf, binary.LittleEndian, uint32(s.RedirectFlags)) // redir flags
	binary.Write(buf, binary.LittleEndian, uint32(s.IdleTimeout))   // timeout in minutes

	return createPacket(PKT_TYPE_TUNNEL_AUTH_RESPONSE, buf.Bytes())
}

func (s *Server) channelRequest(data []byte) (server string, port uint16) {
	buf := bytes.NewReader(data)

	var resourcesSize byte
	var alternative byte
	var protocol uint16
	var nameSize uint16

	binary.Read(buf, binary.LittleEndian, &resourcesSize)
	binary.Read(buf, binary.LittleEndian, &alternative)
	binary.Read(buf, binary.LittleEndian, &port)
	binary.Read(buf, binary.LittleEndian, &protocol)
	binary.Read(buf, binary.LittleEndian, &nameSize)

	nameData := make([]byte, nameSize)
	binary.Read(buf, binary.LittleEndian, &nameData)

	server, _ = DecodeUTF16(nameData)

	return
}

func (s *Server) channelResponse() []byte {
	buf := new(bytes.Buffer)

	binary.Write(buf, binary.LittleEndian, uint32(0))                                     // error code
	binary.Write(buf, binary.LittleEndian, uint16(HTTP_CHANNEL_RESPONSE_FIELD_CHANNELID)) // fields present
	binary.Write(buf, binary.LittleEndian, uint16(0))                                     // reserved

	// channel id is required for Windows clients
	binary.Write(buf, binary.LittleEndian, uint32(1)) // channel id

	// optional fields
	// channel id uint32 (4)
	// udp port uint16 (2)
	// udp auth cookie 1 byte for side channel
	// length uint16

	return createPacket(PKT_TYPE_CHANNEL_RESPONSE, buf.Bytes())
}

func makeRedirectFlags(flags RedirectFlags) int {
	var redir = 0

	if flags.DisableAll {
		return HTTP_TUNNEL_REDIR_DISABLE_ALL
	}
	if flags.EnableAll {
		return HTTP_TUNNEL_REDIR_ENABLE_ALL
	}

	if !flags.Port {
		redir = redir | HTTP_TUNNEL_REDIR_DISABLE_PORT
	}
	if !flags.Clipboard {
		redir = redir | HTTP_TUNNEL_REDIR_DISABLE_CLIPBOARD
	}
	if !flags.Drive {
		redir = redir | HTTP_TUNNEL_REDIR_DISABLE_DRIVE
	}
	if !flags.Pnp {
		redir = redir | HTTP_TUNNEL_REDIR_DISABLE_PNP
	}
	if !flags.Printer {
		redir = redir | HTTP_TUNNEL_REDIR_DISABLE_PRINTER
	}
	return redir
}
