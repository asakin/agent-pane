//go:build unix

package cmd

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agent-pane/internal/peercred"
)

// maxPageRequest bounds one request datagram. macOS refuses unix datagrams
// over 2048 bytes by default, and a request is far smaller.
const maxPageRequest = 2048

// pageReplyWriteTimeout bounds writing one reply, and
// pageRequestReadTimeout reading one request from the session's socket.
var (
	pageReplyWriteTimeout  = 5 * time.Second
	pageRequestReadTimeout = 5 * time.Second
)

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
		s.handlePageRequest(reply, buf[:n], call)
	}
}

// handlePageRequest answers one request on reply, which it closes. The read
// itself runs on its own goroutine.
func (s *Session) handlePageRequest(reply *os.File, body []byte, call func() extensionCaller) {
	var req pageRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Method != "page.read" {
		writePageReply(reply, pageReply{ID: req.ID, Error: "bad request"})
		return
	}
	if errMsg := s.admitRead(reply); errMsg != "" {
		writePageReply(reply, pageReply{ID: req.ID, Error: errMsg})
		return
	}
	t, writeTimeout := currentPageTimeouts(), pageReplyWriteTimeout
	go func() {
		defer s.untrackReply(reply)
		ctx, cancel := s.requestContext(t.total(), watchHangup(reply))
		defer cancel()
		content, err := s.pageRead(ctx, t, call(), req)
		out := pageReply{ID: req.ID, Content: content}
		if err != nil {
			out = pageReply{ID: req.ID, Error: err.Error()}
		}
		writePageReplyWithin(reply, out, writeTimeout)
	}()
}

// admitRead records a reply socket so closing the session closes it, unless
// the session has closed or a limit on reads in flight is reached. It returns
// the reason it refused, or "".
func (s *Session) admitRead(reply *os.File) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errTerminalClosed
	}
	if len(s.replies) >= maxSessionPageReads {
		return errTooManyReads
	}
	if s.manager != nil && !s.manager.takePageRead() {
		return errTooManyReads
	}
	if s.replies == nil {
		s.replies = map[*os.File]struct{}{}
	}
	s.replies[reply] = struct{}{}
	return ""
}

func (s *Session) untrackReply(reply *os.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.replies[reply]; !ok {
		return
	}
	delete(s.replies, reply)
	if s.manager != nil {
		s.manager.releasePageRead()
	}
}

// watchHangup returns a channel that closes when the client end of reply
// goes away, or when reply itself is closed. The client never writes on its
// reply socket, so any read returning is the end of it.
func watchHangup(reply *os.File) <-chan struct{} {
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = reply.Read(make([]byte, 1))
	}()
	return gone
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
	writePageReplyWithin(reply, out, pageReplyWriteTimeout)
}

func writePageReplyWithin(reply *os.File, out pageReply, timeout time.Duration) {
	defer reply.Close()
	out.AgentPane = 1
	// A client that stops reading cannot hold the writer past this.
	if reply.SetWriteDeadline(time.Now().Add(timeout)) != nil {
		return
	}
	_ = json.NewEncoder(reply).Encode(out)
}

// pageClient sends one request over the inherited descriptor named by fdEnv
// and returns the content, or the host's error.
func pageClient(fdEnv string, req pageRequest) (string, error) {
	fd, err := pageDescriptor(fdEnv)
	if err != nil {
		return "", err
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

	return awaitPageReply(mine)
}

// pageDescriptor checks that the descriptor fdEnv names is a page channel,
// before anything is sent on it.
func pageDescriptor(fdEnv string) (int, error) {
	if fdEnv == "" {
		return 0, errors.New(errNotInTerminal)
	}
	fd, err := strconv.Atoi(fdEnv)
	if err != nil || fd < 0 {
		return 0, errors.New(errNotPageChannel)
	}
	if kind, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE); err != nil || kind != syscall.SOCK_DGRAM {
		return 0, errors.New(errNotPageChannel)
	}
	if sa, err := syscall.Getsockname(fd); err != nil {
		return 0, errors.New(errNotPageChannel)
	} else if _, ok := sa.(*syscall.SockaddrUnix); !ok {
		return 0, errors.New(errNotPageChannel)
	}
	return fd, nil
}

// readPage sends one request over the inherited descriptor, or, when this
// process did not inherit one, over the session's socket at socketEnv.
func readPage(fdEnv, socketEnv string, req pageRequest) (string, error) {
	if _, err := pageDescriptor(fdEnv); err != nil && socketEnv != "" {
		return pageSocketClient(socketEnv, req)
	}
	return pageClient(fdEnv, req)
}

