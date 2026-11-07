//go:build unix

package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"
)

// maxPageRequest bounds one request datagram. macOS refuses unix datagrams
// over 2048 bytes by default, and a request is far smaller.
const maxPageRequest = 2048

// pageReplyWriteTimeout bounds writing one reply.
var pageReplyWriteTimeout = 5 * time.Second

// newPageChannel makes the socket pair for one session. The host keeps the
// first end; the second is passed to the shell as pageFD and closed here once
// the shell has it.
func newPageChannel() (host *net.UnixConn, child *os.File, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return nil, nil, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	hostFile := os.NewFile(uintptr(fds[0]), "agent-pane-page-host")
	defer hostFile.Close()
	conn, err := net.FileConn(hostFile)
	if err != nil {
		syscall.Close(fds[1])
		return nil, nil, err
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		syscall.Close(fds[1])
		return nil, nil, errors.New("page channel is not a unix socket")
	}
	return uc, os.NewFile(uintptr(fds[1]), "agent-pane-page"), nil
}

// servePage answers requests on the session's end of the page channel until
// the session closes it. Every request is answered on its own reply socket,
// from its own goroutine, so a read waiting for approval holds up nobody.
func (s *Session) servePage(conn *net.UnixConn, call func() extensionCaller) {
	buf := make([]byte, maxPageRequest)
	oob := make([]byte, syscall.CmsgSpace(4*4))
	for {
		n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if s.isClosed() {
				return
			}
			continue
		}
		reply := takeReplySocket(oob[:oobn])
		if reply == nil {
			continue
		}
		var req pageRequest
		if err := json.Unmarshal(buf[:n], &req); err != nil || req.Method != "page.read" {
			writePageReply(reply, pageReply{ID: req.ID, Error: "bad request"})
			continue
		}
		if !s.trackReply(reply) {
			writePageReply(reply, pageReply{ID: req.ID, Error: errTerminalClosed})
			continue
		}
		go func() {
			defer s.untrackReply(reply)
			content, err := s.pageRead(call(), req)
			out := pageReply{ID: req.ID, Content: content}
			if err != nil {
				out = pageReply{ID: req.ID, Error: err.Error()}
			}
			writePageReply(reply, out)
		}()
	}
}

// trackReply records a reply socket so closing the session closes it. It
// reports false once the session has closed.
func (s *Session) trackReply(reply *os.File) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.replies == nil {
		s.replies = map[*os.File]struct{}{}
	}
	s.replies[reply] = struct{}{}
	return true
}

func (s *Session) untrackReply(reply *os.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.replies, reply)
}

// pollableFile wraps a socket descriptor in non-blocking mode, so the
// runtime poller handles it and its deadlines take effect. A blocking
// descriptor refuses deadlines.
func pollableFile(fd int, name string) (*os.File, error) {
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

// takeReplySocket returns the first descriptor passed with a request and
// closes any others.
func takeReplySocket(oob []byte) *os.File {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil
	}
	var reply *os.File
	for i := range msgs {
		fds, err := syscall.ParseUnixRights(&msgs[i])
		if err != nil {
			continue
		}
		for _, fd := range fds {
			syscall.CloseOnExec(fd)
			if reply == nil {
				reply, _ = pollableFile(fd, "agent-pane-page-reply")
			} else {
				syscall.Close(fd)
			}
		}
	}
	return reply
}

func writePageReply(reply *os.File, out pageReply) {
	defer reply.Close()
	out.AgentPane = 1
	// A client that stops reading cannot hold the writer past this.
	if reply.SetWriteDeadline(time.Now().Add(pageReplyWriteTimeout)) != nil {
		return
	}
	_ = json.NewEncoder(reply).Encode(out)
}

// pageClient sends one request over the inherited descriptor named by fdEnv
// and returns the content, or the host's error.
func pageClient(fdEnv string, req pageRequest) (string, error) {
	if fdEnv == "" {
		return "", errors.New(errNotInTerminal)
	}
	fd, err := strconv.Atoi(fdEnv)
	if err != nil || fd < 0 {
		return "", errors.New(errNotPageChannel)
	}
	if kind, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE); err != nil || kind != syscall.SOCK_DGRAM {
		return "", errors.New(errNotPageChannel)
	}
	if sa, err := syscall.Getsockname(fd); err != nil {
		return "", errors.New(errNotPageChannel)
	} else if _, ok := sa.(*syscall.SockaddrUnix); !ok {
		return "", errors.New(errNotPageChannel)
	}

	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return "", err
	}
	syscall.CloseOnExec(pair[0])
	syscall.CloseOnExec(pair[1])
	mine, err := pollableFile(pair[0], "agent-pane-page-reply")
	if err != nil {
		syscall.Close(pair[1])
		return "", err
	}
	defer mine.Close()

	req.ID = strconv.Itoa(os.Getpid())
	body, err := json.Marshal(req)
	if err != nil {
		syscall.Close(pair[1])
		return "", err
	}
	err = syscall.Sendmsg(fd, body, syscall.UnixRights(pair[1]), nil, 0)
	syscall.Close(pair[1])
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ENOTCONN) || errors.Is(err, syscall.EDESTADDRREQ) {
			return "", errors.New(errTerminalClosed)
		}
		return "", errors.New(errNotPageChannel)
	}

	// Long enough for the person to answer the approval prompt; the host
	// gives up on the request a little before this.
	if err := mine.SetReadDeadline(time.Now().Add(pageRequestTimeout() + pageReplyWriteTimeout)); err != nil {
		return "", err
	}
	var out pageReply
	if err := json.NewDecoder(mine).Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return "", errors.New(errTerminalClosed)
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return "", errors.New("timed out waiting for Agent Pane")
		}
		return "", errors.New(errNotPageChannel)
	}
	if out.AgentPane != 1 {
		return "", errors.New(errNotPageChannel)
	}
	if out.Error != "" {
		return "", errors.New(out.Error)
	}
	return out.Content, nil
}