// pageSocketClient sends one request over the session's socket. The host
// answers only processes started from that session.
func pageSocketClient(path string, req pageRequest) (string, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return "", errors.New(errTerminalClosed)
		}
		return "", errors.New(errNotPageSocket)
	}
	defer conn.Close()
	req.ID = strconv.Itoa(os.Getpid())
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(pageReplyWriteTimeout)); err != nil {
		return "", err
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return "", errors.New(errTerminalClosed)
	}
	return awaitPageReply(conn)
}

// awaitPageReply reads the host's one reply to a request.
func awaitPageReply(conn interface {
	io.Reader
	SetReadDeadline(time.Time) error
}) (string, error) {
	// Long enough for the person to answer the approval prompt; the host
	// gives up on the request a little before this.
	if err := conn.SetReadDeadline(time.Now().Add(pageRequestTimeout() + pageReplyWriteTimeout)); err != nil {
		return "", err
	}
	var out pageReply
	if err := json.NewDecoder(conn).Decode(&out); err != nil {
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

// pageSocketDir holds the sessions' page sockets.
var pageSocketDir = filepath.Join(dataDir, "run")

// newPageSocket listens on a fresh path for the session's processes that
// did not inherit the page descriptor, such as commands a coding agent's
// tool starts with only stdin, stdout and stderr. The folder and socket are
// the user's alone; closing the listener removes the socket.
func newPageSocket() (*net.UnixListener, string, error) {
	dir := pageSocketDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, strings.ToLower(rand.Text())+".sock")
	ln, err := listenPageSocket(path)
	if err != nil {
		return nil, "", err
	}
	return ln, path, nil
}

// listenPageSocket listens on path for a session's processes, replacing a
// socket file nothing answers on any more. A Durable session a new host
// takes back is served at the path its programs already have.
func listenPageSocket(path string) (*net.UnixListener, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode().Type() == os.ModeSocket {
		if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
			conn.Close()
			return nil, errPageSocketInUse
		}
		_ = os.Remove(path)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

var errPageSocketInUse = errors.New("another host still serves the page socket")

// servePageSocketAt serves the session's page socket at path, the one its
// programs were given, once no earlier host serves it, giving up after a few
// seconds or when the session closes.
func (s *Session) servePageSocketAt(path string, root int, call func() extensionCaller) {
	for range 40 {
		ln, err := listenPageSocket(path)
		if err == nil {
			s.mu.Lock()
			closed := s.closed
			if !closed {
				s.pageSocket = ln
			}
			s.mu.Unlock()
			if closed {
				ln.Close()
				return
			}
			s.servePageSocket(ln, root, call)
			return
		}
		if !errors.Is(err, errPageSocketInUse) || s.isClosed() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// servePageSocket answers requests on the session's socket until the
// session closes it. A connection counts as the session's only when its
// process is root, the process the session started, or descends from it, so
// knowing the path is not enough to read the page. Each connection carries
// one request line and then gets the reply, as on the page channel.
func (s *Session) servePageSocket(ln *net.UnixListener, root int, call func() extensionCaller) {
	policy := peercred.Policy{Ancestor: root}
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.isClosed() {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_, rejected := peercred.Check(conn, policy)
		reply, err := connFile(conn)
		if err != nil {
			continue
		}
		if rejected != nil {
			writePageReply(reply, pageReply{Error: errNotSessionProcess})
			continue
		}
		go func() {
			body, err := readRequestLine(reply)
			if err != nil {
				writePageReply(reply, pageReply{Error: "bad request"})
				return
			}
			s.handlePageRequest(reply, body, call)
		}()
	}
}

// connFile turns conn into the kind of reply descriptor the page channel
// uses, and closes conn.
func connFile(conn *net.UnixConn) (*os.File, error) {
	defer conn.Close()
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd, dupErr := -1, error(nil)
	if err := raw.Control(func(c uintptr) { fd, dupErr = syscall.Dup(int(c)) }); err != nil {
		return nil, err
	}
	if dupErr != nil {
		return nil, dupErr
	}
	syscall.CloseOnExec(fd)
	return pollableFile(fd, "agent-pane-page-reply")
}

// readRequestLine reads one newline-ended request of at most maxPageRequest
// bytes. The client writes nothing after it, so nothing past it is lost.
func readRequestLine(f *os.File) ([]byte, error) {
	if err := f.SetReadDeadline(time.Now().Add(pageRequestReadTimeout)); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, maxPageRequest)
	for len(buf) < maxPageRequest {
		n, err := f.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			return buf[:i], f.SetReadDeadline(time.Time{})
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, errors.New("request too large")
}
